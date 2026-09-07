package project_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

func TestFindGitRoot(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"repo/.git/HEAD":        "",
		"repo/a/b/c/":           "",
		"worktree/.git":         "gitdir: /elsewhere\n", // a linked worktree keeps a file
		"worktree/src/":         "",
		"plain/deep/":           "",
		"repo/nested/.git/HEAD": "", // an inner repository wins over the outer one
		"repo/nested/pkg/":      "",
	})
	cases := []struct {
		dir  string
		want string
		ok   bool
	}{
		{"repo", "repo", true},
		{"repo/a/b/c", "repo", true},
		{"worktree/src", "worktree", true},
		{"repo/nested/pkg", "repo/nested", true},
	}
	for _, c := range cases {
		got, ok := project.FindGitRoot(filepath.Join(dir, filepath.FromSlash(c.dir)))
		if ok != c.ok || got != filepath.Join(dir, filepath.FromSlash(c.want)) {
			t.Errorf("FindGitRoot(%s) = %q, %v; want %q, %v", c.dir, got, ok, c.want, c.ok)
		}
	}

	// A plain directory finds nothing — unless the temp directory itself
	// sits inside a repository, which a developer's machine may arrange;
	// then the answer must be an ancestor of the fixture, never inside it.
	if got, ok := project.FindGitRoot(filepath.Join(dir, "plain", "deep")); ok {
		if got != dir && !strings.HasPrefix(dir, got+string(filepath.Separator)) {
			t.Errorf("FindGitRoot(plain/deep) = %q, not an ancestor of %q", got, dir)
		}
		if strings.HasPrefix(got, dir+string(filepath.Separator)) {
			t.Errorf("FindGitRoot(plain/deep) = %q inside the fixture", got)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "plain", ".git")); err == nil {
		t.Fatal("fixture must not contain plain/.git")
	}
}
