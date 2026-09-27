package project_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

// bigDoc is an instruction file too long for the budgets below: twenty
// sections of a few lines each.
func bigDoc(sections int) string {
	var b strings.Builder
	b.WriteString("# Project rules\n\nThese rules apply everywhere. Read them first.\n")
	for i := 1; i <= sections; i++ {
		fmt.Fprintf(&b, "\n## Section %d\n\nRule %d says one thing. It also says another thing, at some length, so that the section is worth outlining rather than showing.\n\n- detail a\n- detail b\n", i, i)
	}
	return b.String()
}

func TestInstructionBudgetFor(t *testing.T) {
	cases := map[int]int{
		0:         project.DefaultInstructionBudget, // unknown window
		131072:    project.DefaultInstructionBudget, // 128k: the default is a sixteenth of it
		1_000_000: project.DefaultInstructionBudget, // never above the default
		8192:      2048,                             // a local model: a sixteenth of its window
		1000:      project.MinInstructionBudget,     // never below the floor
	}
	for window, want := range cases {
		if got := project.InstructionBudgetFor(window); got != want {
			t.Errorf("InstructionBudgetFor(%d) = %d, want %d", window, got, want)
		}
	}
}

func TestFit_NearestFileSurvivesAsOutline(t *testing.T) {
	dir := t.TempDir()
	root, leaf := bigDoc(20), "# Leaf\n\nBe brief. Always.\n"
	writeTree(t, dir, map[string]string{"HINT.md": root, "a/HINT.md": leaf})
	dirs := project.InstructionDirs(dir, filepath.Join(dir, "a"))

	const budget = 1500
	got, warnings := project.ReadInstructions(dirs, project.InstructionNames, budget)
	if len(got) != 2 {
		t.Fatalf("instructions = %+v, want both files", got)
	}
	// The outer file is over budget: some sections in full, the rest as
	// heading + first sentence, all headings kept.
	r := got[0]
	if !r.Outlined || r.Truncated || r.Size != len(root) || !strings.Contains(r.Content, "[...]") {
		t.Fatalf("root = %+v", r)
	}
	for i := 1; i <= 20; i++ {
		if !strings.Contains(r.Content, fmt.Sprintf("## Section %d\n", i)) {
			t.Errorf("root lost the heading of section %d", i)
		}
	}
	if !strings.Contains(r.Content, "Rule 1 says one thing. It also says another thing") {
		t.Error("the first section should have been shown in full before later ones were outlined")
	}
	// The nearer file is small and untouched — before WP0.12 it was the
	// one skipped once the outer file had spent the budget.
	if l := got[1]; l.Content != leaf || l.Outlined || l.Truncated {
		t.Fatalf("leaf = %+v", l)
	}
	if total := len(got[0].Content) + len(got[1].Content); total > budget {
		t.Errorf("rendered %d bytes, over the %d budget", total, budget)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "sections shown as headings only") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestFit_WholeFileFitsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": guideDoc})
	got, warnings := project.ReadInstructions([]string{dir}, project.InstructionNames, len(guideDoc))
	if len(got) != 1 || got[0].Content != guideDoc || got[0].Outlined || got[0].Truncated || len(warnings) != 0 {
		t.Fatalf("got %+v, warnings %v", got, warnings)
	}
}

