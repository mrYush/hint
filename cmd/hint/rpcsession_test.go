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
