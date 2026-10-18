package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// runFakeTurn drives one turn of the fake engine to its end, allowing
// the permission question it asks.
func runFakeTurn(t *testing.T, c *Client, sessionID string) {
	t.Helper()
	ctx := context.Background()
	turn, err := c.StartTurn(ctx, sessionID, "go")
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	for ev := range turn.Events() {
		if ev.Kind == agentapi.EventPermission {
			if err := turn.Reply(ctx, ev.Permission.CallID, permission.Allow); err != nil {
				t.Fatalf("Reply: %v", err)
			}
		}
	}
}

func TestClient_ProfileSwitchAndList(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Second})
	engine.profiles = []ProfileInfo{{Name: "local", Kind: "ollama", Model: "qwen", Default: true, Usable: true}, {Name: "cloud", Kind: "openai", Problem: "needs an API key"}}
	ctx := context.Background()

	infos, err := c.Profiles(ctx)
	if err != nil || len(infos) != 2 || infos[1].Problem == "" {
		t.Fatalf("Profiles = %+v, %v", infos, err)
	}

	view, err := c.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Profile != "" || view.Parent != nil {
		t.Fatalf("a fresh session view = %+v; want no profile and no parent", view)
	}
	ref, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "local"})
	if err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	if ref != (ProfileRef{Profile: "local", Model: "qwen"}) {
		t.Errorf("effective profile = %+v; want the model filled in", ref)
	}
	again, err := c.AttachSession(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Profile != "local" || again.Model != "qwen" {
		t.Errorf("attach after the switch = %+v; want it to carry the profile", again)
	}

	var rpcErr *Error
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "nope"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Errorf("unknown profile = %v; want code %d", err, CodeNotFound)
	}
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "cloud"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Errorf("unusable profile = %v; want code %d", err, CodeInvalidParams)
	}
	if _, err := c.SetProfile(ctx, "ghost", ProfileRef{Profile: "local"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Errorf("unknown session = %v; want code %d", err, CodeNotFound)
	}
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Errorf("empty profile = %v; want code %d", err, CodeInvalidParams)
	}
}

func TestClient_ProfileSwitchWaitsForTheTurn(t *testing.T) {
	c, _ := dialFake(t, &Server{PermissionWait: time.Second})
	ctx := context.Background()
	view, err := c.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := c.StartTurn(ctx, view.ID, "go")
	if err != nil {
		t.Fatal(err)
	}
	// Inside the turn: the fake is waiting on its permission question.
	ev := <-turn.Events()
	for ev.Kind != agentapi.EventPermission {
		ev = <-turn.Events()
	}
	var rpcErr *Error
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "local"}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
		t.Errorf("switch during a turn = %v; want code %d", err, CodeBusy)
	}
	if _, err := c.ForkSession(ctx, ForkOptions{ID: view.ID, Turn: 1}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeBusy {
		t.Errorf("fork during a turn = %v; want code %d", err, CodeBusy)
	}
	if err := turn.Reply(ctx, ev.Permission.CallID, permission.Deny); err != nil {
		t.Fatal(err)
	}
	for range turn.Events() {
	}
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "local"}); err != nil {
		t.Errorf("switch after turn.done = %v; want it accepted", err)
	}
}

func TestClient_Fork(t *testing.T) {
	c, engine := dialFake(t, &Server{PermissionWait: time.Second})
	ctx := context.Background()
	view, err := c.OpenSession(ctx, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetProfile(ctx, view.ID, ProfileRef{Profile: "local"}); err != nil {
		t.Fatal(err)
	}
	runFakeTurn(t, c, view.ID)
	runFakeTurn(t, c, view.ID)

	fork, err := c.ForkSession(ctx, ForkOptions{ID: view.ID, Turn: 1})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if fork.ID == view.ID || fork.Parent == nil || *fork.Parent != (Parent{ID: view.ID, Turn: 1}) {
		t.Fatalf("fork view = %+v; want a new id and parent %s@1", fork, view.ID)
	}
	if fork.Profile != "local" {
		t.Errorf("fork profile = %q; want the source's", fork.Profile)
	}
	if engine.sessions[fork.ID] == nil {
		t.Fatal("the fork is not an engine session")
	}
	// The fork is held: a turn can start on it right away.
	runFakeTurn(t, c, fork.ID)

	var rpcErr *Error
	if _, err := c.ForkSession(ctx, ForkOptions{ID: view.ID, Turn: 3}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Errorf("turn past the end = %v; want code %d", err, CodeInvalidParams)
	}
	// Turn 0 is the whole conversation; -1 is not a turn.
	if whole, err := c.ForkSession(ctx, ForkOptions{ID: view.ID}); err != nil || whole.Parent.Turn != 2 {
		t.Errorf("fork without a turn = %+v, %v; want a cut after turn 2", whole.Parent, err)
	}
	if _, err := c.ForkSession(ctx, ForkOptions{ID: view.ID, Turn: -1}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeInvalidParams {
		t.Errorf("turn -1 = %v; want code %d", err, CodeInvalidParams)
	}
	if _, err := c.ForkSession(ctx, ForkOptions{ID: "ghost", Turn: 1}); !errors.As(err, &rpcErr) || rpcErr.Code != CodeNotFound {
		t.Errorf("unknown source = %v; want code %d", err, CodeNotFound)
	}
}
