package diff_test

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/diff"
)

func TestUnified_IdenticalIsEmpty(t *testing.T) {
	if got := diff.Unified("a.txt", "same\n", "same\n"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestUnified_NewFile(t *testing.T) {
	got := diff.Unified("new.go", "", "package main\n\nfunc main() {}\n")
	want := "--- /dev/null\n" +
		"+++ b/new.go\n" +
		"@@ -0,0 +1,3 @@\n" +
		"+package main\n" +
		"+\n" +
		"+func main() {}\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnified_EditWithContext(t *testing.T) {
	old := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	new := "1\n2\n3\n4\nfive\n6\n7\n8\n9\n10\n"
	got := diff.Unified("n.txt", old, new)
	want := "--- a/n.txt\n" +
		"+++ b/n.txt\n" +
		"@@ -2,7 +2,7 @@\n" +
		" 2\n 3\n 4\n-5\n+five\n 6\n 7\n 8\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnified_TwoHunks(t *testing.T) {
	var oldLines []string
	for i := 1; i <= 20; i++ {
		oldLines = append(oldLines, strings.Repeat("x", i%3+1))
	}
	newLines := append([]string(nil), oldLines...)
	newLines[1] = "changed-2"
	newLines[18] = "changed-19"
	got := diff.Unified("f", strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n")
	if n := strings.Count(got, "@@ -"); n != 2 {
		t.Fatalf("want 2 hunks, got %d:\n%s", n, got)
	}
	if !strings.HasPrefix(got, "--- a/f\n+++ b/f\n@@ -1,5 +1,5 @@\n") {
		t.Fatalf("first hunk header wrong:\n%s", got)
	}
	if !strings.Contains(got, "@@ -16,5 +16,5 @@\n") {
		t.Fatalf("second hunk header wrong:\n%s", got)
	}
}

func TestUnified_NoTrailingNewline(t *testing.T) {
	got := diff.Unified("f", "a\nb", "a\nb\n")
	want := "--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n a\n-b\n\\ No newline at end of file\n+b\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestUnified_DeleteOnly(t *testing.T) {
	got := diff.Unified("f", "a\nb\nc\n", "a\nc\n")
	want := "--- a/f\n+++ b/f\n@@ -1,3 +1,2 @@\n a\n-b\n c\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLines_PrefersDeleteBeforeInsert(t *testing.T) {
	edits := diff.Lines([]string{"a", "b", "c"}, []string{"a", "x", "c"})
	want := []diff.Edit{{diff.Equal, "a"}, {diff.Delete, "b"}, {diff.Insert, "x"}, {diff.Equal, "c"}}
	if len(edits) != len(want) {
		t.Fatalf("edits = %v", edits)
	}
	for i := range want {
		if edits[i] != want[i] {
			t.Fatalf("edits[%d] = %v, want %v", i, edits[i], want[i])
		}
	}
	if a, r := diff.Stat(edits); a != 1 || r != 1 {
		t.Fatalf("stat = +%d -%d", a, r)
	}
}

// apply replays an edit script over old and returns the new side; a
// script that does not reproduce new exactly is a bug in the search.
func apply(t *testing.T, old []string, edits []diff.Edit) []string {
	t.Helper()
	var out []string
	i := 0
	for _, e := range edits {
		switch e.Op {
		case diff.Equal:
			if i >= len(old) || old[i] != e.Text {
				t.Fatalf("equal edit %q does not match old[%d]", e.Text, i)
			}
			out = append(out, e.Text)
			i++
		case diff.Delete:
			if i >= len(old) || old[i] != e.Text {
				t.Fatalf("delete edit %q does not match old[%d]", e.Text, i)
			}
			i++
		case diff.Insert:
			out = append(out, e.Text)
		}
	}
	if i != len(old) {
		t.Fatalf("script consumed %d of %d old lines", i, len(old))
	}
	return out
}

func TestLines_RandomRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	alphabet := []string{"a", "b", "c", "d"}
	for iter := 0; iter < 500; iter++ {
		n, m := rng.Intn(12), rng.Intn(12)
		old := make([]string, n)
		new := make([]string, m)
		for i := range old {
			old[i] = alphabet[rng.Intn(len(alphabet))]
		}
		for i := range new {
			new[i] = alphabet[rng.Intn(len(alphabet))]
		}
		edits := diff.Lines(old, new)
		got := apply(t, old, edits)
		if strings.Join(got, ",") != strings.Join(new, ",") {
			t.Fatalf("round trip: old=%v new=%v edits=%v got=%v", old, new, edits, got)
		}
	}
}

func TestLines_LargeRewriteFallsBack(t *testing.T) {
	old := make([]string, 3000)
	new := make([]string, 3000)
	for i := range old {
		old[i] = "old" + strings.Repeat("!", i%7)
		new[i] = "new" + strings.Repeat("?", i%5)
	}
	edits := diff.Lines(old, new)
	got := apply(t, old, edits)
	if strings.Join(got, "\n") != strings.Join(new, "\n") {
		t.Fatal("fallback script does not reproduce new")
	}
	if a, r := diff.Stat(edits); a != 3000 || r != 3000 {
		t.Fatalf("stat = +%d -%d, want a full replacement", a, r)
	}
}
