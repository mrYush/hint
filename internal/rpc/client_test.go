package rpc

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// dialFake serves a fakeEngine on one end of a pipe and dials the other,
// the way the in-process CLI does.
func dialFake(t *testing.T, srv *Server) (*Client, *fakeEngine) {
	t.Helper()
	engine := &fakeEngine{sessions: map[string]*fakeSession{}}
	srv.Engine = engine
	ctx, cancel := context.WithCancel(context.Background())
	client, peer := net.Pipe()
	go func() { _ = srv.ServeConn(ctx, peer) }()
	c, err := Dial(ctx, client, srv.Token)
	if err != nil {
		cancel()
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close(); cancel() })
	return c, engine
}

// TestClient_TurnWithPermission is the client-side mirror of
// TestHelloThenTurnWithPermission: a Prompter answers the permission event
// through Turn.Reply, and the engine observes the allow.
func TestClient_TurnWithPermission(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Second, Version: "test"})
	ctx := context.Background()
	if c.ServerVersion() != "test" {
		t.Errorf("server version = %q", c.ServerVersion())
	}

	view, err := c.OpenSession(ctx, OpenOptions{Ephemeral: true})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if view.ID != "sess-1" || !engine.sessions[view.ID].ephemeral || view.Created.IsZero() || view.Messages == nil {
		t.Fatalf("session view = %+v (ephemeral %v)", view, engine.sessions[view.ID].ephemeral)
	}

	turn, err := c.StartTurn(ctx, view.ID, "run the tests")
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	if turn.ID == "" {
		t.Fatal("turn has no id")
	}
	var kinds []agentapi.EventKind
	for ev := range turn.Events() {
		kinds = append(kinds, ev.Kind)
		if ev.Kind == agentapi.EventPermission {
			if err := turn.Reply(ctx, ev.Permission.CallID, permission.Allow); err != nil {
				t.Fatalf("Reply: %v", err)
			}
		}
	}
	if turn.Err() != nil || turn.FinishReason() != agentapi.FinishStop {
		t.Fatalf("turn ended with %v, %v", turn.FinishReason(), turn.Err())
	}
	want := []agentapi.EventKind{agentapi.EventTurnStart, agentapi.EventPermission, agentapi.EventTurnEnd}
	if len(kinds) != len(want) {
		t.Fatalf("events = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events = %v, want %v", kinds, want)
		}
	}
	if !engine.sessions[view.ID].allowed {
		t.Fatal("engine did not observe the allow")
	}
}

func TestClient_CancelEndsTurn(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Minute})
	ctx := context.Background()
	view, err := c.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, view.ID, "slow")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range turn.Events() {
		if ev.Kind == agentapi.EventPermission {
			if err := turn.Cancel(ctx); err != nil {
				t.Fatalf("Cancel: %v", err)
			}
		}
	}
	if turn.FinishReason() != agentapi.FinishCanceled {
		t.Fatalf("finish = %q, want canceled", turn.FinishReason())
	}
	if engine.sessions[view.ID].allowed {
		t.Fatal("a cancelled permission was allowed")
	}
}

func TestClient_ErrorsCarryTheCode(t *testing.T) {
	c, _ := dialFake(t, &Server{})
	_, err := c.AttachSession(context.Background(), "nope")
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Fatalf("AttachSession(unknown) = %v, want *Error with CodeNotFound", err)
	}
	// A second turn on a busy session is refused with its own code.
	view, err := c.OpenSession(context.Background(), OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(context.Background(), view.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range turn.Events() {
		if ev.Kind != agentapi.EventPermission {
			continue
		}
		if _, err := c.StartTurn(context.Background(), view.ID, "second"); !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
			t.Errorf("second StartTurn = %v, want CodeBusy", err)
		}
		_ = turn.Cancel(context.Background())
	}
}

func TestClient_WrongTokenIsRefused(t *testing.T) {
	srv := &Server{Engine: &fakeEngine{sessions: map[string]*fakeSession{}}, Token: "secret"}
	client, peer := net.Pipe()
	go func() { _ = srv.ServeConn(context.Background(), peer) }()
	_, err := Dial(context.Background(), client, "wrong")
	var rpcErr *Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != CodeUnauthorized {
		t.Fatalf("Dial with a wrong token = %v", err)
	}
}

func TestClient_CloseFailsOpenTurn(t *testing.T) {
	c, _ := dialFake(t, &Server{PermissionWait: time.Minute})
	view, err := c.OpenSession(context.Background(), OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(context.Background(), view.ID, "hang")
	if err != nil {
		t.Fatal(err)
	}
	for ev := range turn.Events() {
		if ev.Kind == agentapi.EventPermission {
			_ = c.Close()
		}
	}
	if !errors.Is(turn.Err(), ErrClosed) {
		t.Fatalf("turn.Err() = %v, want ErrClosed", turn.Err())
	}
	if _, err := c.ListSessions(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after Close = %v, want ErrClosed", err)
	}
}

// TestQueue pins the one property the reader relies on: push never
// blocks, however far behind the consumer is, and order survives.
func TestQueue(t *testing.T) {
	q := newQueue[int]()
	const n = 10_000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			q.push(i)
		}
		q.close()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("push blocked on an unread queue")
	}
	i := 0
	for v := range q.out {
		if v != i {
			t.Fatalf("item %d = %d", i, v)
		}
		i++
	}
	if i != n {
		t.Fatalf("drained %d items, want %d", i, n)
	}
}
