package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mrYush/hint/internal/diff"
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

// editPlan is a resolved, not yet applied edit: what the file holds now
// and what it would hold afterwards. Run and Describe both start from it,
// so the diff a user confirms and the write that follows cannot disagree
// about what the edit does (the file changing in between is the TOCTOU
// limit WP0.5 already accepts for tool.Root).
type editPlan struct {
	kind     editKind
	abs, rel string
	before   string
	after    string
	// at and n locate the replacement in after, for the result snippet.
	at, n int
	// note explains a non-exact match ("matched with CRLF line endings").
	note string
}

// editKind says how an edit applies: the usual replacement, or one of the
// two empty-old_str forms the Aider edit-block format defines.
type editKind int

const (
	editReplace editKind = iota
	// editCreate writes a file that does not exist yet.
	editCreate
	// editAppend adds new_str to the end of an existing file.
	editAppend
)

// plan validates args and computes the edit without touching the file.
// A failure of the action (bad path, old_str not found) comes back as an
// error result the model should see; a machinery failure is impossible
// here, so the returned result is always populated when ok is false.
func (t *editFile) plan(callID string, args json.RawMessage) (p editPlan, res agentapi.ToolResult, ok bool) {
	var in editFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return p, tool.InvalidArgs(callID, editFileName, err), false
	}
	if strings.TrimSpace(in.Path) == "" {
		return p, tool.InvalidArgs(callID, editFileName, errors.New("path is required")), false
	}
	if in.OldStr == in.NewStr {
		return p, tool.InvalidArgs(callID, editFileName, errors.New("old_str and new_str are identical; nothing to change")), false
	}

	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return p, agentapi.ErrorResult(callID, editFileName, err.Error()), false
	}
	p = editPlan{abs: abs, rel: t.root.Rel(abs)}

	data, _, err := readWhole(abs, maxEditSize)
	switch {
	case errors.Is(err, os.ErrNotExist) && in.OldStr == "":
		// The empty-old_str case the Aider edit-block format defines: a
		// missing file is created with the new text.
		p.kind, p.after, p.n = editCreate, in.NewStr, len(in.NewStr)
		return p, agentapi.ToolResult{}, true
	case err != nil:
		return p, agentapi.ErrorResult(callID, editFileName, pathError(t.root, abs, err)), false
	}
	p.before = string(data)

	if in.OldStr == "" {
		// ... and an existing one gets it appended.
		content := p.before
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		p.kind, p.after, p.at, p.n = editAppend, content+in.NewStr, len(content), len(in.NewStr)
		return p, agentapi.ToolResult{}, true
	}

	updated, at, note, err := replaceUnique(p.before, in.OldStr, in.NewStr)
	if err != nil {
		return p, agentapi.ErrorResult(callID, editFileName, fmt.Sprintf("%s: %v", p.rel, err)), false
	}
	if updated == p.before {
		return p, agentapi.ErrorResult(callID, editFileName, fmt.Sprintf("%s: the edit produced no change", p.rel)), false
	}
	p.after, p.at, p.n, p.note = updated, at, len(in.NewStr), note
	return p, agentapi.ToolResult{}, true
}

// Run implements agentapi.Tool.
func (t *editFile) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	p, res, ok := t.plan(callID, args)
	if !ok {
		return res, nil
	}
	if p.kind == editCreate {
		if err := os.MkdirAll(filepath.Dir(p.abs), 0o755); err != nil {
			return agentapi.ErrorResult(callID, editFileName, pathError(t.root, p.abs, err)), nil
		}
	}
	if err := writeAtomic(p.abs, []byte(p.after), 0o644); err != nil {
		return agentapi.ErrorResult(callID, editFileName, pathError(t.root, p.abs, err)), nil
	}

	switch p.kind {
	case editCreate:
		return agentapi.TextResult(callID, editFileName, fmt.Sprintf("Created %s (%d bytes)", p.rel, len(p.after))), nil
	case editAppend:
		return agentapi.TextResult(callID, editFileName,
			fmt.Sprintf("Appended %d bytes to %s. The end of the file now reads:\n%s", p.n, p.rel, snippet(p.after, p.at, p.n))), nil
	case editReplace:
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Edited %s", p.rel)
	if p.note != "" {
		out.WriteString(" (")
		out.WriteString(p.note)
		out.WriteString(")")
	}
	out.WriteString(". The changed region now reads:\n")
	out.WriteString(snippet(p.after, p.at, p.n))
	return agentapi.TextResult(callID, editFileName, out.String()), nil
}

// Describe implements tool.Describer: the unified diff the edit would
// produce. An edit that cannot be planned — old_str not found, ambiguous
// — reports the same message Run would, as the preview's error.
func (t *editFile) Describe(_ context.Context, args json.RawMessage) (tool.Description, error) {
	p, res, ok := t.plan("preview", args)
	if !ok {
		return tool.Description{}, errors.New(res.Text())
	}
	var summary string
	switch p.kind {
	case editCreate:
		summary = fmt.Sprintf("create %s (%d bytes)", p.rel, len(p.after))
	case editAppend, editReplace:
		added, removed := diff.Stat(diff.Lines(splitKeepNL(p.before), splitKeepNL(p.after)))
		summary = fmt.Sprintf("edit %s (+%d -%d lines)", p.rel, added, removed)
	}
	return tool.Description{
		Summary: summary,
		Detail:  diff.Unified(p.rel, p.before, p.after),
		Path:    p.rel,
	}, nil
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

// Touches implements tool.Toucher: the one file the call names.
func (t *editFile) Touches(args json.RawMessage) []string {
	var in editFileArgs
	if err := tool.DecodeArgs(args, &in); err != nil || in.Path == "" {
		return nil
	}
	abs, err := t.root.Resolve(in.Path)
	if err != nil {
		return nil
	}
	return []string{abs}
}
