package project_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

func TestLoad_NoGit(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"HINT.md":       "root rules\n",
		"sub/AGENTS.md": "sub rules\n",
		"sub/main.go":   "",
		"sub/build/x.o": "",
		"sub/.env":      "",
	})
	// Without a .git anywhere in the fixture there is no root, so only
	// the working directory's own file is read.
	pc, err := project.Load(context.Background(), filepath.Join(dir, "sub"), project.WithGit(""))
	if err != nil {
		t.Fatal(err)
	}
	if pc.Dir != filepath.Join(dir, "sub") {
		t.Errorf("Dir = %s", pc.Dir)
	}
	if pc.GitRoot != "" && !strings.HasPrefix(dir, pc.GitRoot) {
		t.Errorf("GitRoot = %s inside the fixture", pc.GitRoot)
	}
	if len(pc.Warnings) != 0 {
		t.Errorf("warnings: %v", pc.Warnings)
	}
	if n := len(pc.Instructions); n == 0 || pc.Instructions[n-1].Content != "sub rules\n" {
		t.Errorf("instructions: %+v", pc.Instructions)
	}
	want := "AGENTS.md\nbuild/\n  x.o\nmain.go\n(showing 2 levels; use list_dir or glob to see deeper)"
	if pc.Overview != want {
		t.Errorf("overview:\n%s", pc.Overview)
	}
}

func TestLoad_Git(t *testing.T) {
	git := gitPath(t)
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		".gitignore":    "build/\n",
		"HINT.md":       "root rules\n",
		"sub/CLAUDE.md": "sub rules\n",
		"sub/main.go":   "",
		"sub/build/x.o": "",
	})
	initRepo(t, git, dir, ".gitignore", "HINT.md", "sub/main.go")

	pc, err := project.Load(context.Background(), filepath.Join(dir, "sub"),
		project.WithGit(git), project.WithOverview(project.Tree{Depth: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if pc.GitRoot != dir {
		t.Errorf("GitRoot = %s, want %s", pc.GitRoot, dir)
	}
	if len(pc.Instructions) != 2 || pc.Instructions[0].Path != filepath.Join(dir, "HINT.md") || pc.Instructions[1].Path != filepath.Join(dir, "sub", "CLAUDE.md") {
		t.Errorf("instructions root→leaf: %+v", pc.Instructions)
	}
	if pc.Overview != "CLAUDE.md\nmain.go\n(showing 1 level; use list_dir or glob to see deeper)" {
		t.Errorf("overview must drop the ignored build/:\n%s", pc.Overview)
	}
}

func TestLoad_OptionsAndErrors(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"RULES.md": strings.Repeat("r", 100), "f": ""})

	pc, err := project.Load(context.Background(), dir, project.WithGit(""),
		project.WithInstructionNames("RULES.md"), project.WithInstructionBudget(10), project.WithOverview(project.None{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 1 || !pc.Instructions[0].Truncated || pc.Instructions[0].Content != strings.Repeat("r", 10) {
		t.Errorf("custom names and budget: %+v", pc.Instructions)
	}
	if len(pc.Warnings) != 1 || !strings.Contains(pc.Warnings[0], "cut at the 10-byte") {
		t.Errorf("warnings: %v", pc.Warnings)
	}
	if pc.Overview != "" {
		t.Errorf("None must render nothing: %q", pc.Overview)
	}

	if _, err := project.Load(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory is an error")
	}
	if _, err := project.Load(context.Background(), filepath.Join(dir, "f")); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("a file is an error: %v", err)
	}
}
