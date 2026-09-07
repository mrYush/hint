package project_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeTree creates files under dir; keys are slash-separated relative
// paths, and a key ending in "/" is an empty directory.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if rel[len(rel)-1] == '/' {
			if err := os.MkdirAll(abs, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// gitPath returns the git executable or skips the test.
func gitPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	return p
}

// initRepo turns dir into a git repository with files, committing the
// ones named in tracked so that git's view has both tracked and untracked
// entries.
func initRepo(t *testing.T, git, dir string, tracked ...string) {
	t.Helper()
	gitRun(t, git, dir, "init", "-q")
	gitRun(t, git, dir, "config", "user.email", "test@example.com")
	gitRun(t, git, dir, "config", "user.name", "test")
	if len(tracked) > 0 {
		gitRun(t, git, dir, append([]string{"add", "--"}, tracked...)...)
		gitRun(t, git, dir, "commit", "-q", "-m", "init")
	}
}

func gitRun(t *testing.T, git, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(git, args...)
	cmd.Dir = dir
	// A test must not pick up the developer's global ignore or hooks.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
