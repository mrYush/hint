package project_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

// update regenerates testdata/split_rules.golden from the current renderer.
var update = flag.Bool("update", false, "rewrite the golden rendered prompt")

const splitGolden = "testdata/split_rules.golden"

// splitProject is a project that keeps its instructions split: a short
// HINT.md, hint's own rules (one always, one for Go files, one with a
// broken header, one with a bad pattern) and a Cursor rule, plus a
// subdirectory with its own file and rule.
func splitProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{
		"HINT.md":                 "# Root\n\nRoot rules apply everywhere.\n",
		".hint/rules/always.md":   "# Always on\n\nApplies to every file.\n",
		".hint/rules/go.md":       "---\ntitle: Go style\npaths:\n  - \"**/*.go\"\n---\n\n# Go\n\nUse gofmt. Keep errors last.\n",
		".hint/rules/broken.md":   "---\npaths: [unclosed\n---\n# Broken header\n\nStill loads, always.\n",
		".hint/rules/badglob.md":  "---\npaths: ['[']\n---\n# Bad glob\n\nLoads always too.\n",
		".hint/rules/notes.txt":   "not a rule file\n",
		".cursor/rules/ts.mdc":    "---\ndescription: TypeScript conventions\nglobs: *.ts, *.tsx\nalwaysApply: false\n---\nPrefer const.\n",
		"sub/AGENTS.md":           "Sub rules.\n",
		"sub/.hint/rules/sub.md":  "Sub rule, always.\n",
		"sub/.hint/rules/deep.md": "---\npaths: [\"deep/*.go\"]\n---\nDeep rule.\n",
	})
	return dir
}

func TestDiscoverRules(t *testing.T) {
	dir := splitProject(t)
	j := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	rules, warnings := project.DiscoverRules([]string{dir, j("sub")})

	type want struct {
		path, root, title string
		paths             []string
	}
	wants := []want{
		{j(".hint", "rules", "always.md"), dir, "Always on", nil},
		{j(".hint", "rules", "badglob.md"), dir, "Bad glob", nil},
		{j(".hint", "rules", "broken.md"), dir, "Broken header", nil},
		{j(".hint", "rules", "go.md"), dir, "Go style", []string{"**/*.go"}},
		{j(".cursor", "rules", "ts.mdc"), dir, "TypeScript conventions", []string{"*.ts", "*.tsx"}},
		{j("sub", ".hint", "rules", "deep.md"), j("sub"), "deep", []string{"deep/*.go"}},
		{j("sub", ".hint", "rules", "sub.md"), j("sub"), "sub", nil},
	}
	if len(rules) != len(wants) {
		t.Fatalf("rules = %+v, want %d", rules, len(wants))
	}
	for i, w := range wants {
		r := rules[i]
		if r.Path != w.path || r.Root != w.root || r.Title != w.title || !reflect.DeepEqual(r.Paths, w.paths) {
			t.Errorf("rule %d = %+v, want %+v", i, r, w)
		}
	}
	// Two authoring mistakes, two warnings, both rules still loaded (always).
	if len(warnings) != 2 || !strings.Contains(warnings[0], "badglob.md") || !strings.Contains(warnings[1], "front matter not read") {
		t.Errorf("warnings = %v", warnings)
	}
}

func TestRuleMatches(t *testing.T) {
	r := project.Rule{Root: "/p", Paths: []string{"internal/**/*.go", "*.md"}}
	cases := map[string]bool{
		"/p/internal/a/b.go":  true,
		"/p/internal/b.go":    true,
		"/p/cmd/b.go":         false,
		"/p/docs/x/README.md": true,  // no slash: the name at any depth
		"/q/internal/b.go":    false, // outside the root
		"/p/../q/b.md":        false,
	}
	for abs, want := range cases {
		if got := r.Matches(abs); got != want {
			t.Errorf("Matches(%s) = %v, want %v", abs, got, want)
		}
	}
	if (project.Rule{Root: "/p"}).Matches("/p/a.go") {
		t.Error("a rule without paths is not conditional and matches nothing")
	}
}

