package project_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

func TestInstructionDirs(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	j := func(parts ...string) string { return filepath.Join(append([]string{root}, parts...)...) }
	cases := []struct {
		name      string
		root, dir string
		want      []string
	}{
		{"at root", root, root, []string{root}},
		{"two below", root, j("a", "b"), []string{root, j("a"), j("a", "b")}},
		{"no root", "", j("a"), []string{j("a")}},
		{"outside root", root, filepath.Dir(root), []string{filepath.Dir(root)}},
		{"unclean", root + string(filepath.Separator), j("a") + string(filepath.Separator) + ".", []string{root, j("a")}},
	}
	for _, c := range cases {
		if got := project.InstructionDirs(c.root, c.dir); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: InstructionDirs = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestReadInstructions_PrecedenceAndOrder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"HINT.md":           "root hint\n",
		"AGENTS.md":         "root agents (must not be read)\n",
		"a/AGENTS.md":       "a agents\n",
		"a/CLAUDE.md":       "a claude (must not be read)\n",
		"a/b/CLAUDE.md":     "b claude\n",
		"a/b/c/HINT.md":     "   \n\n", // whitespace only: left out
		"a/b/c/d/HINT.md/":  "",        // a directory of that name is not a file
		"a/b/c/d/AGENTS.md": "d agents\n",
	})
	dirs := project.InstructionDirs(dir, filepath.Join(dir, "a", "b", "c", "d"))
	got, warnings := project.ReadInstructions(dirs, project.InstructionNames, project.DefaultInstructionBudget)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := []project.Instruction{
		{Path: filepath.Join(dir, "HINT.md"), Content: "root hint\n"},
		{Path: filepath.Join(dir, "a", "AGENTS.md"), Content: "a agents\n"},
		{Path: filepath.Join(dir, "a", "b", "CLAUDE.md"), Content: "b claude\n"},
		{Path: filepath.Join(dir, "a", "b", "c", "d", "AGENTS.md"), Content: "d agents\n"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestReadInstructions_Budget(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"HINT.md":       strings.Repeat("a", 10) + "\n",
		"a/HINT.md":     strings.Repeat("b", 20),
		"a/b/HINT.md":   "c",
		"a/b/c/HINT.md": "d",
	})
	dirs := project.InstructionDirs(dir, filepath.Join(dir, "a", "b", "c"))
	got, warnings := project.ReadInstructions(dirs, project.InstructionNames, 16)

	if len(got) != 2 {
		t.Fatalf("got %d instructions, want 2: %+v", len(got), got)
	}
	if got[0].Truncated || got[0].Content != strings.Repeat("a", 10)+"\n" {
		t.Errorf("first file within budget must be whole: %+v", got[0])
	}
	// 11 bytes spent, 5 remain: the second file is cut to 5 bytes.
	if !got[1].Truncated || got[1].Content != "bbbbb" {
		t.Errorf("second file must be cut at the remaining budget: %+v", got[1])
	}
	// The budget is spent: the two nearer files are skipped, and each
	// skip is a warning, as is the cut.
	if len(warnings) != 3 {
		t.Fatalf("warnings = %v", warnings)
	}
	if !strings.Contains(warnings[0], "a/HINT.md") || !strings.Contains(warnings[0], "cut at the 16-byte") {
		t.Errorf("cut warning: %q", warnings[0])
	}
	for _, w := range warnings[1:] {
		if !strings.Contains(w, "skipped") {
			t.Errorf("skip warning: %q", w)
		}
	}
}

func TestReadInstructions_Unreadable(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"HINT.md": "x"})
	// An unreadable file is a warning, not a failure of the run.
	got, warnings := project.ReadInstructions([]string{dir, filepath.Join(dir, "missing")}, project.InstructionNames, 100)
	if len(got) != 1 || len(warnings) != 0 {
		t.Fatalf("a missing directory is simply empty: %+v %v", got, warnings)
	}
}

func TestRenderInstructions(t *testing.T) {
	if project.RenderInstructions(nil) != "" {
		t.Fatal("no instructions must render nothing")
	}
	got := project.RenderInstructions([]project.Instruction{
		{Path: "/p/HINT.md", Content: "Be brief.\n\n"},
		{Path: "/p/sub/AGENTS.md", Content: "Use tabs", Truncated: true},
	})
	want := "<project_instructions>\n" +
		"<file path=\"/p/HINT.md\">\nBe brief.\n</file>\n" +
		"<file path=\"/p/sub/AGENTS.md\">\nUse tabs\n[... truncated at the instruction budget]\n</file>\n" +
		"</project_instructions>"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
