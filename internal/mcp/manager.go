package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Options configures a [Manager].
type Options struct {
	// Client identifies hint to every server. Empty values become "hint"
	// and "dev".
	Client ClientInfo
	// ProjectRoot is the canonical project directory, the first half of
	// a consent key.
	ProjectRoot string
	// Environ is the base environment of a stdio server, already
	// stripped of hint's API keys. Nil uses the process environment.
	Environ []string
	// Consent decides whether a project server may start. Nil disables
	// every project server.
	Consent Consent
	// Budget is the MCP schema budget. Zero uses [DefaultBudget].
	Budget Budget
	// ConnectTimeout bounds one connection attempt. Zero uses
	// DefaultConnectTimeout; a hanging server must not hold the run.
	ConnectTimeout time.Duration
	// ListTimeout bounds one tools/list exchange. Zero uses
	// DefaultListTimeout; the handshake alone is not enough of a bound.
	ListTimeout time.Duration
	// Dial replaces the real transport in tests.
	Dial DialFunc
	// Now and Backoff replace the clock and the restart delay in tests.
	Now     func() time.Time
	Backoff func(attempt int) time.Duration
}

// Manager owns the MCP servers of one run: it connects them on the first
// Tools call, lists their tools once, applies the schema budget, and
// reconnects a dead session. It is safe for concurrent tool calls.
type Manager struct {
	servers        []*server
	budget         Budget
	connectTimeout time.Duration
	listTimeout    time.Duration
	dial           DialFunc
	now            func() time.Time
	backoff        func(attempt int) time.Duration

	mu       sync.Mutex
	closed   bool
	warnings []string
	warned   map[string]bool
	hidden   map[string]string
}

// NewManager validates the configurations and builds the run's servers.
// A project server is checked for consent here and stays disabled, with a
// warning, when approval is missing or its definition changed.
func NewManager(cfgs []ServerConfig, opts Options) (*Manager, error) {
	opts.Budget = opts.Budget.normalize()
	m := &Manager{
		budget:  opts.Budget,
		now:     opts.Now,
		backoff: opts.Backoff,
		warned:  map[string]bool{},
		hidden:  map[string]string{},
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.backoff == nil {
		m.backoff = defaultBackoff
	}
	m.connectTimeout = opts.ConnectTimeout
	if m.connectTimeout <= 0 {
		m.connectTimeout = DefaultConnectTimeout
	}
	m.listTimeout = opts.ListTimeout
	if m.listTimeout <= 0 {
		m.listTimeout = DefaultListTimeout
	}
	if opts.Dial == nil {
		conn := &connector{info: opts.Client, environ: opts.Environ}
		m.dial = conn.dial
	} else {
		m.dial = opts.Dial
	}
	seen := make(map[string]bool, len(cfgs))
	for i := range cfgs {
		cfg := cfgs[i]
		cfg.normalize()
		if err := cfg.Validate(); err != nil {
			return nil, err
		}
		if seen[cfg.Name] {
			return nil, fmt.Errorf("mcp: duplicate server %q", cfg.Name)
		}
		seen[cfg.Name] = true
		srv := &server{cfg: cfg, mgr: m, enabled: true}
		if cfg.Project {
			switch opts.Consent {
			case nil:
				srv.enabled = false
				srv.reason = fmt.Sprintf("MCP server %q comes from the project file and is disabled until consent is recorded; run `hint mcp enable %s`", cfg.Name, cfg.Name)
			default:
				ok, reason := opts.Consent.Check(opts.ProjectRoot, cfg)
				srv.enabled = ok
				srv.reason = reason
			}
			if srv.reason != "" {
				m.warnf("%s", srv.reason)
			}
		}
		m.servers = append(m.servers, srv)
	}
	return m, nil
}

// Tools connects every enabled server that is not connected yet, lists
// its tools, and returns the adapters that fit the budget. A server that
// cannot be reached is a warning, not a run failure: a broken MCP server
// must not stop hint from answering with its built-in tools.
func (m *Manager) Tools(ctx context.Context) ([]agentapi.Tool, error) {
	m.mu.Lock()
	closed := m.closed
	m.mu.Unlock()
	if closed {
		return nil, errors.New("mcp: manager is closed")
	}
	var out []agentapi.Tool
	total := 0
	for _, srv := range m.servers {
		if !srv.enabled {
			continue
		}
		tools, err := srv.listTools(ctx)
		if err != nil {
			srv.setLastError(err)
			m.clearHidden(srv.cfg.Name)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			m.warnf("MCP server %q is unavailable: %v", srv.cfg.Name, err)
			continue
		}
		srv.setLastError(nil)
		size := 0
		for _, t := range tools {
			size += schemaSize(t)
		}
		if m.budget.PerServer > 0 && size > m.budget.PerServer {
			reason := hideReasonServer(srv.cfg.Name, size, m.budget.PerServer)
			m.setHidden(srv.cfg.Name, reason)
			m.warnf("%s", reason)
			continue
		}
		if m.budget.Total > 0 && total+size > m.budget.Total {
			reason := hideReasonTotal(srv.cfg.Name, size, total, m.budget.Total)
			m.setHidden(srv.cfg.Name, reason)
			m.warnf("%s", reason)
			continue
		}
		m.clearHidden(srv.cfg.Name)
		out = append(out, tools...)
		total += size
	}
	return out, nil
}

func (m *Manager) setHidden(name, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hidden[name] = reason
}

func (m *Manager) clearHidden(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hidden, name)
}

