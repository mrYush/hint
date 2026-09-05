package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const editFileName = "edit_file"

const editFileDescription = `Replace one unique occurrence of old_str in a file with new_str.

This is a search/replace edit: old_str must match the file exactly once, including whitespace and indentation, so include enough surrounding lines to make it unique. Read the file first and copy the text verbatim. If the only difference is indentation, the edit is still applied and new_str is re-indented to match; any other mismatch fails with a hint showing the closest lines. Set old_str to an empty string to create a new file with new_str, or to append new_str to an existing one. Use write_file to rewrite a file wholesale.`

type editFileArgs struct {
	Path   string `json:"path" jsonschema_description:"Path of the file to edit, relative to the working directory"`
	OldStr string `json:"old_str" jsonschema_description:"Exact text to find; must occur exactly once. Empty to create or append"`
	NewStr string `json:"new_str" jsonschema_description:"Text that replaces old_str"`
}

var editFileSchema = tool.MustSchema(editFileArgs{})

type editFile struct {
	root tool.Root
}

func newEditFile(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&editFile{root: root}, o.limits)
}

func (*editFile) Name() string                 { return editFileName }
func (*editFile) Description() string          { return editFileDescription }
func (*editFile) InputSchema() json.RawMessage { return editFileSchema }
func (*editFile) Class() agentapi.ActionClass  { return agentapi.ClassWrite }

// Run implements agentapi.Tool.
func (t *editFile) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in editFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, editFileName, err), nil
	}
	if strings.TrimSpace(in.Path) == "" {
		return tool.InvalidArgs(callID, editFileName, errors.New("path is required")), nil
	}
	if in.OldStr == in.NewStr {
		return tool.InvalidArgs(callID, editFileName, errors.New("old_str and new_str are identical; nothing to change")), nil
	}

	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, editFileName, err.Error()), nil
	}
	rel := t.root.Rel(abs)

	if in.OldStr == "" {
		return t.createOrAppend(callID, abs, rel, in.NewStr)
	}

	data, _, err := readWhole(abs, maxEditSize)
	if err != nil {
		return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
	}
	content := string(data)

	updated, at, note, err := replaceUnique(content, in.OldStr, in.NewStr)
	if err != nil {
		return agentapi.ErrorResult(callID, editFileName, fmt.Sprintf("%s: %v", rel, err)), nil
	}
	if updated == content {
		return agentapi.ErrorResult(callID, editFileName, fmt.Sprintf("%s: the edit produced no change", rel)), nil
	}
	if err := writeAtomic(abs, []byte(updated), 0o644); err != nil {
		return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
	}

	var out strings.Builder
	fmt.Fprintf(&out, "Edited %s", rel)
	if note != "" {
		out.WriteString(" (")
		out.WriteString(note)
		out.WriteString(")")
	}
	out.WriteString(". The changed region now reads:\n")
	out.WriteString(snippet(updated, at, len(in.NewStr)))
	return agentapi.TextResult(callID, editFileName, out.String()), nil
}

// createOrAppend implements the empty-old_str case the Aider edit-block
// format defines: a missing file is created with the new text, an existing
// one gets it appended.
func (t *editFile) createOrAppend(callID, abs, rel, text string) (agentapi.ToolResult, error) {
	data, _, err := readWhole(abs, maxEditSize)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
		}
		if err := writeAtomic(abs, []byte(text), 0o644); err != nil {
			return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
		}
		return agentapi.TextResult(callID, editFileName, fmt.Sprintf("Created %s (%d bytes)", rel, len(text))), nil
	case err != nil:
		return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	updated := content + text
	if err := writeAtomic(abs, []byte(updated), 0o644); err != nil {
		return agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), nil
	}
	return agentapi.TextResult(callID, editFileName,
		fmt.Sprintf("Appended %d bytes to %s. The end of the file now reads:\n%s", len(text), rel, snippet(updated, len(content), len(text)))), nil
}

