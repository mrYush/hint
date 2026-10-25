package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestClient_CompleteAnswersAndCanBeCancelled(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Second})
	ctx := context.Background()

	settings, err := c.CompletionSettings(ctx)
	if err != nil || settings.Mode != "tab" || settings.IdleAfterMs != 600 {
		t.Fatalf("CompletionSettings = %+v, %v", settings, err)
	}
	res, err := c.Complete(ctx, CompleteRequest{ID: "c1", Text: "add a test for"})
	if err != nil || res.ID != "c1" || res.Suggestion != " done" || res.Usage == nil {
		t.Fatalf("Complete = %+v, %v", res, err)
	}

	// A slow completion is cancelled by complete.cancel: the call then
	// fails and the engine saw the cancel.
	engine.setCompletion(5*time.Second, false)
	done := make(chan error, 1)
	go func() {
		_, err := c.Complete(ctx, CompleteRequest{ID: "c2", Text: "slow"})
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err := c.CancelComplete(ctx, "c2"); err != nil {
		t.Fatalf("CancelComplete: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a cancelled completion answered")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled completion did not return")
	}
	if _, cancelled := engine.completionCounts(); cancelled != 1 {
		t.Errorf("engine saw %d cancels, want 1", cancelled)
	}
	// Cancelling what already answered is fine.
	if err := c.CancelComplete(ctx, "c1"); err != nil {
		t.Errorf("late cancel = %v", err)
	}

	var rpcErr *Error
	if _, err := c.Complete(ctx, CompleteRequest{ID: "", Text: "x"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Errorf("complete without an id = %v; want code %d", err, CodeInvalidParams)
	}
	engine.setCompletion(0, true)
	if res, err := c.Complete(ctx, CompleteRequest{ID: "c3", Text: "x"}); err != nil || !res.Disabled || res.Reason == "" {
		t.Errorf("disabled completion = %+v, %v; want disabled with a reason", res, err)
	}
}

// TestClient_CompleteDoesNotBlockTheTurn pins the reason complete runs
// beside the connection's loop: while a completion waits on its model,
// a permission answer and a cancel for a turn still get through.
func TestClient_CompleteDoesNotBlockTheTurn(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: 5 * time.Second})
	engine.setCompletion(3*time.Second, false)
	ctx := context.Background()
	view, err := c.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, view.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	ev := <-turn.Events()
	for ev.Kind != agentapi.EventPermission {
		ev = <-turn.Events()
	}
	go func() { _, _ = c.Complete(ctx, CompleteRequest{ID: "c1", Text: "while the turn waits"}) }()
	time.Sleep(50 * time.Millisecond)
	replied := make(chan error, 1)
	go func() { replied <- turn.Reply(ctx, ev.Permission.CallID, permission.Allow) }()
	select {
	case err := <-replied:
		if err != nil {
			t.Fatalf("Reply during a completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("permission.reply waited behind the completion")
	}
	for range turn.Events() {
	}
	if turn.FinishReason() != agentapi.FinishStop {
		t.Errorf("turn finished %q", turn.FinishReason())
	}
}

func TestClient_NewerCompleteCancelsTheOlder(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Second})
	engine.setCompletion(3*time.Second, false)
	ctx := context.Background()
	first := make(chan error, 1)
	go func() {
		_, err := c.Complete(ctx, CompleteRequest{ID: "c1", Text: "ab"})
		first <- err
	}()
	time.Sleep(50 * time.Millisecond)
	engine.setCompletion(0, false)
	if res, err := c.Complete(ctx, CompleteRequest{ID: "c2", Text: "abc"}); err != nil || res.ID != "c2" {
		t.Fatalf("second Complete = %+v, %v", res, err)
	}
	select {
	case err := <-first:
		if err == nil {
			t.Error("the superseded completion answered")
		}
	case <-time.After(time.Second):
		t.Fatal("the superseded completion was not cancelled")
	}
}

func TestClient_Models(t *testing.T) {
	c, _ := dialFake(t, &Server{})
	ctx := context.Background()
	models, err := c.Models(ctx, "local")
	if err != nil || len(models) != 2 || models[0] != "qwen" {
		t.Fatalf("Models = %v, %v", models, err)
	}
	var rpcErr *Error
	if _, err := c.Models(ctx, "nope"); !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Errorf("unknown profile = %v; want code %d", err, CodeNotFound)
	}
}
