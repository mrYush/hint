package tool_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/tool"
)

func lines(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		b.WriteString(strings.Repeat("x", 20))
		b.WriteString(" line ")
		b.WriteString(strings.Repeat("0", 3-len(itoa(i))) + itoa(i))
		b.WriteByte('\n')
	}
	return b.String()
}

func itoa(i int) string {
	return strings.TrimSpace(strings.Repeat(" ", 0) + string(rune('0'+i/100)) + string(rune('0'+i/10%10)) + string(rune('0'+i%10)))
}

func TestTruncate_WithinLimitUnchanged(t *testing.T) {
	s := lines(10)
	if got := tool.Truncate(s, len(s)); got != s {
		t.Fatalf("changed a string at its limit")
	}
	if got := tool.Truncate(s, 0); got != s {
		t.Fatalf("limit 0 must disable truncation")
	}
}

func TestTruncate_KeepsHeadAndTailWithMarker(t *testing.T) {
	s := lines(200) // 30 bytes per line, 6000 bytes
	got := tool.Truncate(s, 1000)

	if len(got) > 1000 {
		t.Fatalf("result is %d bytes, over the 1000 limit", len(got))
	}
	if !strings.HasPrefix(got, "xxxxxxxxxxxxxxxxxxxx line 001\n") {
		t.Fatalf("head lost: %q", got[:40])
	}
	if !strings.HasSuffix(got, "line 200\n") {
		t.Fatalf("tail lost: %q", got[len(got)-40:])
	}
	if !strings.Contains(got, "... [output truncated:") || !strings.Contains(got, "lines) omitted") {
		t.Fatalf("marker missing: %q", got)
	}
	// Cuts happen on line boundaries: no partial line before or after the
	// marker.
	before := got[:strings.Index(got, "\n... [output truncated")]
	if !strings.HasSuffix(before, "\n") {
		t.Fatalf("head does not end on a line boundary: %q", before[len(before)-20:])
	}
	after := got[strings.Index(got, "bytes] ...\n\n")+len("bytes] ...\n\n"):]
	if !strings.HasPrefix(after, "xxxxxxxxxxxxxxxxxxxx line ") {
		t.Fatalf("tail does not start on a line boundary: %q", after[:30])
	}
}

func TestTruncate_NeverSplitsUTF8(t *testing.T) {
	s := strings.Repeat("→", 2000) // 3-byte runes, no newlines
	got := tool.Truncate(s, 400)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated output is not valid UTF-8")
	}
	if !strings.HasPrefix(got, "→") || !strings.HasSuffix(got, "→") {
		t.Fatalf("head/tail lost")
	}
}

func TestBoundedBuffer_MatchesTruncate(t *testing.T) {
	s := lines(200)
	want := tool.Truncate(s, 1000)

	b := tool.NewBoundedBuffer(1000)
	// Write in awkward chunks that do not align with lines.
	for i := 0; i < len(s); i += 7 {
		end := min(i+7, len(s))
		if _, err := b.Write([]byte(s[i:end])); err != nil {
			t.Fatal(err)
		}
	}
	if b.Len() != len(s) {
		t.Fatalf("Len = %d, want %d", b.Len(), len(s))
	}
	got := b.String()
	if got != want {
		t.Fatalf("stream truncation differs from string truncation\n got: %q\nwant: %q", got, want)
	}
}

func TestBoundedBuffer_SmallOutputIntact(t *testing.T) {
	b := tool.NewBoundedBuffer(1000)
	_, _ = b.Write([]byte("hello "))
	_, _ = b.Write([]byte("world\n"))
	if got := b.String(); got != "hello world\n" {
		t.Fatalf("got %q", got)
	}
	unbounded := tool.NewBoundedBuffer(0)
	big := lines(500)
	_, _ = unbounded.Write([]byte(big))
	if unbounded.String() != big {
		t.Fatalf("unbounded buffer altered its input")
	}
}