// replaceUnique finds old in content exactly once and replaces it with new.
// It returns the new content, the byte offset the replacement starts at, and
// a note when the match was not byte-exact.
//
// Strategies, in order, stopping at the first that yields a match:
//
//  1. Exact substring match — the contract the tool advertises.
//  2. The same with old/new converted to CRLF when the file uses CRLF line
//     endings, so a model that read a Windows file never has to reproduce
//     the carriage returns.
//  3. Whole-line match ignoring a uniform difference in leading whitespace,
//     ported from Aider's replace_part_with_missing_leading_whitespace:
//     models often drop or shrink the indentation of a block consistently
//     across old and new. The replacement is re-indented by the same
//     amount.
//
// Any strategy that finds more than one candidate fails as ambiguous rather
// than guessing: silently editing the wrong site is the one outcome worse
// than a failed call.
func replaceUnique(content, old, new string) (updated string, at int, note string, err error) {
	if idx, n := findAll(content, old); n == 1 {
		return content[:idx] + new + content[idx+len(old):], idx, "", nil
	} else if n > 1 {
		return "", 0, "", fmt.Errorf("old_str occurs %d times (at lines %s); include more context so it matches exactly once",
			n, lineNumbers(content, old, 5))
	}

	if strings.Contains(content, "\r\n") && !strings.Contains(old, "\r") {
		crlfOld := strings.ReplaceAll(old, "\n", "\r\n")
		crlfNew := strings.ReplaceAll(new, "\n", "\r\n")
		if idx, n := findAll(content, crlfOld); n == 1 {
			return content[:idx] + crlfNew + content[idx+len(crlfOld):], idx, "matched with CRLF line endings", nil
		} else if n > 1 {
			return "", 0, "", fmt.Errorf("old_str occurs %d times; include more context so it matches exactly once", n)
		}
	}

	if updated, at, ok, ambiguous := replaceIgnoringIndent(content, old, new); ambiguous {
		return "", 0, "", errors.New("old_str matches several places once indentation is ignored; include more context so it matches exactly once")
	} else if ok {
		return updated, at, "matched ignoring a uniform indentation difference; new_str was re-indented to match", nil
	}

	msg := "old_str not found"
	if hint := closestLines(content, old); hint != "" {
		msg += ". " + hint
	}
	return "", 0, "", errors.New(msg)
}

// findAll returns the first offset of old in content and the number of
// non-overlapping occurrences.
func findAll(content, old string) (first, count int) {
	first = strings.Index(content, old)
	if first < 0 {
		return -1, 0
	}
	return first, strings.Count(content, old)
}

// lineNumbers lists the 1-based line of the first max occurrences of old.
func lineNumbers(content, old string, max int) string {
	var nums []string
	from := 0
	for len(nums) < max {
		idx := strings.Index(content[from:], old)
		if idx < 0 {
			break
		}
		abs := from + idx
		nums = append(nums, fmt.Sprint(1+strings.Count(content[:abs], "\n")))
		from = abs + len(old)
	}
	s := strings.Join(nums, ", ")
	if strings.Count(content, old) > max {
		s += ", ..."
	}
	return s
}

// replaceIgnoringIndent is the whole-line, indentation-tolerant strategy.
// ok reports a unique match was applied; ambiguous reports several.
func replaceIgnoringIndent(content, old, new string) (updated string, at int, ok, ambiguous bool) {
	oldLines := splitLines(old)
	newLines := splitLines(new)
	if len(oldLines) == 0 || allBlank(oldLines) {
		return "", 0, false, false
	}
	// Outdent old and new by the indentation they share, so that the file's
	// indentation is the only thing left to discover.
	if common := commonIndent(append(append([]string{}, oldLines...), newLines...)); common > 0 {
		oldLines = outdent(oldLines, common)
		newLines = outdent(newLines, common)
	}

	contentLines := splitLines(content)
	type hit struct {
		start  int
		indent string
	}
	var hits []hit
	for i := 0; i+len(oldLines) <= len(contentLines); i++ {
		if indent, match := matchesButIndent(contentLines[i:i+len(oldLines)], oldLines); match {
			hits = append(hits, hit{i, indent})
		}
	}
	switch len(hits) {
	case 0:
		return "", 0, false, false
	case 1:
	default:
		return "", 0, false, true
	}

	h := hits[0]
	replacement := make([]string, len(newLines))
	for i, l := range newLines {
		if strings.TrimSpace(l) == "" {
			replacement[i] = l
		} else {
			replacement[i] = h.indent + l
		}
	}
	before := strings.Join(contentLines[:h.start], "")
	after := strings.Join(contentLines[h.start+len(oldLines):], "")
	return before + strings.Join(replacement, "") + after, len(before), true, false
}

