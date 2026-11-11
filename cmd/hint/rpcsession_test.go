package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
)

// TestSecondRunCannotAttachTheSessionBeingRecorded: two runs continuing
// one session would interleave their records. The second gets CodeBusy
// while the first records, and succeeds once that run exits.
func TestSecondRunCannotAttachTheSessionBeingRecorded(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "ok" }}, "")
	_, first, stopFirst := testEngineServer(t, as)
	defer stopFirst()
	ctx := context.Background()
	view, err := first.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}

	_, second := testEngine(t, as)
	if _, err := second.AttachSession(ctx, view.ID); err == nil {
		t.Fatal("the second run attached a session the first one records")
	} else {
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeBusy {
			t.Fatalf("attach while recorded = %v, want CodeBusy", err)
		}
		if !strings.Contains(err.Error(), "another hint process") {
			t.Errorf("attach error does not say why: %v", err)
		}
	}

	stopFirst()
	attached, err := second.AttachSession(ctx, view.ID)
	if err != nil {
		t.Fatalf("attach after the first run exited: %v", err)
	}
	if attached.ID != view.ID {
		t.Errorf("attached %s, want %s", attached.ID, view.ID)
	}
}

// TestAttachPrefixDoesNotReachAnotherConversation: when a recorded
// session is held by another run, a prefix must answer busy even if a
// live ephemeral session shares its first characters. A prefix must
// never reach a different conversation than the one the store resolved.
func TestAttachPrefixDoesNotReachAnotherConversation(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "ok" }}, "")
	_, first, stopFirst := testEngineServer(t, as)
	defer stopFirst()
	ctx := context.Background()
	recorded, err := first.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}

	eng, second, stopSecond := testEngineServer(t, as)
	defer stopSecond()
	prefix := recorded.ID[:8]
	ephemeral := prefix + "00000000"
	if ephemeral == recorded.ID {
		ephemeral = prefix + "ffffffff"
	}
	eng.adopt(nil, ephemeral)

	if view, err := second.AttachSession(ctx, prefix); err == nil {
		t.Fatalf("a prefix of the held session attached %q", view.ID)
	} else {
		var rpcErr *rpc.Error
		if !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeBusy {
			t.Fatalf("attach by prefix while held = %v, want CodeBusy", err)
		}
	}

	// Once the recording run exits, the prefix resolves to the recorded
	// session, not to the ephemeral one that shares it.
	stopFirst()
	attached, err := second.AttachSession(ctx, prefix)
	if err != nil {
		t.Fatalf("attach after the first run exited: %v", err)
	}
	if attached.ID != recorded.ID {
		t.Errorf("attached %s, want the recorded %s", attached.ID, recorded.ID)
	}
}
