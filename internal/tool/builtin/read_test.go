package builtin_test

import (
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestReadFile_NumbersLines(t *testing.T) {
	root := newRoot(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	tl := builtin.NewReadFile(root)

	if tl.Name() != "read_file" || tl.Class() != agentapi.ClassRead {
		t.Fatalf("descriptor: %s %s", tl.Name(), tl.Class())
	}
	got := ok(t, run(t, tl, `{"path":"a.txt"}`))
	want := "     1\tone\n     2\ttwo\n     3\tthree\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReadFile_OffsetLimitAndFooter(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 10; i++ {
		sb.WriteString("line\n")
	}
	root := newRoot(t, map[string]string{"a.txt": sb.String()})
	tl := builtin.NewReadFile(root)

	got := ok(t, run(t, tl, `{"path":"a.txt","offset":4,"limit":3}`))
	if !strings.HasPrefix(got, "     4\tline\n     5\tline\n     6\tline\n") {
		t.Fatalf("wrong window: %q", got)
	}
	if !strings.Contains(got, "(showing lines 4-6 of 10; call again with offset=7 to continue)") {
		t.Fatalf("footer missing: %q", got)
	}

	// Numbers spelled as strings — what small models send — are accepted.
	got2 := ok(t, run(t, tl, `{"path":"a.txt","offset":"4","limit":"3"}`))
	if got2 != got {
		t.Fatalf("quoted numbers gave a different result:\n%q\n%q", got2, got)
	}

	// Reading to the end has no footer.
	if got := ok(t, run(t, tl, `{"path":"a.txt","offset":9}`)); strings.Contains(got, "showing lines") {
		t.Fatalf("footer on a complete read: %q", got)
	}
}

func TestReadFile_Errors(t *testing.T) {
	root := newRoot(t, map[string]string{
		"a.txt":   "x\n",
		"bin.dat": "abc\x00def",
		"sub/f":   "",
	})
	tl := builtin.NewReadFile(root)

	tests := []struct {
		name, args, want string
	}{
		{"missing path", `{}`, "path is required"},
		{"bad json", `{"path":1}`, "invalid arguments"},
		{"negative offset", `{"path":"a.txt","offset":-1}`, "must not be negative"},
		{"not found", `{"path":"nope.txt"}`, "no such file"},
		{"directory", `{"path":"sub"}`, "is a directory"},
		{"binary", `{"path":"bin.dat"}`, "binary file"},
		{"outside root", `{"path":"../secret"}`, "outside the working directory"},
		{"absolute outside", `{"path":"/etc/passwd"}`, "outside the working directory"},
		{"offset past end", `{"path":"a.txt","offset":5}`, "past the end"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			failed(t, run(t, tl, tt.args), tt.want)
		})
	}
}

func TestReadFile_EmptyAndLongLines(t *testing.T) {
	long := strings.Repeat("y", 3000)
	root := newRoot(t, map[string]string{"empty": "", "long.txt": long + "\nshort\n", "crlf.txt": "a\r\nb\r\n"})
	tl := builtin.NewReadFile(root)

	if got := ok(t, run(t, tl, `{"path":"empty"}`)); got != "(empty file)" {
		t.Fatalf("empty file: %q", got)
	}
	got := ok(t, run(t, tl, `{"path":"long.txt"}`))
	if !strings.Contains(got, "... [line truncated]") || strings.Contains(got, long) {
		t.Fatalf("long line not cut: %d bytes", len(got))
	}
	if !strings.HasSuffix(got, "     2\tshort\n") {
		t.Fatalf("line after the long one lost: %q", got[len(got)-30:])
	}
	if got := ok(t, run(t, tl, `{"path":"crlf.txt"}`)); strings.Contains(got, "\r") {
		t.Fatalf("carriage returns leaked into the output: %q", got)
	}
}

func TestReadFile_OutputTruncatedByLimits(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("a fairly long line of text to fill the buffer\n")
	}
	root := newRoot(t, map[string]string{"big.txt": sb.String()})
	tl := builtin.NewReadFile(root, builtin.WithLimits(tool.Limits{MaxOutput: 2000}))

	got := ok(t, run(t, tl, `{"path":"big.txt"}`))
	if len(got) > 2000 || !strings.Contains(got, "[output truncated:") {
		t.Fatalf("decorator did not truncate: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "     1\t") || !strings.Contains(got, "   500\t") {
		t.Fatalf("head or tail missing: %q ... %q", got[:20], got[len(got)-60:])
	}
}
