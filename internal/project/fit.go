package project

import (
	"context"
	"fmt"
	"strings"
)

// The escalation ladder of WP0.12: when the instruction files of a run do
// not fit their budget, each file is shown in the least lossy form that
// makes the whole set fit — the full text, then the text with some
// sections reduced to their heading and first sentence, then only such
// an outline, then (opt-in) a model-written summary, then a cut. Nothing
// discovered is dropped outright while an outline of it can fit.

// outlineMarker closes an outline entry: the model reads it as "this
// section exists and the rest is fetched with the instructions tool".
const outlineMarker = " [...]"

// headlessReserve is the guaranteed share of a file with no headings, for
// which an outline is impossible: its first kilobyte, cut at a line.
const headlessReserve = 1 << 10

// rawFile is a discovered instruction file before fitting.
type rawFile struct {
	path       string
	content    string
	sections   []Section
	outline    string // every section as an outline entry
	summarized bool
	size       int // bytes of the original file, before any summary
}

func newRawFile(path, content string) rawFile {
	f := rawFile{path: path, content: content, size: len(content)}
	f.sections = Sections(content)
	f.outline = outlineOf(f.sections)
	return f
}

// headless reports a file without headings.
func (f rawFile) headless() bool {
	return len(f.sections) == 1 && f.sections[0].Level == 0
}

// reserve is the smallest form of the file worth showing: its outline, or
// for a headless file its first kilobyte. Every file is guaranteed this
// much before any file gets more, so a near file is never dropped for a
// far one.
func (f rawFile) reserve() int {
	if f.headless() {
		return min(len(f.content), headlessReserve)
	}
	return min(len(f.content), len(f.outline))
}

// outlineEntry renders a section as its heading and the first sentence
// of its body, closed by outlineMarker when the body has more.
func outlineEntry(s Section) string {
	var b strings.Builder
	if h := s.Heading(); h != "" {
		b.WriteString(h)
		b.WriteString("\n")
	}
	if strings.TrimSpace(s.Body) != "" {
		b.WriteString(FirstSentence(s.Body))
		b.WriteString(outlineMarker)
		b.WriteString("\n")
	}
	return b.String()
}

// outlineOf renders every section as an outline entry.
func outlineOf(sections []Section) string {
	entries := make([]string, len(sections))
	for i, s := range sections {
		entries[i] = outlineEntry(s)
	}
	return strings.Join(entries, "\n")
}

// fitted is one file laid out under its allowance.
type fitted struct {
	text      string
	outlined  int // sections reduced to an outline entry
	truncated bool
}

// layout renders f under limit bytes: the whole file when it fits, else
// as many sections in full as the limit allows, in document order, and
// the rest as outline entries. When even the outline does not fit — or
// the file has no headings to outline by — the text is cut at a line.
func layout(f rawFile, limit int) fitted {
	if len(f.content) <= limit {
		return fitted{text: f.content}
	}
	if f.headless() {
		return fitted{text: cutLines(f.content, limit), truncated: true}
	}

	n := len(f.sections)
	entries := make([]string, n)
	fulls := make([]string, n)
	total := n - 1 // the "\n" between sections
	for i, s := range f.sections {
		entries[i] = outlineEntry(s)
		fulls[i] = s.Text() + "\n"
		total += len(entries[i])
	}
	if total > limit {
		return fitted{text: cutLines(f.outline, limit), truncated: true}
	}

	// Greedy in document order: a section is shown in full when the
	// upgrade still leaves room for every later section's entry, which
	// the running total already accounts for.
	chosen := entries
	var outlined int
	for i := range f.sections {
		delta := len(fulls[i]) - len(entries[i])
		if total+delta <= limit {
			chosen[i] = fulls[i]
			total += delta
		} else if strings.TrimSpace(f.sections[i].Body) != "" {
			outlined++
		}
	}
	return fitted{text: strings.Join(chosen, "\n"), outlined: outlined}
}

// cutLines returns the longest prefix of s within limit bytes that ends
// at a line break. When not even the first line fits it returns "" — a
// fragment of a line is worth less than a skip warning — unless s is a
// single line, which is then cut at a word.
func cutLines(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	if i := strings.LastIndex(s[:limit], "\n"); i >= 0 {
		return s[:i+1]
	}
	if strings.Contains(s, "\n") {
		return ""
	}
	if i := strings.LastIndex(s[:limit], " "); i > 0 {
		return s[:i]
	}
	return s[:limit]
}

