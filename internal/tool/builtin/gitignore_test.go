package builtin_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
)

// gitRepo turns the root into a repository whose .gitignore excludes
// build/ and *.log, so the pure-Go walks can be checked against git's
// rules; it skips when git is not installed.
func gitRepo(t *testing.T) (tool.Root, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	root := newRoot(t, map[string]string{
		".gitignore":    "build/\n*.log\n",
		"main.go":       "package main // needle",
		"build/out.go":  "package out // needle",
		"debug.log":     "needle",
		"docs/notes.md": "needle",
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}} {
		cmd := exec.Command(git, args...)
		cmd.Dir = root.Dir()
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root, git
}

func TestGitignore_PureGoWalks(t *testing.T) {
	root, git := gitRepo(t)

	got := ok(t, run(t, builtin.NewListDir(root, builtin.WithGit(git)), `{}`))
	if strings.Contains(got, "build") || strings.Contains(got, "debug.log") || !strings.Contains(got, "main.go") {
		t.Errorf("list_dir must honour .gitignore:\n%s", got)
	}
	got = ok(t, run(t, builtin.NewListDir(root, builtin.WithGit("")), `{}`))
	if !strings.Contains(got, "build/") || !strings.Contains(got, "debug.log") {
		t.Errorf("without git the walk keeps the old behaviour:\n%s", got)
	}

	got = ok(t, run(t, builtin.NewGlob(root, builtin.WithRipgrep(""), builtin.WithGit(git)), `{"pattern":"**/*.go"}`))
	if strings.Contains(got, "build/out.go") || !strings.Contains(got, "main.go") {
		t.Errorf("glob (pure Go) must honour .gitignore:\n%s", got)
	}

	got = ok(t, run(t, builtin.NewGrep(root, builtin.WithRipgrep(""), builtin.WithGit(git)), `{"pattern":"needle"}`))
	if strings.Contains(got, "build/") || strings.Contains(got, "debug.log") || !strings.Contains(got, "docs/notes.md") {
		t.Errorf("grep (pure Go) must honour .gitignore:\n%s", got)
	}
	got = ok(t, run(t, builtin.NewGrep(root, builtin.WithRipgrep(""), builtin.WithGit(git)), `{"pattern":"needle","path":"docs"}`))
	if !strings.Contains(got, "docs/notes.md") {
		t.Errorf("grep of a subdirectory builds rules relative to it:\n%s", got)
	}
}