func TestFit_OutlinesOnlyWhenTheyDoNotFit(t *testing.T) {
	// Even the outlines overflow: the outer one is cut at a line and the
	// nearer file is skipped, as before WP0.12.
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": bigDoc(20), "a/HINT.md": "# Leaf\n\nBe brief.\n"})
	dirs := project.InstructionDirs(dir, filepath.Join(dir, "a"))

	const budget = 100
	got, warnings := project.ReadInstructions(dirs, project.InstructionNames, budget)
	if len(got) != 1 || !got[0].Truncated || len(got[0].Content) > budget || !strings.HasSuffix(got[0].Content, "\n") {
		t.Fatalf("got %+v", got)
	}
	if !strings.HasPrefix(got[0].Content, "# Project rules\nThese rules apply everywhere. [...]\n") {
		t.Errorf("the cut outline should start with the first entry: %q", got[0].Content)
	}
	if len(warnings) != 2 || !strings.Contains(warnings[0], "cut at the 100-byte") || !strings.Contains(warnings[1], "skipped") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestFit_HeadlessFileIsCutNotOutlined(t *testing.T) {
	dir := t.TempDir()
	text := strings.Repeat("a rule without any heading\n", 100)
	writeTree(t, dir, map[string]string{"HINT.md": text})
	got, _ := project.ReadInstructions([]string{dir}, project.InstructionNames, 500)
	if len(got) != 1 || got[0].Outlined || !got[0].Truncated || len(got[0].Content) > 500 || !strings.HasSuffix(got[0].Content, "\n") {
		t.Fatalf("got %+v", got)
	}
}

// fakeSummarizer answers with a fixed summary and records what it was
// asked, or fails when err is set.
type fakeSummarizer struct {
	summary string
	err     error
	calls   []string // path each call was about
	limits  []int
}

func (f *fakeSummarizer) Summarize(_ context.Context, path, _ string, maxBytes int) (string, error) {
	f.calls = append(f.calls, path)
	f.limits = append(f.limits, maxBytes)
	return f.summary, f.err
}

func TestFit_SummarizesOnlyWhatOutlinesCannotFit(t *testing.T) {
	dir := t.TempDir()
	root, leaf := bigDoc(20), "# Leaf\n\nBe brief.\n"
	writeTree(t, dir, map[string]string{"HINT.md": root, "a/HINT.md": leaf})
	paths := project.InstructionPaths("", project.InstructionDirs(dir, filepath.Join(dir, "a")), project.InstructionNames)
	sum := &fakeSummarizer{summary: "# Project rules (summary)\n\nTwenty sections of rules.\n"}

	const budget = 300
	got, warnings := project.ReadInstructionFiles(context.Background(), paths, budget, sum)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	// Only the file that could not fit was summarized, to what was left
	// after the small file kept its own words.
	if len(sum.calls) != 1 || sum.calls[0] != paths[0] || sum.limits[0] != budget-len(leaf) {
		t.Fatalf("summarizer calls = %v limits = %v", sum.calls, sum.limits)
	}
	if r := got[0]; !r.Summarized || r.Content != sum.summary || r.Size != len(root) || r.Outlined || r.Truncated {
		t.Fatalf("root = %+v", r)
	}
	if l := got[1]; l.Summarized || l.Content != leaf {
		t.Fatalf("leaf = %+v", l)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "summarized by the model") || !strings.Contains(warnings[0], "instructions tool reads the original") {
		t.Errorf("warnings = %v", warnings)
	}

	// With enough budget for the outlines, no summary is asked for even
	// though a summarizer is available: the user's words come first.
	sum.calls = nil
	got, _ = project.ReadInstructionFiles(context.Background(), paths, 1500, sum)
	if len(sum.calls) != 0 || !got[0].Outlined || got[0].Summarized {
		t.Errorf("a fitting outline must not be summarized: calls %v, root %+v", sum.calls, got[0])
	}
}

func TestFit_SummaryFailureFallsBackToOutline(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": bigDoc(20)})
	sum := &fakeSummarizer{err: errors.New("model offline")}
	got, warnings := project.ReadInstructionFiles(context.Background(), []string{filepath.Join(dir, "HINT.md")}, 100, sum)
	if len(got) != 1 || got[0].Summarized || !got[0].Truncated {
		t.Fatalf("got %+v", got)
	}
	if len(warnings) < 1 || !strings.Contains(warnings[0], "could not be summarized (model offline)") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestCachedSummarizer(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "instructions")
	inner := &fakeSummarizer{summary: "short"}
	c := project.CachedSummarizer{Dir: cache, Inner: inner}
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		got, err := c.Summarize(ctx, "/p/HINT.md", "long content", 100)
		if err != nil || got != "short" {
			t.Fatalf("call %d: %q, %v", i, got, err)
		}
	}
	if len(inner.calls) != 1 {
		t.Fatalf("inner summarizer called %d times for the same content, want 1", len(inner.calls))
	}
	sum := sha256.Sum256([]byte("long content"))
	if _, err := os.Stat(filepath.Join(cache, hex.EncodeToString(sum[:])+".md")); err != nil {
		t.Errorf("cache entry named by the content hash: %v", err)
	}
	// Changed content is a new entry; a failing inner call caches nothing.
	if _, _ = c.Summarize(ctx, "/p/HINT.md", "other content", 100); len(inner.calls) != 2 {
		t.Errorf("changed content must be summarized again: %d calls", len(inner.calls))
	}
	inner.err = errors.New("down")
	if _, err := c.Summarize(ctx, "/p/HINT.md", "third", 100); err == nil {
		t.Error("inner error must surface")
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 2 {
		t.Errorf("cache holds %d entries, want 2", len(entries))
	}
}

func TestLoad_ScalesBudgetToWindow(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": bigDoc(20)})

	// An 8k model gets a 2 KiB budget by default...
	pc, err := project.Load(context.Background(), dir, project.WithGit(""), project.WithContextWindow(8192))
	if err != nil {
		t.Fatal(err)
	}
	if len(pc.Instructions) != 1 || len(pc.Instructions[0].Content) > 2048 || !pc.Instructions[0].Outlined {
		t.Fatalf("instructions for an 8k window = %+v", pc.Instructions)
	}
	if len(pc.InstructionPaths) != 1 || pc.InstructionPaths[0] != filepath.Join(dir, "HINT.md") {
		t.Errorf("InstructionPaths = %v", pc.InstructionPaths)
	}
	// ...unless the budget was set explicitly, which is taken as given.
	pc, err = project.Load(context.Background(), dir, project.WithGit(""), project.WithContextWindow(8192), project.WithInstructionBudget(project.DefaultInstructionBudget))
	if err != nil {
		t.Fatal(err)
	}
	if pc.Instructions[0].Outlined || pc.Instructions[0].Content != bigDoc(20) {
		t.Fatalf("an explicit budget must not be scaled: %+v", pc.Instructions[0])
	}
}

func TestRenderInstructions_MarksReducedFiles(t *testing.T) {
	got := project.RenderInstructions([]project.Instruction{
		{Path: "/p/HINT.md", Content: "# A\nfirst. [...]\n", Outlined: true},
		{Path: "/p/sub/HINT.md", Content: "Summary.", Summarized: true, Size: 9000},
	})
	for _, want := range []string{
		`<file path="/p/HINT.md" outline="true">`,
		"[... sections ending in [...] were left out at the instruction budget; read one with the instructions tool: path /p/HINT.md, section = its heading]",
		`<file path="/p/sub/HINT.md" summary="true">`,
		"[... this is a model-written summary of the 9000-byte file; the instructions tool returns the original /p/sub/HINT.md]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering lacks %q:\n%s", want, got)
		}
	}
}
