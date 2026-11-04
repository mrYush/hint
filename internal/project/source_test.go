package project_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

func TestInstructionSourcesShareBudgetAndOutline(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": "# Local\n\nLocal rules.\n"})
	source := project.InstructionSource{
		Path:    "mcp://fake/?uri=test%3A%2F%2Frules",
		Content: bigDoc(20),
	}
	pc, err := project.Load(context.Background(), dir,
		project.WithGit(""),
		project.WithOverview(project.None{}),
		project.WithInstructionSources(source),
		project.WithInstructionBudget(1000),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 2 {
		t.Fatalf("instructions = %+v", pc.Instructions)
	}
	remote := pc.Instructions[1]
	if remote.Path != source.Path || !remote.Outlined || !strings.Contains(remote.Content, "[...]") {
		t.Errorf("remote instruction = %+v", remote)
	}
	if total := len(pc.Instructions[0].Content) + len(remote.Content); total > 1000 {
		t.Errorf("content uses %d bytes, over budget", total)
	}
	if got := pc.InstructionPaths[len(pc.InstructionPaths)-1]; got != source.Path {
		t.Errorf("last instruction path = %q", got)
	}
	if got := pc.InstructionSources(); len(got) != 1 || got[0] != source {
		t.Errorf("sources = %+v", got)
	}
}
