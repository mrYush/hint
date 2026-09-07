package project_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/project"
)

// TestDefaultsMatchConfig pins the duplicated constants: config applies
// these defaults when no source sets a limit, and it cannot import this
// package to read them.
func TestDefaultsMatchConfig(t *testing.T) {
	if project.DefaultInstructionBudget != config.DefaultInstructionBudget {
		t.Errorf("instruction budget: project %d, config %d", project.DefaultInstructionBudget, config.DefaultInstructionBudget)
	}
	if project.DefaultTreeDepth != config.DefaultOverviewDepth {
		t.Errorf("overview depth: project %d, config %d", project.DefaultTreeDepth, config.DefaultOverviewDepth)
	}
	if project.DefaultTreeMaxEntries != config.DefaultOverviewEntries {
		t.Errorf("overview entries: project %d, config %d", project.DefaultTreeMaxEntries, config.DefaultOverviewEntries)
	}
}

func TestGlobalInstructions(t *testing.T) {
	home := t.TempDir()
	global := filepath.Join(home, "HINT.md")
	writeTree(t, home, map[string]string{"HINT.md": "Always answer in English.\n"})
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": "Run go test.\n"})

	pc, err := project.Load(context.Background(), dir, project.WithGit(""), project.WithGlobalInstructions(global))
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 2 || pc.Instructions[0].Path != global || pc.Instructions[1].Content != "Run go test.\n" {
		t.Fatalf("instructions = %+v, want the global file first", pc.Instructions)
	}

	// The global file shares the budget: with room for it alone, the
	// project's own file is skipped with a warning.
	pc, err = project.Load(context.Background(), dir, project.WithGit(""), project.WithGlobalInstructions(global),
		project.WithInstructionBudget(len("Always answer in English.\n")))
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 1 || pc.Instructions[0].Path != global {
		t.Fatalf("instructions under a tight budget = %+v", pc.Instructions)
	}
	if len(pc.Warnings) != 1 || !strings.Contains(pc.Warnings[0], "skipped") {
		t.Errorf("warnings = %v, want one skip", pc.Warnings)
	}

	// A missing global file, or one that is a directory, is not an error
	// and not a warning.
	for _, path := range []string{filepath.Join(home, "missing.md"), home} {
		pc, err = project.Load(context.Background(), dir, project.WithGit(""), project.WithGlobalInstructions(path))
		if err != nil {
			t.Fatal(err)
		}
		if len(pc.Instructions) != 1 || len(pc.Warnings) != 0 {
			t.Errorf("%s: instructions = %+v, warnings = %v", path, pc.Instructions, pc.Warnings)
		}
	}

	// Running inside the global file's own directory lists it once.
	pc, err = project.Load(context.Background(), home, project.WithGit(""), project.WithGlobalInstructions(global))
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 1 {
		t.Errorf("global directory: instructions = %+v, want one", pc.Instructions)
	}
}
