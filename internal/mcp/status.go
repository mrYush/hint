package mcp

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/mrYush/hint/pkg/agentapi"
)

// State is the lifecycle state of one configured MCP server.
type State string

const (
	// StateConfigured is an enabled server that has not been contacted
	// yet; `hint mcp list` uses it.
	StateConfigured State = "configured"
	// StateRunning is a server that is connected and whose tools fit the
	// budget.
	StateRunning State = "running"
	// StateDisabled is a project server without a matching consent
	// record.
	StateDisabled State = "disabled"
	// StateFailed is a server whose connection or tool listing failed.
	StateFailed State = "failed"
	// StateHidden is a connected server whose tools were kept out of the
	// prompt by the schema budget.
	StateHidden State = "hidden"
)

// ServerInfo is the part of a server known without connecting to it.
type ServerInfo struct {
	Name       string
	Transport  Transport
	Permission agentapi.ActionClass
	Enabled    bool
	Reason     string
	Tools      ToolFilter
	Resources  []string
	Timeout    time.Duration
}

// ServerStatus is what `hint mcp status` reports after a connection.
type ServerStatus struct {
	Name       string
	Transport  Transport
	Permission agentapi.ActionClass
	State      State
	Reason     string
	Protocol   string
	ServerName string
	Tools      int
	Bytes      int
	Resources  []string
	Health     string
}

// Servers returns the configured servers without connecting to any of
// them. It honors the consent check done by NewManager.
func (m *Manager) Servers() []ServerInfo {
	out := make([]ServerInfo, 0, len(m.servers))
	for _, srv := range m.servers {
		out = append(out, ServerInfo{
			Name:       srv.cfg.Name,
			Transport:  srv.cfg.Transport,
			Permission: srv.cfg.Permission,
			Enabled:    srv.enabled,
			Reason:     srv.reason,
			Tools: ToolFilter{
				Allow: append([]string(nil), srv.cfg.Tools.Allow...),
				Deny:  append([]string(nil), srv.cfg.Tools.Deny...),
			},
			Resources: append([]string(nil), srv.cfg.Resources...),
			Timeout:   srv.cfg.Timeout,
		})
	}
	return out
}

// Status connects every enabled server, applies the schema budget, and
// reports each server's state, protocol, tool count, configured resource count
// and health without reading resource bodies. A server that fails is reported
// as failed; the others still get a status.
func (m *Manager) Status(ctx context.Context) ([]ServerStatus, error) {
	if _, err := m.Tools(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	hidden := make(map[string]string, len(m.hidden))
	for name, reason := range m.hidden {
		hidden[name] = reason
	}
	m.mu.Unlock()

	out := make([]ServerStatus, 0, len(m.servers))
	for _, srv := range m.servers {
		st := ServerStatus{
			Name:       srv.cfg.Name,
			Transport:  srv.cfg.Transport,
			Permission: srv.cfg.Permission,
			Resources:  append([]string(nil), srv.cfg.Resources...),
		}
		if !srv.enabled {
			st.State = StateDisabled
			st.Reason = srv.reason
			out = append(out, st)
			continue
		}
		st.Tools = srv.toolCount()
		st.Bytes = srv.toolBytes()
		st.Protocol, st.ServerName = srv.initInfo()
		if reason, ok := hidden[srv.cfg.Name]; ok {
			st.State = StateHidden
			st.Reason = reason
		} else if err := srv.lastError(); err != nil {
			st.State = StateFailed
			st.Reason = err.Error()
			out = append(out, st)
			continue
		} else {
			st.State = StateRunning
		}
		health, err := srv.ping(ctx)
		if err != nil {
			st.Health = "error: " + err.Error()
		} else {
			st.Health = health
		}
		out = append(out, st)
	}
	return out, nil
}

// pingTimeout bounds the health check; a server that stops answering
// must not hold `hint mcp status` indefinitely.
const pingTimeout = 5 * time.Second

// ping performs the MCP ping health check on a legacy server. The
// 2026-07-28 revision removed ping; there the successful discovery done
// by Connect is the health check, and a method-not-found answer falls
// back to it instead of reporting a healthy server as broken.
func (s *server) ping(ctx context.Context) (string, error) {
	s.mu.Lock()
	sess := s.sess
	modern := false
	if sess != nil {
		if res := sess.InitializeResult(); res != nil && res.ProtocolVersion >= "2026-07-28" {
			modern = true
		}
	}
	s.mu.Unlock()
	if sess == nil {
		return "", errors.New("not connected")
	}
	timeout := s.cfg.Timeout
	if timeout <= 0 || timeout > pingTimeout {
		timeout = pingTimeout
	}
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := sess.Ping(pingCtx, nil); err != nil {
		if modern && isMethodNotFound(err) {
			return "discover", nil
		}
		return "", err
	}
	return "ping", nil
}

func isMethodNotFound(err error) bool {
	var werr *jsonrpc.Error
	return errors.As(err, &werr) && werr.Code == jsonrpc.CodeMethodNotFound
}

func (s *server) initInfo() (protocol, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess == nil {
		return "", ""
	}
	res := s.sess.InitializeResult()
	if res == nil {
		return "", ""
	}
	if res.ServerInfo != nil {
		name = res.ServerInfo.Name
	}
	return res.ProtocolVersion, name
}

func (s *server) toolCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tools)
}

func (s *server) toolBytes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, t := range s.tools {
		total += schemaSize(t)
	}
	return total
}
