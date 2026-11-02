package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFirstSentenceCutsRuneBoundary(t *testing.T) {
	got := FirstSentence(strings.Repeat("Привет ", 100))
	if !utf8.ValidString(got) {
		t.Fatalf("FirstSentence returned invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("FirstSentence returned a replacement rune: %q", got)
	}
}

func TestCutLinesCutsRuneBoundary(t *testing.T) {
	got := cutLines(strings.Repeat("Привет", 10), 11)
	if !utf8.ValidString(got) {
		t.Fatalf("cutLines returned invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("cutLines returned a replacement rune: %q", got)
	}
	if len(got) > 11 {
		t.Fatalf("cutLines returned %d bytes, want at most 11", len(got))
	}
}

func TestReadBoundedCutsRuneBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "HINT.md")
	want := strings.Repeat("Привет", 10)
	if err := os.WriteFile(p, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	got, truncated, err := readBounded(p, 11)
	if err != nil {
		t.Fatalf("readBounded: %v", err)
	}
	if !truncated {
		t.Fatal("readBounded did not report the cut")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("readBounded returned invalid UTF-8: %q", got)
	}
	if strings.ContainsRune(got, utf8.RuneError) {
		t.Fatalf("readBounded returned a replacement rune: %q", got)
	}
	if len(got) > 11 {
		t.Fatalf("readBounded returned %d bytes, want at most 11", len(got))
	}
}
