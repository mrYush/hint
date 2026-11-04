package project_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestInstructionSourcesConcurrentAccess(t *testing.T) {
	pc, err := project.Load(context.Background(), t.TempDir(), project.WithGit(""), project.WithOverview(project.None{}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				pc.Layout(context.Background(), nil)
				pc.InstructionSources()
				pc.Snapshot()
			}
		}()
	}
	for i := range 50 {
		pc.AddInstructionSources(context.Background(), []project.InstructionSource{{
			Path: "mcp://fake/?uri=resource", Content: strings.Repeat("x", i+1),
		}})
	}
	wg.Wait()
}

type summarizerFunc func(context.Context, string, string, int) (string, error)

func (f summarizerFunc) Summarize(ctx context.Context, path, content string, limit int) (string, error) {
	return f(ctx, path, content, limit)
}

func TestAddInstructionSourcesDoesNotLockSummarizer(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": strings.Repeat("Long rules.\n", 100)})
	var pc *project.Context
	summarizer := summarizerFunc(func(context.Context, string, string, int) (string, error) {
		if pc != nil {
			pc.Snapshot()
		}
		return "Short rules.", nil
	})
	var err error
	pc, err = project.Load(context.Background(), dir,
		project.WithGit(""), project.WithOverview(project.None{}),
		project.WithInstructionBudget(100), project.WithSummarizer(summarizer))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		pc.AddInstructionSources(context.Background(), []project.InstructionSource{{Path: "mcp://fake/?uri=rules", Content: "Remote rules."}})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("AddInstructionSources held its lock while calling the summarizer")
	}
}
