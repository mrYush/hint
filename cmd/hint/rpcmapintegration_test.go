package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/repomap"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

type engineMapExtractor struct{}

func (engineMapExtractor) Extract(ctx context.Context, source extract.Source, limits extract.Limits) (extract.Result, error) {
	return extract.Extract(ctx, source, limits)
}
func (engineMapExtractor) Close() error { return nil }

func TestMapToolThroughEngineAndProvider(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "located" }, call: func(n int) *agentapi.ToolCall {
		if n == 1 {
			return &agentapi.ToolCall{ID: "map", Name: "repo_map", Arguments: json.RawMessage(`{"path":"source.go"}`)}
		}
		return nil
	}}
	as, _, _, _ := testAssembly(t, chat, "")
	as.cfg.Providers[0].ContextWindow = 32768
	as.mapPath = filepath.Join(t.TempDir(), "code-v2.sqlite")
	as.mapRunner = engineMapExtractor{}
	if err := os.WriteFile(filepath.Join(as.cwd, "source.go"), []byte("package source\nfunc FindTarget() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	eng, client := testEngine(t, as)
	t.Cleanup(eng.closeMCP)
	view, err := client.OpenSession(t.Context(), rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Repos(t.Context(), rpc.RepoRequest{SessionID: view.ID, Action: "index", Mode: "on"}); err != nil {
		t.Fatal(err)
	}
	drive(t, client, view.ID, "Find the declaration in source.go")
	requests := chat.seen()
	if len(requests) != 2 {
		t.Fatalf("provider calls=%d", len(requests))
	}
	found := false
	for _, message := range requests[1].Messages {
		if message.Role != agentapi.RoleTool {
			continue
		}
		var result repomap.Response
		if err := json.Unmarshal([]byte(message.Text()), &result); err != nil {
			t.Fatalf("map lost JSON through dispatcher: %v: %s", err, message.Text())
		}
		for _, declaration := range result.Entries {
			found = found || declaration.Name == "FindTarget"
		}
	}
	if !found {
		t.Fatal("map declaration did not reach provider through ordinary tool history")
	}
}

func TestIndexModeChangesCatalogBetweenTurns(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "ready" }}
	as, _, _, _ := testAssembly(t, chat, "")
	as.cfg.Providers[0].ContextWindow = 32768
	as.mapPath = filepath.Join(t.TempDir(), "code-v2.sqlite")
	as.mapRunner = engineMapExtractor{}
	eng, client := testEngine(t, as)
	t.Cleanup(eng.closeMCP)
	view, err := client.OpenSession(t.Context(), rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"auto", "on", "off"} {
		if _, err := eng.Repos(t.Context(), rpc.RepoRequest{SessionID: view.ID, Action: "index", Mode: mode}); err != nil {
			t.Fatal(err)
		}
		drive(t, client, view.ID, "Report readiness")
		requests := chat.seen()
		present := false
		for _, schema := range requests[len(requests)-1].Tools {
			present = present || schema.Name == "repo_map"
		}
		if present != (mode == "on") {
			t.Fatalf("mode=%s repo_map present=%t", mode, present)
		}
	}
	eng.indexModeMu.RLock()
	_, err = eng.Repos(t.Context(), rpc.RepoRequest{SessionID: view.ID, Action: "index", Mode: "on"})
	eng.indexModeMu.RUnlock()
	if !errors.Is(err, rpc.ErrBusy) {
		t.Fatalf("mode changed during a held turn lease: %v", err)
	}
}

func TestExtractionCommandRegisteredWithoutConfiguration(t *testing.T) {
	request, err := json.Marshal(struct {
		Source extract.Source `json:"source"`
		Limits extract.Limits `json:"limits"`
	}{Source: extract.Source{Path: "source.go", Content: []byte("package source\nfunc WorkerEntry() {}\n")}, Limits: extract.DefaultLimits()})
	if err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	command := newRootCommand()
	command.SetArgs([]string{"internal-extract"})
	command.SetIn(bytes.NewReader(append(request, '\n')))
	command.SetOut(&out)
	command.SetErr(&stderr)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(bytes.TrimSpace(out.Bytes())) || !strings.Contains(out.String(), "WorkerEntry") || stderr.Len() != 0 {
		t.Fatalf("worker protocol: out=%s err=%s", &out, &stderr)
	}
}
