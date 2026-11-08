package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestRunTurnReleasesWorkspaceBeforeChannelCloses(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "ok" }}, "")
	eng, err := newEngine(t.Context(), as)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.closeMCP)
	live := eng.newSession(nil, "test")
	for _, prompt := range []string{"first", "second"} {
		events, err := live.RunTurn(t.Context(), prompt, nil)
		if err != nil {
			t.Fatalf("%s RunTurn: %v", prompt, err)
		}
		for ev := range events {
			if ev.Kind == agentapi.EventError {
				t.Errorf("%s turn: %+v", prompt, ev.Err)
			}
		}
		// A drained event stream must permit both mode changes and a new turn.
		if !eng.indexModeMu.TryLock() {
			t.Fatal("closed event stream still holds the index mode lock")
		}
		eng.indexModeMu.Unlock()
		release, err := live.workspace.BeginTurn()
		if err != nil {
			t.Fatalf("closed event stream still holds the workspace: %v", err)
		}
		release()
	}
}

func TestReposAddRemovePersistenceAndBusy(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{}, "")
	eng, cl := testEngine(t, as)
	ctx := context.Background()
	view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	added := t.TempDir()
	result, err := cl.Repos(ctx, rpc.RepoRequest{SessionID: view.ID, Action: "add", Path: added, Alias: "library"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Roots) != 2 || !strings.Contains(result.Text, "library") {
		t.Fatalf("added: %+v", result)
	}
	live := eng.live[view.ID]
	if got := live.file.Workspace(); len(got.Roots) != 2 || got.Version != 2 {
		t.Fatalf("membership not recorded: %+v", got)
	}
	release, err := live.workspace.BeginTurn()
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Repos(ctx, rpc.RepoRequest{SessionID: view.ID, Action: "remove", Repo: "library"})
	if !errors.Is(err, rpc.ErrBusy) {
		t.Fatalf("busy mutation: %v", err)
	}
	release()
	if _, err := cl.Repos(ctx, rpc.RepoRequest{SessionID: view.ID, Action: "remove", Repo: "library"}); err != nil {
		t.Fatal(err)
	}
	if _, err := live.workspace.Resolve("library", "."); !errors.Is(err, workspace.ErrUnknownRoot) {
		t.Fatalf("root still attached: %v", err)
	}
	if len(live.file.Workspace().Roots) != 1 {
		t.Fatal("removal not recorded")
	}
}

func TestReposListingHasBoundedProgressAndMembershipBinding(t *testing.T) {
	s, _ := repoTestSession(t)
	parent := t.TempDir()
	for i := 0; i < 24; i++ {
		path := filepath.Join(parent, strings.Repeat("long", 40), strings.Repeat("path", 40), fmt.Sprint(i))
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
		alias := fmt.Sprintf("root%d", i) + strings.Repeat("\x01", 100) + "x"
		if _, err := s.workspace.AttachLocal(path, alias); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	firstCursor := ""
	for pages := 0; pages < 30; pages++ {
		result, err := s.repoListing(cursor)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > 8192 || len(result.Roots) == 0 {
			t.Fatalf("unbounded/nonprogress page: %d %v", len(encoded), err)
		}
		for _, root := range result.Roots {
			if seen[root.ID] {
				t.Fatal("duplicate root")
			}
			seen[root.ID] = true
			if len(root.Path) > 256 {
				t.Fatal("unbounded path metadata")
			}
		}
		if firstCursor == "" {
			firstCursor = result.NextCursor
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 26 || cursor != "" {
		t.Fatalf("lost roots: %d cursor=%q", len(seen), cursor)
	}
	if _, err := s.workspace.AttachLocal(t.TempDir(), "new-root"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.repoListing(firstCursor); err == nil {
		t.Fatal("changed membership accepted old cursor")
	}
}

func TestReposIndexModeRequiresIdleEngine(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{}, "")
	as.mapPath = filepath.Join(t.TempDir(), "catalog.sqlite")
	as.mapRunner = engineMapExtractor{}
	eng, cl := testEngine(t, as)
	t.Cleanup(eng.closeMCP)
	view, err := cl.OpenSession(context.Background(), rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	eng.indexModeMu.RLock()
	_, err = eng.Repos(context.Background(), rpc.RepoRequest{SessionID: view.ID, Action: "index", Mode: "on"})
	if !errors.Is(err, rpc.ErrBusy) {
		eng.indexModeMu.RUnlock()
		t.Fatalf("active turn mode change: %v", err)
	}
	_, err = eng.Repos(context.Background(), rpc.RepoRequest{SessionID: view.ID, Action: "index"})
	eng.indexModeMu.RUnlock()
	if err != nil {
		t.Fatalf("read status blocked by active turn: %v", err)
	}
	if _, err := eng.Repos(context.Background(), rpc.RepoRequest{SessionID: view.ID, Action: "index", Mode: "off"}); err != nil {
		t.Fatal(err)
	}
}

func TestREPLReposCommand(t *testing.T) {
	as, sio, _, stderr := testAssembly(t, &fakeChat{}, "/repos\n/exit\n")
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.repl(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "Workspace revision") {
		t.Fatalf("/repos missing output: %s", stderr.String())
	}
}
