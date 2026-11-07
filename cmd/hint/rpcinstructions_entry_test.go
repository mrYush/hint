package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap"
	"github.com/mrYush/hint/internal/workspace"
)

func TestRepoPrepareEntryIDPreservesExactTarget(t *testing.T) {
	for _, name := range []string{" leading space.txt", "raw\xff.txt"} {
		t.Run(name, func(t *testing.T) {
			s, dir := repoTestSession(t)
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
				if strings.Contains(name, "\xff") && (runtime.GOOS == "darwin" || runtime.GOOS == "windows") {
					t.Skipf("host filesystem rejects invalid UTF-8 filenames: %v", err)
				}
				t.Fatal(err)
			}
			_, info, err := s.workspace.ToolRoot("library")
			if err != nil {
				t.Fatal(err)
			}
			id := workspace.EntryID(info.ID, name)
			catalogPath := filepath.Join(t.TempDir(), "catalog.sqlite")
			catalog, err := index.Open(catalogPath, index.Options{})
			if err != nil {
				t.Fatal(err)
			}
			_, putErr := catalog.PutSource(t.Context(), index.File{RootID: info.ID, ID: id,
				Path: []byte(name), Kind: index.KindFile, State: index.StatePending}, nil)
			closeErr := catalog.Close()
			if putErr != nil || closeErr != nil {
				t.Fatalf("seed catalog: put=%v close=%v", putErr, closeErr)
			}
			s.eng.mapIndex, err = repomap.New(context.Background(), repomap.Options{Path: catalogPath, Mode: repomap.ModeOff})
			if err != nil {
				t.Fatal(err)
			}
			args, err := json.Marshal(map[string]string{"repo": "library", "entry_id": id})
			if err != nil {
				t.Fatal(err)
			}
			if result := repoRun(t, s, "repo_prepare", string(args)); result.IsError {
				t.Fatalf("prepare by entry: %s", result.Text())
			}
			s.repoInstructions.mu.Lock()
			target := s.repoInstructions.prepared.target
			s.repoInstructions.mu.Unlock()
			if target != name {
				t.Fatalf("target changed bytes: got %q want %q", target, name)
			}
			args, err = json.Marshal(map[string]string{"repo": "library", "entry_id": id, "content": "after"})
			if err != nil {
				t.Fatal(err)
			}
			if result := repoRun(t, s, "write_file", string(args)); !result.IsError {
				t.Fatal("entry handle bypassed next-request delivery")
			}
			repoPrefix(t, s)
			if result := repoRun(t, s, "write_file", string(args)); result.IsError {
				t.Fatalf("write by entry: %s", result.Text())
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "after" {
				t.Fatalf("exact target not updated: %q %v", data, err)
			}
		})
	}
}

func TestRepoRoutingRejectsNullAndAmbiguousFields(t *testing.T) {
	s, _ := repoTestSession(t)
	for _, tc := range []struct{ name, args, reason string }{
		{"null repo", `{"repo":null,"path":"new.txt","content":"x"}`, "repo must be a string"},
		{"null path", `{"path":null,"content":"x"}`, "path must be a string"},
		{"null entry", `{"entry_id":null,"content":"x"}`, "entry_id must be a string"},
		{"empty entry", `{"entry_id":"","content":"x"}`, "entry_id requires a file tool"},
		{"entry and empty path", `{"entry_id":"e_test","path":"","content":"x"}`, "cannot be combined with path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := repoRun(t, s, "write_file", tc.args)
			if !result.IsError || !strings.Contains(result.Text(), tc.reason) {
				t.Fatalf("unexpected validation: %s", result.Text())
			}
		})
	}
}

func TestRepoPrepareRejectsNullAndAmbiguousFields(t *testing.T) {
	s, _ := repoTestSession(t)
	for _, tc := range []struct{ name, args, reason string }{
		{"null repo", `{"repo":null,"path":"new.txt"}`, "repo must be a string"},
		{"null path", `{"repo":"library","path":null}`, "path must be a string"},
		{"null entry", `{"repo":"library","entry_id":null}`, "entry_id must be a string"},
		{"empty entry", `{"repo":"library","entry_id":""}`, "entry_id cannot be empty"},
		{"entry and empty path", `{"repo":"library","entry_id":"e_test","path":""}`, "mutually exclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := repoRun(t, s, "repo_prepare", tc.args)
			if !result.IsError || !strings.Contains(result.Text(), tc.reason) {
				t.Fatalf("unexpected validation: %s", result.Text())
			}
		})
	}
}