// fitInstructions lays files out under one shared byte budget and reports
// what each one lost as a warning.
//
// Every file is first reserved its smallest form (see rawFile.reserve);
// the remainder is then handed out root→leaf, so an outer file is shown
// in full before a nearer one is, but the nearer one is never skipped
// while its outline can fit. When even the reserves overflow the budget
// and a Summarizer is configured, files are summarized down to a
// proportional share of it first; without one, the outlines are cut
// root→leaf and whatever cannot fit at all is skipped, as before WP0.12.
func fitInstructions(ctx context.Context, files []rawFile, budget int, s Summarizer) ([]Instruction, []string) {
	var warnings []string
	total := 0
	for _, f := range files {
		total += f.reserve()
	}

	if total > budget && s != nil {
		files, warnings, total = summarizeOversized(ctx, files, budget, s)
	}

	out := make([]Instruction, 0, len(files))
	remaining := budget - total
	outlinesFit := remaining >= 0
	if !outlinesFit {
		remaining = budget
	}
	for _, f := range files {
		var lay fitted
		if outlinesFit {
			lay = layout(f, f.reserve()+remaining)
			remaining -= len(lay.text) - f.reserve()
		} else {
			lay = layout(f, remaining)
			remaining -= len(lay.text)
		}
		if lay.text == "" {
			warnings = append(warnings, fmt.Sprintf("%s: skipped, the %d-byte instruction budget is spent", f.path, budget))
			continue
		}
		in := Instruction{Path: f.path, Content: lay.text, Size: f.size, Summarized: f.summarized,
			Outlined: lay.outlined > 0, Truncated: lay.truncated}
		if in.Outlined {
			warnings = append(warnings, fmt.Sprintf("%s: %d of %d sections shown as headings only to fit the %d-byte instruction budget; the instructions tool reads them",
				f.path, lay.outlined, len(f.sections), budget))
		}
		if in.Truncated {
			warnings = append(warnings, fmt.Sprintf("%s: cut at the %d-byte instruction budget", f.path, budget))
		}
		out = append(out, in)
	}
	return out, warnings
}

// summarizeOversized asks s for a summary of every file that does not fit
// an equal split of budget, and returns the files with those summaries in
// place of their text. Files that fit the split keep their words; what
// they leave of the budget is split equally among the rest, so a short
// file next to a huge one is never paraphrased to make room. A summary
// that fails is a warning and leaves the file as it was; one that ignores
// its limit is cut. Reserves are recomputed for the caller.
func summarizeOversized(ctx context.Context, files []rawFile, budget int, s Summarizer) ([]rawFile, []string, int) {
	split := budget / max(len(files), 1)
	kept, oversized := 0, 0
	for _, f := range files {
		if len(f.content) <= split {
			kept += len(f.content)
		} else {
			oversized++
		}
	}
	share := (budget - kept) / max(oversized, 1)

	var warnings []string
	out := make([]rawFile, 0, len(files))
	newTotal := 0
	for _, f := range files {
		if len(f.content) > split {
			summary, err := s.Summarize(ctx, f.path, f.content, share)
			switch {
			case err != nil:
				warnings = append(warnings, fmt.Sprintf("%s: could not be summarized (%v); shown as an outline instead", f.path, err))
			case strings.TrimSpace(summary) == "":
				warnings = append(warnings, fmt.Sprintf("%s: the summarizer returned nothing; shown as an outline instead", f.path))
			default:
				summary = cutLines(strings.TrimSpace(summary)+"\n", share)
				warnings = append(warnings, fmt.Sprintf("%s: summarized by the model to fit the %d-byte instruction budget (%d -> %d bytes); the instructions tool reads the original",
					f.path, budget, len(f.content), len(summary)))
				g := newRawFile(f.path, summary)
				g.summarized, g.size = true, f.size
				f = g
			}
		}
		newTotal += f.reserve()
		out = append(out, f)
	}
	return out, warnings, newTotal
}
