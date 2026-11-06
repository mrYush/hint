package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

func repoTestSession(t *testing.T) (*rpcSession, string) {
	t.Helper()
	as, _, _, _ := testAssembly(t, &fakeChat{}, "")
	eng, err := newEngine(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.closeMCP)
	live := eng.newSession(nil, "repo-instructions")
	secondary := t.TempDir()
	if _, err := live.workspace.AttachLocal(secondary, "library"); err != nil {
		t.Fatal(err)
	}
	return live, secondary
}

func repoRun(t *testing.T, s *rpcSession, name, args string) agentapi.ToolResult {
	t.Helper()
	for _, tool := range s.sessionTools() {
		if tool.Name() == name {
			result, err := tool.Run(context.Background(), "test", json.RawMessage(args))
			if err != nil {
				t.Fatal(err)
			}
			return result
		}
	}
	t.Fatalf("tool %s absent", name)
	return agentapi.ToolResult{}
}

func repoPrefix(t *testing.T, s *rpcSession) string {
	t.Helper()
	messages, err := s.workspacePreamble().Prefix(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return messages[0].Text()
}

func TestRepoPreparationRequiresDeliveredFreshInstructions(t *testing.T) {
	s, dir := repoTestSession(t)
	rules := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(rules, []byte("# Library\nUse the library style.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write := `{"repo":"library","path":"new.txt","content":"hello"}`
	if result := repoRun(t, s, "write_file", write); !result.IsError {
		t.Fatal("unprepared write succeeded")
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library","path":"new.txt"}`); result.IsError {
		t.Fatalf("prepare: %+v", result)
	}
	if result := repoRun(t, s, "write_file", write); !result.IsError {
		t.Fatal("same-batch write succeeded before a model request")
	}
	if prefix := repoPrefix(t, s); !strings.Contains(prefix, "Use the library style.") {
		t.Fatalf("instruction missing: %s", prefix)
	}
	if result := repoRun(t, s, "write_file", write); result.IsError {
		t.Fatalf("prepared write: %+v", result)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "new.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("write missing: %q %v", data, err)
	}
	if err := os.WriteFile(rules, []byte("# Library\nNew instructions.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "write_file", write); !result.IsError {
		t.Fatal("changed instructions allowed stale write")
	}
	if prefix := repoPrefix(t, s); !strings.Contains(prefix, "New instructions.") {
		t.Fatal("next request did not refresh changed instructions")
	}
	if result := repoRun(t, s, "write_file", write); result.IsError {
		t.Fatalf("refreshed write: %+v", result)
	}
	if result := repoRun(t, s, "write_file", `{"repo":"library","path":"other.txt","content":"x"}`); !result.IsError {
		t.Fatal("preparation granted a different target")
	}
	if err := s.workspace.Detach("library"); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "write_file", write); !result.IsError {
		t.Fatal("detached root kept write authority")
	}
}

func TestRepoPreparationAncestorsRulesAndIsolation(t *testing.T) {
	s, dir := repoTestSession(t)
	for path, content := range map[string]string{"AGENTS.md": "ROOT_DIRECTIVE", "src/AGENTS.md": "CHILD_DIRECTIVE", ".hint/rules/go.md": "---\npaths: '**/*.go'\n---\nGO_RULE", ".cursor/rules/js.mdc": "---\nglobs: '**/*.js'\n---\nJS_RULE"} {
		abs := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(abs), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library","path":"src/new.go"}`); result.IsError {
		t.Fatalf("prepare: %+v", result)
	}
	prefix := repoPrefix(t, s)
	for _, want := range []string{"ROOT_DIRECTIVE", "CHILD_DIRECTIVE", "GO_RULE"} {
		if !strings.Contains(prefix, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(prefix, "JS_RULE") {
		t.Fatal("unmatched conditional rule loaded")
	}
	if result := repoRun(t, s, "instructions", `{"repo":"library","path":"src/AGENTS.md"}`); result.IsError || !strings.Contains(result.Text(), "CHILD_DIRECTIVE") {
		t.Fatalf("secondary instructions: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(dir, "src", "HINT.md"), []byte("NEW_PREFERRED"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "write_file", `{"repo":"library","path":"src/new.go","content":"package x"}`); !result.IsError {
		t.Fatal("new higher-priority instruction did not revoke readiness")
	}
	if err := os.Mkdir(filepath.Join(dir, "src", ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library","path":"src/new.go"}`); !result.IsError {
		t.Fatal("nested repository instructions crossed boundary")
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library","path":"../outside"}`); !result.IsError {
		t.Fatal("outside ancestry accepted")
	}
}

func TestRepoRoutingDefaultsPreviewsAndBoundaries(t *testing.T) {
	s, dir := repoTestSession(t)
	if result := repoRun(t, s, "write_file", `{"path":"primary.txt","content":"primary"}`); result.IsError {
		t.Fatalf("primary needs preparation: %+v", result)
	}
	if err := os.WriteFile(filepath.Join(dir, "secondary.txt"), []byte("secondary"), 0600); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "read_file", `{"repo":"library","path":"secondary.txt"}`); result.IsError || !strings.Contains(result.Text(), "secondary") {
		t.Fatalf("secondary read: %+v", result)
	}
	if result := repoRun(t, s, "read_file", `{"path":"secondary.txt"}`); !result.IsError {
		t.Fatal("default root switched implicitly")
	}
	for _, tt := range s.sessionTools() {
		if err := agentapi.SchemaOf(tt).Validate(); err != nil {
			t.Fatalf("%s schema: %v", tt.Name(), err)
		}
		if tt.Name() == "write_file" {
			description := tool.Describe(context.Background(), agentapi.ToolCall{Name: tt.Name(), Arguments: json.RawMessage(`{"repo":"library","path":"new.txt","content":"x"}`)}, tt)
			if !strings.Contains(description.Summary, "[library]") {
				t.Fatalf("unscoped preview: %+v", description)
			}
		}
	}
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "secret.txt"), []byte("nested"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.workspace.AttachLocal(nested, "nested"); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "read_file", `{"repo":"library","path":"nested/secret.txt"}`); !result.IsError {
		t.Fatal("parent crossed attached root")
	}
	if result := repoRun(t, s, "glob", `{"repo":"library","pattern":"**/*.txt"}`); result.IsError || strings.Contains(result.Text(), "secret.txt") {
		t.Fatalf("walk crossed boundary: %+v", result)
	}
}

func TestRepoPreparationLimitsAndRootReplacement(t *testing.T) {
	s, dir := repoTestSession(t)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), make([]byte, repoInstructionBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library"}`); !result.IsError {
		t.Fatal("oversized instruction accepted")
	}
	if err := os.Remove(filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library"}`); result.IsError {
		t.Fatal(result)
	}
	repoPrefix(t, s)
	old := dir + "-old"
	if err := os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(old) })
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if result := repoRun(t, s, "bash", `{"repo":"library","command":"true"}`); !result.IsError {
		t.Fatal("replacement root kept instruction readiness")
	}
}

func TestRepoInstructionRenderedEnvelopeIsBounded(t *testing.T) {
	s, dir := repoTestSession(t)
	for i := 0; i < 60; i++ {
		path := filepath.Join(dir, ".hint/rules", fmt.Sprintf("%03d-%s.md", i, strings.Repeat("long", 40)))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# Important\nKeep this rule.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := s.loadRepoInstructions(context.Background(), "library", "target.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1024, 2048, 4096} {
		output, err := prepared.prompt(context.Background(), budget)
		if err != nil || len(output) > budget {
			t.Fatalf("budget %d: %d %v", budget, len(output), err)
		}
	}
}

func TestRepoPreparationIncludesCanonicalSymlinkAncestors(t *testing.T) {
	s, dir := repoTestSession(t)
	if err := os.MkdirAll(filepath.Join(dir, "physical/deep"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "physical/AGENTS.md"), []byte("CANONICAL_PARENT_RULE"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "physical/deep"), filepath.Join(dir, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if result := repoRun(t, s, "repo_prepare", `{"repo":"library","path":"alias/new.go"}`); result.IsError {
		t.Fatalf("prepare: %+v", result)
	}
	if prefix := repoPrefix(t, s); !strings.Contains(prefix, "CANONICAL_PARENT_RULE") {
		t.Fatal("symlink path bypassed canonical ancestor instructions")
	}
}

func TestAttachedOrdinaryFolderDoesNotInheritAncestorGitIgnore(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	s, _ := repoTestSession(t)
	parent := t.TempDir()
	cmd := exec.Command(git, "init", "-q", parent)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s %v", output, err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("*.go\n"), 0600); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(parent, "ordinary")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, "visible.go"), []byte("package visible\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.workspace.AttachLocal(child, "ordinary"); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, args string }{{"list_dir", `{"repo":"ordinary"}`}, {"glob", `{"repo":"ordinary","pattern":"*.go"}`}, {"grep", `{"repo":"ordinary","pattern":"package"}`}} {
		result := repoRun(t, s, tc.name, tc.args)
		if result.IsError || !strings.Contains(result.Text(), "visible.go") {
			t.Fatalf("%s inherited ancestor ignore: %+v", tc.name, result)
		}
	}
}
