package builtin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

const rulesDoc = "# Rules\n\nIntro.\n\n## Testing\n\nRun go test -race\nbefore every commit.\n\n### Flaky tests\n\nQuarantine them.\n\n## Style\n\nUse gofmt.\n"

// instructionFiles writes a global and a project instruction file into
// directories the tool has no root over — that is the point of it.
func instructionFiles(t *testing.T) (global, projectFile string) {
	t.Helper()
	home, repo := t.TempDir(), t.TempDir()
	global = filepath.Join(home, "HINT.md")
	projectFile = filepath.Join(repo, "HINT.md")
	if err := os.WriteFile(global, []byte("Always answer in English.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projectFile, []byte(rulesDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	return global, projectFile
}

func TestInstructions_Index(t *testing.T) {
	global, proj := instructionFiles(t)
	tl := builtin.NewInstructions([]string{global, proj})
	if tl.Name() != "instructions" || tl.Class() != agentapi.ClassRead {
		t.Fatalf("descriptor: %s %s", tl.Name(), tl.Class())
	}
	got := ok(t, run(t, tl, `{}`))
	for _, want := range []string{global + " (26 bytes)", proj + " (", "  Rules\n    Testing\n      Flaky tests\n    Style"} {
		if !strings.Contains(got, want) {
			t.Errorf("index lacks %q:\n%s", want, got)
		}
	}
	if got := ok(t, run(t, builtin.NewInstructions(nil), `{}`)); !strings.Contains(got, "no instruction files") {
		t.Errorf("empty index: %q", got)
	}
}

func TestInstructions_ReadFileAndSection(t *testing.T) {
	global, proj := instructionFiles(t)
	tl := builtin.NewInstructions([]string{global, proj})

	if got := ok(t, run(t, tl, `{"path":"`+proj+`"}`)); got != rulesDoc {
		t.Errorf("whole file = %q", got)
	}
	// A section comes with its subsections; the match ignores case and
	// the outline marker the prompt showed.
	got := ok(t, run(t, tl, `{"path":"`+proj+`","section":"testing [...]"}`))
	if !strings.HasPrefix(got, "## Testing\n") || !strings.Contains(got, "### Flaky tests") || strings.Contains(got, "## Style") {
		t.Errorf("section = %q", got)
	}
	failed(t, run(t, tl, `{"path":"`+proj+`","section":"Deploy"}`), "headings are: Rules; Testing; Flaky tests; Style")
}

func TestInstructions_ResolvesNamesAndRefusesOthers(t *testing.T) {
	global, proj := instructionFiles(t)
	tl := builtin.NewInstructions([]string{global, proj})

	// Both files are called HINT.md: the bare name is ambiguous, the
	// parent-relative one is not.
	failed(t, run(t, tl, `{"path":"HINT.md"}`), "names several instruction files")
	rel := filepath.Join(filepath.Base(filepath.Dir(proj)), "HINT.md")
	if got := ok(t, run(t, tl, `{"path":"`+rel+`"}`)); got != rulesDoc {
		t.Errorf("parent-relative name = %q", got)
	}
	// Anything the run did not discover is refused, however real.
	other := filepath.Join(filepath.Dir(proj), "secret.txt")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	failed(t, run(t, tl, `{"path":"`+other+`"}`), "is not an instruction file of this run")
	failed(t, run(t, tl, `{"section":"Testing"}`), "section needs a path")
	failed(t, run(t, tl, `{"quote":"x","path":"y"}`), "cannot be combined")
}

func TestInstructions_LocatesQuote(t *testing.T) {
	global, proj := instructionFiles(t)
	tl := builtin.NewInstructions([]string{global, proj})

	// The quote spans a line break and differs in case and punctuation.
	got := ok(t, run(t, tl, `{"quote":"run go test -race before every commit"}`))
	if !strings.HasPrefix(got, proj+":7\nRun go test -race\nbefore every commit.") {
		t.Errorf("location = %q", got)
	}
	if got := ok(t, run(t, tl, `{"quote":"answer in English"}`)); !strings.HasPrefix(got, global+":1\n") {
		t.Errorf("global location = %q", got)
	}
	failed(t, run(t, tl, `{"quote":"never heard of it"}`), "no instruction file of this run contains")
}
