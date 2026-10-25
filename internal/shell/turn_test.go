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

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

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
