// Package diff computes line-oriented differences and renders them in the
// unified format, for the permission prompt that shows a user what a write
// is about to change (WP0.6) and for any later client that renders a diff.
//
// It is a small hand-written Myers implementation rather than a dependency:
// the project prefers the standard library, and the unified rendering — the
// part a prompt actually needs — is not in any of the candidate modules
// anyway.
package diff

import (
	"fmt"
	"strings"
)

// Op is the kind of one line-level edit.
type Op int8

const (
	// Equal is a line present in both inputs.
	Equal Op = iota
	// Delete is a line present only in the old input.
	Delete
	// Insert is a line present only in the new input.
	Insert
)

// Edit is one line of an edit script.
type Edit struct {
	Op   Op
	Text string
}

// maxEdits caps the edit distance the Myers search is willing to explore.
// The search stores one frontier per step, so memory grows with the square
// of the distance; past the cap the inputs are so different that a
// line-by-line diff would not be readable anyway, and Lines falls back to
// "everything removed, everything added".
const maxEdits = 2000

// Lines returns the shortest edit script turning old into new, as a
// sequence of Equal, Delete and Insert lines. It is the Myers O(ND)
// algorithm ("An O(ND) Difference Algorithm and Its Variations", 1986)
// after trimming the common prefix and suffix, which is where most of a
// source file's lines usually are.
func Lines(old, new []string) []Edit {
	// Common prefix and suffix never take part in the search; they are
	// re-attached as Equal edits around whatever the middle produced.
	prefix := 0
	for prefix < len(old) && prefix < len(new) && old[prefix] == new[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(new)-prefix &&
		old[len(old)-1-suffix] == new[len(new)-1-suffix] {
		suffix++
	}

	edits := make([]Edit, 0, len(old)+len(new))
	for _, l := range old[:prefix] {
		edits = append(edits, Edit{Equal, l})
	}
	edits = append(edits, myers(old[prefix:len(old)-suffix], new[prefix:len(new)-suffix])...)
	for _, l := range old[len(old)-suffix:] {
		edits = append(edits, Edit{Equal, l})
	}
	return edits
}

// myers runs the forward Myers search over a and b and backtracks the
// recorded frontiers into an edit script.
func myers(a, b []string) []Edit {
	n, m := len(a), len(b)
	switch {
	case n == 0 && m == 0:
		return nil
	case n == 0:
		return replaceAll(a, b)
	case m == 0:
		return replaceAll(a, b)
	}

	limit := min(n+m, maxEdits)
	// v[k] is the furthest x reached on diagonal k (= x - y); k ranges over
	// [-d, d] at step d, so it is stored with an offset. One copy of v is
	// kept per step for the backtrack.
	offset := limit
	v := make([]int, 2*limit+2)
	var trace [][]int
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			// Prefer moving down (an insert) at the left edge or when the
			// diagonal below reached further; otherwise move right (a
			// delete). This is the tie-break that makes deletes precede
			// inserts in the output, the order unified diffs are read in.
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				return backtrack(a, b, trace, offset)
			}
		}
	}
	return replaceAll(a, b)
}

// backtrack walks the recorded frontiers from the end back to the start,
// emitting the edit script in reverse, then flips it.
func backtrack(a, b []string, trace [][]int, offset int) []Edit {
	var rev []Edit
	x, y := len(a), len(b)
	for d := len(trace) - 1; d > 0; d-- {
		v := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := v[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			x--
			y--
			rev = append(rev, Edit{Equal, a[x]})
		}
		if x == prevX {
			y--
			rev = append(rev, Edit{Insert, b[y]})
		} else {
			x--
			rev = append(rev, Edit{Delete, a[x]})
		}
	}
	// Whatever is left at d == 0 is a common run at the very start.
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, Edit{Equal, a[x]})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

// replaceAll is the degenerate script: every old line deleted, every new
// line inserted.
func replaceAll(a, b []string) []Edit {
	edits := make([]Edit, 0, len(a)+len(b))
	for _, l := range a {
		edits = append(edits, Edit{Delete, l})
	}
	for _, l := range b {
		edits = append(edits, Edit{Insert, l})
	}
	return edits
}

// Stat counts the inserted and deleted lines of an edit script.
func Stat(edits []Edit) (added, removed int) {
	for _, e := range edits {
		switch e.Op {
		case Insert:
			added++
		case Delete:
			removed++
		case Equal:
		}
	}
	return added, removed
}

// contextLines is how many unchanged lines a hunk shows around a change —
// the same default as diff -u.
const contextLines = 3

// Unified renders the difference between old and new as a unified diff
// with three lines of context, headed by "--- a/name" and "+++ b/name".
// An empty old renders as a new file ("--- /dev/null"). It returns "" when
// the inputs are identical.
func Unified(name, old, new string) string {
	if old == new {
		return ""
	}
	edits := Lines(splitLines(old), splitLines(new))

	var b strings.Builder
	if old == "" {
		b.WriteString("--- /dev/null\n")
	} else {
		fmt.Fprintf(&b, "--- a/%s\n", name)
	}
	fmt.Fprintf(&b, "+++ b/%s\n", name)

	// Walk the script hunk by hunk: a hunk starts contextLines before the
	// first change and ends contextLines after the last one that is not
	// separated from the next by more than 2*contextLines equal lines.
	for i := 0; i < len(edits); {
		if edits[i].Op == Equal {
			i++
			continue
		}
		start := max(i-contextLines, 0)
		end := i
		for j := i; j < len(edits); j++ {
			if edits[j].Op != Equal {
				end = j + 1
				continue
			}
			if j-end >= 2*contextLines {
				break
			}
		}
		end = min(end+contextLines, len(edits))
		writeHunk(&b, edits, start, end)
		i = end
	}
	return b.String()
}

// writeHunk renders edits[start:end] as one @@ hunk.
func writeHunk(b *strings.Builder, edits []Edit, start, end int) {
	// Line numbers of the hunk's first line in each input: count how many
	// old and new lines the edits before start consumed.
	oldStart, newStart := 1, 1
	for _, e := range edits[:start] {
		switch e.Op {
		case Equal:
			oldStart++
			newStart++
		case Delete:
			oldStart++
		case Insert:
			newStart++
		}
	}
	oldCount, newCount := 0, 0
	for _, e := range edits[start:end] {
		switch e.Op {
		case Equal:
			oldCount++
			newCount++
		case Delete:
			oldCount++
		case Insert:
			newCount++
		}
	}
	fmt.Fprintf(b, "@@ -%s +%s @@\n", hunkRange(oldStart, oldCount), hunkRange(newStart, newCount))

	for _, e := range edits[start:end] {
		switch e.Op {
		case Equal:
			b.WriteByte(' ')
		case Delete:
			b.WriteByte('-')
		case Insert:
			b.WriteByte('+')
		}
		b.WriteString(strings.TrimSuffix(e.Text, "\n"))
		b.WriteByte('\n')
		if !strings.HasSuffix(e.Text, "\n") {
			b.WriteString("\\ No newline at end of file\n")
		}
	}
}

// hunkRange formats one side of a hunk header the way diff -u does: a
// count of 1 is implied, and an empty side reports the line before it.
func hunkRange(start, count int) string {
	switch count {
	case 1:
		return fmt.Sprint(start)
	case 0:
		return fmt.Sprintf("%d,0", start-1)
	default:
		return fmt.Sprintf("%d,%d", start, count)
	}
}

// splitLines splits s into lines that keep their "\n" terminator, so that
// a last line without one differs from the same text with it — the
// difference "\ No newline at end of file" reports. An empty s has no
// lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
