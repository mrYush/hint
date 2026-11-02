package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFirstLineOfCutsRuneBoundary(t *testing.T) {
	got := firstLineOf(strings.Repeat("Привет", 10), 11)
	want := "Приве..."
	if got != want {
		t.Fatalf("firstLineOf = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("firstLineOf returned invalid UTF-8: %q", got)
	}
}
