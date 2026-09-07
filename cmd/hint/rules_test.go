package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestPreamble_LoadsRuleOnTouch(t *testing.T) {
	dir := t.TempDir()
	for rel, content := range map[string]string{
		"HINT.md":           "Root rules.\n",
		".hint/rules/go.md": "---\ntitle: Go style\npaths: ['*.go']\n---\nUse gofmt.\n",
		"main.go":           "package main\n",
	} {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pc, err := project.Load(context.Background(), dir, project.WithGit(""), project.WithOverview(project.None{}))
	if err != nil {
		t.Fatal(err)
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := toolRegistry(root, pc.InstructionPaths)
	if err != nil {
		t.Fatal(err)
	}
	var notices bytes.Buffer
	pre := newPreamble(pc, registry, &notices, nil)
	rulePath := filepath.Join(dir, ".hint", "rules", "go.md")

	// Before any touch: the rule is listed, not loaded.
	initial := pre.initial()[0].Text()
	if !strings.Contains(initial, "<project_rules>") || !strings.Contains(initial, "Go style ("+rulePath+") for *.go") || strings.Contains(initial, `<file path="`+rulePath) {
		t.Fatalf("initial prompt:\n%s", initial)
	}
	history := []agentapi.Message{agentapi.SystemMessage(initial), agentapi.UserMessage("q")}
	prefix, err := pre.Prefix(context.Background(), history)
	if err != nil || len(prefix) != 1 || prefix[0].Text() != initial {
		t.Fatalf("prefix without a touch = %+v, %v; want the initial prompt unchanged", prefix, err)
	}

	// A read of a Go file — plus a call to a tool the registry does not
	// know, which is ignored — loads the rule with its paths and drops
	// it from the index.
	history = append(history,
		agentapi.Message{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{
			{ID: "c1", Name: "ghost", Arguments: json.RawMessage(`{"path":"main.go"}`)},
			{ID: "c2", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)},
		}},
		agentapi.TextResult("c2", "read_file", "package main").Message(),
	)
	prefix, err = pre.Prefix(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	got := prefix[0].Text()
	if !strings.Contains(got, `<file path="`+rulePath+`" paths="*.go">`+"\nUse gofmt.") || strings.Contains(got, "<project_rules>") {
		t.Fatalf("prompt after the touch:\n%s", got)
	}
	if !strings.Contains(notices.String(), `rule "Go style" loaded from `+rulePath) {
		t.Errorf("notices = %q", notices.String())
	}
	// The same history again rebuilds nothing and says nothing more.
	notices.Reset()
	again, _ := pre.Prefix(context.Background(), history)
	if again[0].Text() != got || notices.Len() != 0 {
		t.Errorf("a repeated prefix changed the prompt or spoke again: %q", notices.String())
	}
}
