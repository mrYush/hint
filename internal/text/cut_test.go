package text_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/text"
)

func TestCut(t *testing.T) {
	cases := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"empty", "", 5, ""},
		{"zero", "abc", 0, ""},
		{"negative", "abc", -1, ""},
		{"inside ascii", "abc", 2, "ab"},
		{"at end", "abc", 3, "abc"},
		{"beyond end", "abc", 4, "abc"},
		{"cyrillic inside first rune", "Привет", 1, ""},
		{"cyrillic after first rune", "Привет", 2, "П"},
		{"cyrillic inside second rune", "Привет", 3, "П"},
		{"arrow inside first rune", "→→", 1, ""},
		{"arrow after first rune", "→→", 3, "→"},
		{"emoji inside", "🙂x", 3, ""},
		{"emoji after", "🙂x", 4, "🙂"},
		{"combining base only", "e\u0301x", 1, "e"},
		{"combining inside mark", "e\u0301x", 2, "e"},
		{"combining after mark", "e\u0301x", 3, "e\u0301"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := text.Cut(c.s, c.n)
			if got != c.want {
				t.Fatalf("Cut(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Cut(%q, %d) = %q is not valid UTF-8", c.s, c.n, got)
			}
		})
	}
}

func TestCutNeverSplitsRunes(t *testing.T) {
	s := strings.Repeat("Привет→🙂", 50)
	for n := 0; n <= len(s); n++ {
		got := text.Cut(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("Cut at %d produced invalid UTF-8: %q", n, got)
		}
		if !strings.HasPrefix(s, got) {
			t.Fatalf("Cut at %d is not a prefix: %q", n, got)
		}
	}
}