// matchesButIndent reports whether window equals pattern line for line
// once leading whitespace is ignored, with every non-blank line of the
// window carrying the same extra indentation, which it returns.
func matchesButIndent(window, pattern []string) (indent string, ok bool) {
	found := false
	for i := range pattern {
		w, p := window[i], pattern[i]
		wt, pt := strings.TrimLeft(w, " \t"), strings.TrimLeft(p, " \t")
		if wt != pt {
			return "", false
		}
		if strings.TrimSpace(p) == "" {
			continue
		}
		extra := w[:len(w)-len(wt)]
		if len(extra) < len(p)-len(pt) {
			return "", false
		}
		extra = extra[:len(extra)-(len(p)-len(pt))]
		if found && extra != indent {
			return "", false
		}
		indent, found = extra, true
	}
	return indent, true
}

// splitLines splits s into lines that keep their terminator, adding one to
// the last line so that the whole-line strategies compare like with like.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	lines := strings.SplitAfter(s, "\n")
	return lines[:len(lines)-1]
}

func allBlank(lines []string) bool {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

// commonIndent returns the smallest leading-whitespace length among the
// non-blank lines.
func commonIndent(lines []string) int {
	common := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " \t"))
		if common < 0 || n < common {
			common = n
		}
	}
	return max(common, 0)
}

func outdent(lines []string, n int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if strings.TrimSpace(l) == "" || len(l) < n {
			out[i] = l
		} else {
			out[i] = l[n:]
		}
	}
	return out
}

// closestLines finds the window of the file that best matches old once
// leading and trailing whitespace is ignored on each line, and renders it
// with whitespace made visible, so the model can see what it got wrong.
// It returns "" when fewer than half the lines agree.
func closestLines(content, old string) string {
	pattern := splitLines(old)
	for len(pattern) > 0 && strings.TrimSpace(pattern[0]) == "" {
		pattern = pattern[1:]
	}
	for len(pattern) > 0 && strings.TrimSpace(pattern[len(pattern)-1]) == "" {
		pattern = pattern[:len(pattern)-1]
	}
	lines := splitLines(content)
	if len(pattern) == 0 || len(lines) < len(pattern) {
		return ""
	}

	bestScore, bestStart := 0, -1
	for start := 0; start+len(pattern) <= len(lines); start++ {
		score := 0
		for j := range pattern {
			if strings.TrimSpace(lines[start+j]) == strings.TrimSpace(pattern[j]) {
				score++
			}
		}
		if score > bestScore {
			bestScore, bestStart = score, start
		}
	}
	if bestStart < 0 || bestScore < (len(pattern)+1)/2 {
		return ""
	}

	end := bestStart + len(pattern) - 1
	var b strings.Builder
	fmt.Fprintf(&b, "Closest match at lines %d-%d (%d of %d lines agree ignoring whitespace):\n",
		bestStart+1, end+1, bestScore, len(pattern))
	for i := max(bestStart-1, 0); i <= min(end+1, len(lines)-1); i++ {
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, visibleWhitespace(strings.TrimRight(lines[i], "\r\n")))
	}
	b.WriteString("Use the exact text shown (→ = tab, · = leading space)")
	return b.String()
}

// visibleWhitespace marks tabs and leading spaces so an indentation error
// is visible in a proportional font.
func visibleWhitespace(s string) string {
	trimmed := strings.TrimLeft(s, " \t")
	lead := strings.NewReplacer("\t", "→", " ", "·").Replace(s[:len(s)-len(trimmed)])
	return lead + strings.ReplaceAll(trimmed, "\t", "→")
}

// snippet renders the lines around the byte range [at, at+n) of content,
// numbered, with two lines of context each side, capped so that a large
// replacement does not echo itself back in full.
func snippet(content string, at, n int) string {
	const context, maxLines = 2, 40
	startLine := strings.Count(content[:at], "\n")
	endLine := startLine + strings.Count(content[at:min(at+n, len(content))], "\n")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")

	from := max(startLine-context, 0)
	to := min(endLine+context, len(lines)-1)
	var b strings.Builder
	shown := 0
	for i := from; i <= to; i++ {
		if shown == maxLines {
			fmt.Fprintf(&b, "   ...\t(%d more lines)\n", to-i+1)
			break
		}
		fmt.Fprintf(&b, "%6d\t%s\n", i+1, strings.TrimSuffix(lines[i], "\r"))
		shown++
	}
	return b.String()
}
