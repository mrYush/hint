package builtin_test

import (
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool/builtin"
)

func TestInstructionsReadsMemorySource(t *testing.T) {
	source := project.InstructionSource{Path: "mcp://fake/?uri=test%3A%2F%2Frules", Content: rulesDoc}
	tl := builtin.NewInstructions([]string{source.Path}, source)
	if got := ok(t, run(t, tl, `{}`)); !strings.Contains(got, source.Path) || !strings.Contains(got, "Testing") {
		t.Errorf("index = %q", got)
	}
	if got := ok(t, run(t, tl, `{"path":"`+source.Path+`"}`)); got != rulesDoc {
		t.Errorf("whole resource = %q", got)
	}
	got := ok(t, run(t, tl, `{"path":"`+source.Path+`","section":"testing"}`))
	if !strings.HasPrefix(got, "## Testing\n") || strings.Contains(got, "## Style") {
		t.Errorf("resource section = %q", got)
	}
	if got := ok(t, run(t, tl, `{"quote":"run go test -race"}`)); !strings.HasPrefix(got, source.Path+":7\n") {
		t.Errorf("resource quote = %q", got)
	}
}
