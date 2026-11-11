package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestSummarizeArgsCutsOnRuneBoundary(t *testing.T) {
	got := SummarizeArgs([]byte(strings.Repeat("Привет ", 30)))
	if !utf8.ValidString(got) {
		t.Fatalf("SummarizeArgs returned invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("SummarizeArgs returned a replacement rune: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("long arguments were not cut: %q", got)
	}
}

func TestExplain(t *testing.T) {
	auth := agentapi.NewError(agentapi.ErrAuth, "Unauthorized")
	if got := Explain(auth); !strings.Contains(got, "Unauthorized") || !strings.Contains(got, "api_key") {
		t.Errorf("Explain(auth) = %q; want the error and what to check", got)
	}
	other := agentapi.NewError(agentapi.ErrNetwork, "connection refused")
	if got := Explain(other); got != other.Error() {
		t.Errorf("Explain(network) = %q; want the bare error", got)
	}
	if got := Explain(errors.New("plain")); got != "plain" {
		t.Errorf("Explain(plain) = %q", got)
	}
	if got := Advice(fmt.Errorf("one-shot: %w", auth)); !strings.Contains(got, "api_key") {
		t.Errorf("Advice(wrapped auth) = %q; want what to check", got)
	}
	if got := Advice(other); got != "" {
		t.Errorf("Advice(network) = %q; want none", got)
	}
}

// silentCore answers hello and then reads every request without
// answering it: a core that took turn.start and wedged.
func silentCore(t *testing.T, conn net.Conn) {
	t.Helper()
	go func() {
		dec := json.NewDecoder(conn)
		enc := json.NewEncoder(conn)
		for {
			var req struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := dec.Decode(&req); err != nil {
				return
			}
			if req.Method == "hello" {
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"protocol_version": rpc.ProtocolVersion, "server": "silent"},
				})
			}
		}
	}()
}

func TestRunTurnGivesUpOnASilentCore(t *testing.T) {
	saved := startWait
	startWait = 50 * time.Millisecond
	t.Cleanup(func() { startWait = saved })

	client, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	silentCore(t, server)
	cl, err := rpc.Dial(context.Background(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })

	// A cancelled context must not end the wait early (the start runs to
	// its answer), but startWait must.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		_, err := RunTurn(ctx, cl, "s1", "hi", nil)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "did not answer turn.start") {
			t.Errorf("RunTurn = %v; want the start timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunTurn still waits for a core that never answers")
	}
}

// testSink lets a test supply the permission answer and ignores the
// display callbacks.
type testSink struct {
	permission func(context.Context, agentapi.PermissionRequest) (permission.Decision, error)
}

func (testSink) Text(string)                 {}
func (testSink) ToolStart(agentapi.ToolCall) {}
func (testSink) ToolEnd(agentapi.ToolResult) {}
func (testSink) Notice(string)               {}

func (s testSink) Permission(ctx context.Context, req agentapi.PermissionRequest) (permission.Decision, error) {
	return s.permission(ctx, req)
}

// permissionCore is a minimal core for the permission answer path: it
// starts one turn, asks one permission question, rejects every
// permission.reply, and ends the turn when the shell cancels it. It
// reports that cancel on the returned channel.
func permissionCore(t *testing.T) (*rpc.Client, <-chan struct{}) {
	t.Helper()
	client, server := net.Pipe()
	cancelled := make(chan struct{}, 1)
	go func() {
		defer func() { _ = server.Close() }()
		dec := json.NewDecoder(server)
		enc := json.NewEncoder(server)
		for {
			var req struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
			}
			if err := dec.Decode(&req); err != nil {
				return
			}
			switch req.Method {
			case "hello":
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{"protocol_version": rpc.ProtocolVersion, "server": "test"},
				})
			case "turn.start":
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{"turn_id": "turn-1"},
				})
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "method": "event",
					"params": map[string]any{
						"turn_id": "turn-1", "session_id": "s1",
						"event": map[string]any{
							"kind": agentapi.EventPermission,
							"permission": map[string]any{
								"call_id": "call-1", "tool": "bash",
								"class": agentapi.ClassExecute, "summary": "run the tests",
							},
						},
					},
				})
			case "permission.reply":
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"error": map[string]any{"code": rpc.CodeNotFound, "message": "no permission question is waiting"},
				})
			case "turn.cancel":
				select {
				case cancelled <- struct{}{}:
				default:
				}
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "id": req.ID,
					"result": map[string]any{"ok": true},
				})
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0", "method": "turn.done",
					"params": map[string]any{"turn_id": "turn-1", "session_id": "s1", "finish_reason": agentapi.FinishCanceled},
				})
			}
		}
	}()
	cl, err := rpc.Dial(context.Background(), client, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close() })
	return cl, cancelled
}

func TestRunTurnPermissionPromptFailureCancelsTurn(t *testing.T) {
	cause := errors.New("tty gone")
	cl, cancelled := permissionCore(t)
	sink := testSink{permission: func(context.Context, agentapi.PermissionRequest) (permission.Decision, error) {
		return permission.Deny, cause
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := RunTurn(ctx, cl, "s1", "run the tests", sink)
	if !errors.Is(err, cause) {
		t.Fatalf("RunTurn = %v; want the prompt failure preserved", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("RunTurn only returned after the context ended: %v", ctx.Err())
	}
	if res.Finish != agentapi.FinishError || !errors.Is(res.Err, cause) {
		t.Fatalf("result = %+v; want FinishError carrying the prompt failure", res)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("RunTurn did not cancel the turn after the prompt failed")
	}
}

func TestRunTurnFailedPermissionReplyCancelsTurn(t *testing.T) {
	cl, cancelled := permissionCore(t)
	sink := testSink{permission: func(context.Context, agentapi.PermissionRequest) (permission.Decision, error) {
		return permission.Allow, nil
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := RunTurn(ctx, cl, "s1", "run the tests", sink)
	if err == nil || !strings.Contains(err.Error(), "sending the permission answer") {
		t.Fatalf("RunTurn = %v; want the failed permission.reply", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("RunTurn only returned after the context ended: %v", ctx.Err())
	}
	var rpcErr *rpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Fatalf("RunTurn = %v; want the core's reply error in the chain", err)
	}
	if res.Finish != agentapi.FinishError || !errors.Is(res.Err, rpcErr) {
		t.Fatalf("result = %+v; want FinishError carrying the reply error", res)
	}
	select {
	case <-cancelled:
	default:
		t.Fatal("RunTurn did not cancel the turn after the answer failed to reach the core")
	}
}
