package session

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestWorkspaceHistoryAcrossResumeCompactionAndFork(t *testing.T) {
	store := NewStore(t.TempDir())
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	primary := WorkspaceRoot{ID: "primary", Alias: "primary", Path: t.TempDir(), Primary: true}
	other := WorkspaceRoot{ID: "other", Alias: "other", Path: t.TempDir()}
	appendRoots := func(rev uint64, roots ...WorkspaceRoot) {
		t.Helper()
		if err := sess.AppendWorkspace(WorkspaceRef{Version: 1, Revision: rev, AfterTurn: sess.UserTurns(), Roots: roots}); err != nil {
			t.Fatal(err)
		}
	}
	turn := func(text string) {
		t.Helper()
		if err := sess.AppendMessage(agentapi.UserMessage(text)); err != nil {
			t.Fatal(err)
		}
	}
	appendRoots(1, primary)
	turn("one")
	appendRoots(2, primary, other)
	turn("two")
	appendRoots(3, primary)
	turn("three")
	for _, tc := range []struct{ turn, roots int }{{1, 1}, {2, 2}, {3, 1}} {
		if got := sess.WorkspaceAtTurn(tc.turn); len(got.Roots) != tc.roots {
			t.Fatalf("turn %d: %+v", tc.turn, got)
		}
	}
	if err := sess.AppendCompaction(agentapi.Compaction{History: []agentapi.Message{agentapi.UserMessage("two"), agentapi.UserMessage("three")}}); err != nil {
		t.Fatal(err)
	}
	path := sess.Path()
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	loaded, err := load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UserTurns() != 3 {
		t.Fatalf("lost monotonic turns: %d", loaded.UserTurns())
	}
	if got := loaded.WorkspaceAtTurn(1); len(got.Roots) != 2 {
		t.Fatalf("compacted turn authority: %+v", got)
	}
	copied := loaded.WorkspaceHistoryAtTurn(2)
	// A second fork at the first retained turn must still see that turn's root,
	// rather than the last snapshot that removed it afterwards.
	earlier := WorkspaceHistoryForTurn(copied, 2, 2, 1)
	if len(earlier) != 1 || len(earlier[0].Roots) != 2 {
		t.Fatalf("nested fork history: %+v", earlier)
	}
	copied[0].Roots[0].Path = "changed"
	if loaded.WorkspaceHistoryAtTurn(2)[0].Roots[0].Path == "changed" {
		t.Fatal("history aliases caller memory")
	}
}

func TestWorkspaceRootRawPathAndReservations(t *testing.T) {
	root := WorkspaceRoot{ID: "root", Alias: "primary", Path: "/raw/" + string([]byte{0xff}), Primary: true}
	data, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "path_bytes") {
		t.Fatalf("raw bytes not encoded: %s", data)
	}
	var decoded WorkspaceRoot
	if err := json.Unmarshal(data, &decoded); err != nil || decoded != root {
		t.Fatalf("round trip: %+v %v", decoded, err)
	}
	if err := json.Unmarshal([]byte(`{"id":"r","alias":"x","path":"/one","path_bytes":"L3R3bw=="}`), &decoded); err == nil {
		t.Fatal("ambiguous path accepted")
	}
	base := WorkspaceRef{Version: 2, Revision: 1, Roots: []WorkspaceRoot{root}, Reservations: []WorkspaceRoot{root}}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []WorkspaceRoot{{ID: "else", Alias: root.Alias, Path: root.Path}, {ID: root.ID, Alias: "other", Path: "/different", Primary: true}, {ID: "other", Alias: "other", Path: "/other", Primary: true}, {ID: "other", Alias: "bad/alias", Path: "/other"}} {
		ref := base
		ref.Reservations = append(append([]WorkspaceRoot(nil), base.Reservations...), bad)
		if err := ref.validate(); err == nil {
			t.Fatalf("conflicting reservation accepted: %+v", bad)
		}
	}
}

func TestWorkspaceRecordValidationAndWriteFailure(t *testing.T) {
	sess, err := NewStore(t.TempDir()).Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ref := WorkspaceRef{Version: 1, Revision: 1, Roots: []WorkspaceRoot{{ID: "r", Alias: "primary", Path: "/root", Primary: true}}}
	if err := sess.AppendWorkspace(ref); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	ref.Revision = 2
	if err := sess.AppendWorkspace(ref); err == nil {
		t.Fatal("closed file accepted membership")
	}
	if sess.Workspace().Revision != 1 {
		t.Fatal("failed write changed effective history")
	}
	if err := (&Record{Kind: KindWorkspace}).Validate(); err == nil {
		t.Fatal("nil snapshot accepted")
	}
	for _, bad := range []WorkspaceRef{{Version: 3, Revision: 1, Roots: ref.Roots}, {Version: 1, Revision: 0, Roots: ref.Roots}, {Version: 1, Revision: 1, Roots: append(ref.Roots, ref.Roots...)}} {
		if err := bad.validate(); err == nil {
			t.Fatalf("invalid snapshot: %+v", bad)
		}
	}
	if errors.Is((&Record{Kind: KindWorkspace, Workspace: &ref}).Validate(), ErrUnknownKind) {
		t.Fatal("workspace kind is unknown")
	}
}