func TestActivation(t *testing.T) {
	goRule := project.Rule{Path: "/p/.hint/rules/go.md", Root: "/p", Title: "Go", Paths: []string{"**/*.go"}}
	tsRule := project.Rule{Path: "/p/.cursor/rules/ts.mdc", Root: "/p", Title: "TS", Paths: []string{"*.ts"}}
	always := project.Rule{Path: "/p/.hint/rules/always.md", Root: "/p", Title: "Always"}
	a := project.NewActivation([]project.Rule{goRule, always, tsRule})

	if got := a.Pending(); len(got) != 2 || got[0].Title != "Go" || got[1].Title != "TS" {
		t.Fatalf("pending = %+v", got)
	}
	if got := a.Touch("/p/README.md"); len(got) != 0 {
		t.Errorf("an unrelated touch activated %+v", got)
	}
	// One touch activates every rule it matches, once.
	if got := a.Touch("/p/x.ts", "/p/cmd/main.go"); len(got) != 2 || got[0].Title != "Go" || got[1].Title != "TS" {
		t.Fatalf("activated = %+v, want Go then TS in discovery order", got)
	}
	if got := a.Touch("/p/other.go"); len(got) != 0 {
		t.Errorf("a second touch re-activated %+v", got)
	}
	if got := a.Active(); len(got) != 2 || len(a.Pending()) != 0 {
		t.Errorf("active = %+v, pending = %+v", got, a.Pending())
	}
}

func TestLoad_SplitProject(t *testing.T) {
	dir := splitProject(t)
	j := func(parts ...string) string { return filepath.Join(append([]string{dir}, parts...)...) }
	pc, err := project.Load(context.Background(), j("sub"), project.WithGit(""), project.WithOverview(project.None{}))
	if err != nil {
		t.Fatal(err)
	}
	// Without a git root only sub is an instruction directory: its own
	// rules are found, the parent's are not — that is what a repository
	// root is for.
	if len(pc.Rules) != 2 {
		t.Fatalf("rules from sub alone = %+v", pc.Rules)
	}

	// Pretend the fixture is a repository so the whole tree is read.
	writeTree(t, dir, map[string]string{".git/HEAD": "ref: refs/heads/main\n"})
	pc, err = project.Load(context.Background(), j("sub"), project.WithGit(""), project.WithOverview(project.None{}))
	if err != nil {
		t.Fatal(err)
	}
	// Always-loaded files, in "nearest wins" order: each directory's
	// unconditional rules right after its own instruction file.
	wantPaths := []string{
		j("HINT.md"), j(".hint", "rules", "always.md"), j(".hint", "rules", "badglob.md"), j(".hint", "rules", "broken.md"),
		j("sub", "AGENTS.md"), j("sub", ".hint", "rules", "sub.md"),
	}
	var gotPaths []string
	for _, in := range pc.Instructions {
		gotPaths = append(gotPaths, in.Path)
		if in.Paths != nil || strings.Contains(in.Content, "---") {
			t.Errorf("%s: an always file must carry no paths and no front matter: %+v", in.Path, in)
		}
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Errorf("always files = %v\nwant %v", gotPaths, wantPaths)
	}
	// The instructions tool may read the conditional ones too.
	if n := len(pc.InstructionPaths); n != len(wantPaths)+3 || pc.InstructionPaths[n-1] != j("sub", ".hint", "rules", "deep.md") {
		t.Errorf("InstructionPaths = %v", pc.InstructionPaths)
	}
	if len(pc.Warnings) != 2 {
		t.Errorf("warnings = %v", pc.Warnings)
	}

	// A touch on a Go file brings the Go rule in — after its directory's
	// file, with its paths on the block — and drops it from the index.
	act := project.NewActivation(pc.Rules)
	act.Touch(j("cmd", "main.go"))
	instructions, warnings := pc.Layout(context.Background(), act.Active())
	if len(warnings) != 0 {
		t.Errorf("layout warnings = %v", warnings)
	}
	var goIn *project.Instruction
	for i := range instructions {
		if instructions[i].Path == j(".hint", "rules", "go.md") {
			goIn = &instructions[i]
		}
	}
	if goIn == nil || !reflect.DeepEqual(goIn.Paths, []string{"**/*.go"}) || !strings.HasPrefix(goIn.Content, "# Go\n") {
		t.Fatalf("go rule in layout = %+v", goIn)
	}
	if instructions[4].Path != goIn.Path || instructions[5].Path != j("sub", "AGENTS.md") {
		t.Errorf("the go rule must follow its directory's always files: %v", pathsOf(instructions))
	}

	rendered := project.RenderInstructions(instructions) + "\n" + project.RenderRuleIndex(act.Pending())
	rendered = strings.ReplaceAll(rendered, dir, "$ROOT")
	if *update {
		if err := os.WriteFile(splitGolden, []byte(rendered), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(splitGolden)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if rendered != string(want) {
		t.Errorf("rendered prompt differs from %s (run with -update after checking):\n%s", splitGolden, rendered)
	}
}

func pathsOf(instructions []project.Instruction) []string {
	out := make([]string, len(instructions))
	for i, in := range instructions {
		out[i] = in.Path
	}
	return out
}
