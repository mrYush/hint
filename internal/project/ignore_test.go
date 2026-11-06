package project_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

func TestBasic(t *testing.T) {
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{".", true, false},
		{"", true, false},
		{"main.go", false, false},
		{".env", false, true},
		{".git", true, true},
		{"docs/.private.md", false, true},
		{"node_modules", true, true},
		{"vendor", true, true},
		{"vendor.go", false, false},  // a file named like a skipped dir is not one
		{"pkg/target", true, true},   // the rule is by name, at any depth
		{"target.txt", false, false}, // only directories are pruned by name
		{"..", true, false},          // never an entry of a walk, but not hidden either
		{"vendor/x.go", false, true}, // beneath a pruned directory, asked out of walk order
		{"a/.git/HEAD", false, true}, // a hidden ancestor hides the file
	}
	for _, c := range cases {
		if got := project.Basic.Ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("Basic.Ignored(%q, %v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}
	if got := project.SkippedDirs(); !reflect.DeepEqual(got, []string{"__pycache__", "dist", "node_modules", "target", "vendor"}) {
		t.Errorf("SkippedDirs() = %v", got)
	}
	if project.IsHidden(".") || project.IsHidden("..") || !project.IsHidden(".x") || project.IsHidden("x") {
		t.Error("IsHidden misclassifies a name")
	}
}

func TestRules_NoGit(t *testing.T) {
	ig, err := project.NewRules("").Ignorer(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ig != project.Basic {
		t.Fatalf("without git the rules must be Basic, got %T", ig)
	}
}

func TestRules_OutsideRepository(t *testing.T) {
	git := gitPath(t)
	dir := t.TempDir()
	ig, err := project.NewRules(git).Ignorer(context.Background(), dir)
	if err != nil {
		t.Fatalf("not being in a repository is not an error: %v", err)
	}
	if ig != project.Basic {
		t.Fatalf("outside a repository the rules must be Basic, got %T", ig)
	}
}

func TestRules_Gitignore(t *testing.T) {
	git := gitPath(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore":          "build/\n*.log\n!keep.log\n",
		"main.go":             "",
		"build/out.bin":       "",
		"debug.log":           "",
		"keep.log":            "",
		"untracked/new.go":    "",
		"sub/.gitignore":      "secret.txt\n",
		"sub/secret.txt":      "",
		"sub/public.txt":      "",
		"vendor/committed.go": "",
		"empty/":              "",
	})
	initRepo(t, git, dir, ".gitignore", "main.go", "vendor/committed.go")

	ig, err := project.NewRules(git).Ignorer(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ig == project.Basic {
		t.Fatal("inside a repository the rules must come from git")
	}
	cases := []struct {
		rel   string
		isDir bool
		want  bool
	}{
		{".", true, false},
		{"main.go", false, false},          // tracked
		{"untracked/new.go", false, false}, // untracked and not ignored
		{"untracked", true, false},
		{"build/out.bin", false, true},  // ignored by build/
		{"build", true, true},           // a directory with nothing git lists is pruned
		{"debug.log", false, true},      // ignored by *.log
		{"keep.log", false, false},      // re-admitted by !keep.log
		{"sub/secret.txt", false, true}, // nested .gitignore
		{"sub/public.txt", false, false},
		{"sub", true, false},
		{"empty", true, true},                // git lists no files there, so nothing to show
		{"vendor/committed.go", false, true}, // Basic still prunes vendor even when committed
		{"vendor", true, true},
		{".gitignore", false, true}, // tracked, but hidden entries stay hidden
	}
	for _, c := range cases {
		if got := ig.Ignored(c.rel, c.isDir); got != c.want {
			t.Errorf("Ignored(%q, %v) = %v, want %v", c.rel, c.isDir, got, c.want)
		}
	}

	// Rules are relative to the directory they were built for: asked
	// about sub, git lists sub's files as top-level entries.
	subIg, err := project.NewRules(git).Ignorer(context.Background(), filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if subIg.Ignored("public.txt", false) || !subIg.Ignored("secret.txt", false) {
		t.Error("rules for a subdirectory are not relative to it")
	}
}

func TestRules_Submodule(t *testing.T) {
	git := gitPath(t)
	dir := t.TempDir()
	// A gitlink shows up in ls-files as one entry at the mount point; its
	// contents must be kept, since git has nothing to say about them.
	// Building a real submodule needs network-free plumbing that differs
	// across git versions, so the entry is simulated with a tracked file
	// whose path is later a directory on disk.
	writeTree(t, dir, map[string]string{"lib": "", "main.go": ""})
	initRepo(t, git, dir, "lib", "main.go")
	ig, err := project.NewRules(git).Ignorer(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ig.Ignored("lib/inner/file.go", false) || ig.Ignored("lib/inner", true) {
		t.Error("entries beneath a listed path must be kept")
	}
	if !ig.Ignored("lib/.hidden", false) {
		t.Error("Basic still applies beneath a listed path")
	}
}

func TestRulesTrackedFileUnderIgnoredDirectory(t *testing.T) {
	git := gitPath(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"build/kept.go": "package kept", "build/ignored.go": "package ignored", "odd\nname.go": "package odd"})
	initRepo(t, git, dir, "build/kept.go", "odd\nname.go")
	writeTree(t, dir, map[string]string{".gitignore": "build/\n*.go\n"})
	ig, err := project.NewRules(git).Ignorer(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ig.Ignored("build", true) || ig.Ignored("build/kept.go", false) || ig.Ignored("odd\nname.go", false) {
		t.Fatal("tracked entries were excluded")
	}
	if !ig.Ignored("build/ignored.go", false) {
		t.Fatal("untracked ignored source admitted")
	}
	if err := project.IgnoreError(ig); err != nil {
		t.Fatal(err)
	}
}

func TestRulesNeverRunRepositoryFSMonitorHook(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture hook uses a POSIX shell")
	}
	git := gitPath(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"build/kept.go": "package kept"})
	initRepo(t, git, dir)
	gitRun(t, git, dir, "add", "build/kept.go")
	writeTree(t, dir, map[string]string{".gitignore": "build/\n"})
	hook := filepath.Join(t.TempDir(), "monitor")
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("HINT_TEST_FSMONITOR_MARKER", marker)
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nprintf 'ran\\n' > \"$HINT_TEST_FSMONITOR_MARKER\"\nprintf 'token\\0'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, git, dir, "config", "core.fsmonitor", hook)
	gitRun(t, git, dir, "ls-files", "--cached")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fixture did not activate fsmonitor hook: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	ig, err := project.NewRules(git).Ignorer(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if ig.Ignored("build", true) || ig.Ignored("build/kept.go", false) {
		t.Fatal("tracked file disappeared")
	}
	if err := project.IgnoreError(ig); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("read-only policy ran a repository hook: %v", err)
	}
}