// Warnings returns the run's MCP warnings, one per distinct problem, in
// the order they were found.
func (m *Manager) Warnings() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.warnings...)
}

// Close closes every session and waits for a stdio child to stop. It is
// idempotent.
func (m *Manager) Close() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	servers := append([]*server(nil), m.servers...)
	m.mu.Unlock()
	var errs []error
	for _, srv := range servers {
		if err := srv.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (m *Manager) warnf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.warned[msg] {
		return
	}
	m.warned[msg] = true
	m.warnings = append(m.warnings, msg)
}

func defaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	d := 100 * time.Millisecond * time.Duration(1<<(attempt-1))
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

// server is one configured server and its on-demand connection.
type server struct {
	cfg ServerConfig
	mgr *Manager

	enabled bool
	reason  string

	mu       sync.Mutex
	sess     *mcp.ClientSession
	tools    []agentapi.Tool
	lastErr  error
	failures int
	next     time.Time
}

// listTools connects if needed and lists the server's tools once. A
// reconnect clears the cache, so a restarted server is re-listed before
// the next call that needs its schemas.
func (s *server) listTools(ctx context.Context) ([]agentapi.Tool, error) {
	s.mu.Lock()
	if s.tools != nil {
		tools := s.tools
		s.mu.Unlock()
		return tools, nil
	}
	if err := s.connectLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	sess := s.sess
	s.mu.Unlock()

	timeout := s.mgr.listTimeout
	if s.cfg.Timeout > 0 && s.cfg.Timeout < timeout {
		timeout = s.cfg.Timeout
	}
	listCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	out := make([]agentapi.Tool, 0)
	for remote, err := range sess.Tools(listCtx, nil) {
		if err != nil {
			if isConnectionClosed(err) {
				s.dropIf(sess)
			}
			return nil, fmt.Errorf("listing tools: %w", err)
		}
		if !s.cfg.allows(remote.Name) {
			continue
		}
		t, err := newToolAdapter(s, remote)
		if err != nil {
			s.mgr.warnf("%v", err)
			continue
		}
		out = append(out, limitedTool(t, s.cfg))
	}
	s.mu.Lock()
	if s.sess == sess {
		s.tools = out
	}
	s.mu.Unlock()
	return out, nil
}

// call executes one tool. A read-class call is retried once on a lost
// connection; a write or execute call is not, because the first attempt
// may have reached the server and retrying could apply it twice.
func (s *server) call(ctx context.Context, name string, args json.RawMessage, class agentapi.ActionClass) (*mcp.CallToolResult, error) {
	res, sess, err := s.callOnce(ctx, name, args)
	if err == nil {
		return res, nil
	}
	if !isConnectionClosed(err) {
		return nil, err
	}
	s.dropIf(sess)
	if class != agentapi.ClassRead {
		return nil, fmt.Errorf("mcp: server %q: connection lost; the %s call may have reached the server and was not retried: %w", s.cfg.Name, class, err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	res, _, err = s.callOnce(ctx, name, args)
	return res, err
}

func (s *server) callOnce(ctx context.Context, name string, args json.RawMessage) (*mcp.CallToolResult, *mcp.ClientSession, error) {
	s.mu.Lock()
	if err := s.connectLocked(ctx); err != nil {
		s.mu.Unlock()
		return nil, nil, err
	}
	sess := s.sess
	s.mu.Unlock()
	if sess == nil {
		return nil, nil, fmt.Errorf("mcp: server %q is not connected", s.cfg.Name)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	return res, sess, err
}

func (s *server) connectLocked(ctx context.Context) error {
	if s.sess != nil {
		return nil
	}
	now := s.mgr.now()
	if now.Before(s.next) {
		return fmt.Errorf("restart backoff: %s left", s.next.Sub(now).Round(time.Millisecond))
	}
	timeout := s.mgr.connectTimeout
	if s.cfg.Timeout > 0 && s.cfg.Timeout < timeout {
		timeout = s.cfg.Timeout
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	sess, err := s.mgr.dial(connectCtx, s.cfg)
	if err != nil {
		s.failures++
		s.next = now.Add(s.mgr.backoff(s.failures))
		return err
	}
	s.sess = sess
	s.tools = nil
	s.failures = 0
	s.next = time.Time{}
	return nil
}

// dropIf closes sess only if it is still the server's current session.
// A concurrent call may already have replaced it; closing the new one
// would make the next call fail for no reason.
func (s *server) setLastError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastErr = err
}

func (s *server) lastError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}

func (s *server) dropIf(sess *mcp.ClientSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sess != sess {
		return
	}
	s.dropLocked()
}

func (s *server) dropLocked() {
	if s.sess != nil {
		_ = s.sess.Close()
		s.sess = nil
	}
	s.tools = nil
}

func (s *server) close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var err error
	if s.sess != nil {
		err = s.sess.Close()
		s.sess = nil
	}
	s.tools = nil
	return err
}

// isConnectionClosed reports a session that can be reconnected. A
// protocol error is not one: the server answered, so a retry would only
// repeat the same failure.
func isConnectionClosed(err error) bool {
	if errors.Is(err, mcp.ErrConnectionClosed) || errors.Is(err, mcp.ErrSessionMissing) || errors.Is(err, io.EOF) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, mcp.ErrConnectionClosed.Error()) ||
		strings.Contains(msg, "client is closing") ||
		strings.Contains(msg, "EOF")
}
