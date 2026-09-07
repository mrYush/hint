package project

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// InstructionNames are the file names read for a project's agent
// instructions, in precedence order: the first one present in a directory
// is used and the others there are not read, so a project that keeps both
// an AGENTS.md and a CLAUDE.md is not told the same thing twice.
var InstructionNames = []string{"HINT.md", "AGENTS.md", "CLAUDE.md"}

// DefaultInstructionBudget is the number of bytes all instruction files of
// a run may occupy together — roughly 8k tokens, a few percent of a 128k
// context window. The budget is shared rather than per file because it is
// the total that competes with the conversation for the window.
const DefaultInstructionBudget = 32 << 10

const (
	// instructionWindowShare is the fraction of a model's context window
	// the default budget may take: one sixteenth, which for a 128k window
	// is DefaultInstructionBudget itself.
	instructionWindowShare = 16
	// bytesPerToken is the estimate the budget arithmetic shares with the
	// agent's token estimator.
	bytesPerToken = 4
	// MinInstructionBudget is the floor of the scaled budget: room for a
	// heading list even on the smallest local model.
	MinInstructionBudget = 1 << 10
	// MaxInstructionFileSize bounds how much of one file is read at all;
	// beyond it the rest is not even outlined.
	MaxInstructionFileSize = 1 << 20
)

// InstructionBudgetFor returns the default budget for a model whose
// context window is window tokens: [DefaultInstructionBudget], scaled down
// so the preamble takes at most a sixteenth of the window at four bytes a
// token, never below [MinInstructionBudget]. An unknown window (0) keeps
// the default.
func InstructionBudgetFor(window int) int {
	if window <= 0 {
		return DefaultInstructionBudget
	}
	scaled := window * bytesPerToken / instructionWindowShare
	return max(min(DefaultInstructionBudget, scaled), MinInstructionBudget)
}

// Instruction is one instruction file as it goes into the prompt.
type Instruction struct {
	// Path is the file's absolute path.
	Path string
	// Content is what the prompt shows: the file, the file with some
	// sections reduced to their heading and first sentence (Outlined), a
	// model-written summary (Summarized), or a prefix (Truncated).
	Content string
	// Size is the file's size in bytes, whatever Content shows of it.
	Size int
	// Outlined reports that at least one section of Content ends in the
	// "[...]" marker: its text was left out and the instructions tool
	// returns it.
	Outlined bool
	// Summarized reports that Content is a summary written by a model,
	// not the user's own words.
	Summarized bool
	// Truncated reports that Content was cut at the budget.
	Truncated bool
}

// InstructionDirs returns the directories to search for instruction
// files, from root down to dir inclusive, so a nearer file lands later
// in the prompt and refines the outer one. With no root, or a dir that
// is not under it, only dir is searched.
func InstructionDirs(root, dir string) []string {
	dir = filepath.Clean(dir)
	if root == "" {
		return []string{dir}
	}
	root = filepath.Clean(root)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return []string{dir}
	}
	dirs := []string{root}
	if rel == "." {
		return dirs
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		dirs = append(dirs, cur)
	}
	return dirs
}

// InstructionPaths resolves which files a run reads: the global file when
// it is a regular file, then the first of names that exists in each of
// dirs, in order. A repository file that is the global file itself (hint
// run inside ~/.config/hint) is listed once.
func InstructionPaths(global string, dirs []string, names []string) []string {
	var paths []string
	if global != "" {
		if info, err := os.Stat(global); err == nil && info.Mode().IsRegular() {
			paths = append(paths, global)
		}
	}
	for _, dir := range dirs {
		p, ok := firstInstruction(dir, names)
		if !ok || (len(paths) > 0 && paths[0] == p) {
			continue
		}
		paths = append(paths, p)
	}
	return paths
}

// ReadInstructions reads the first file named in names that exists in
// each of dirs, in order, under one shared byte budget. It is
// [ReadInstructionFiles] over [InstructionPaths] with no global file and
// no summarizer.
func ReadInstructions(dirs []string, names []string, budget int) ([]Instruction, []string) {
	return ReadInstructionFiles(context.Background(), InstructionPaths("", dirs, names), budget, nil)
}

// ReadInstructionFiles reads paths, in order, and lays them out under one
// shared byte budget (see fit.go for the ladder). s, when not nil, is
// asked to summarize files that do not fit even as outlines. Files that
// are empty or whitespace cost nothing and are left out. Every outline,
// summary, cut, skip or read failure is returned as a warning rather than
// an error: instructions are a convenience, and a project with a broken
// one still deserves an answer.
func ReadInstructionFiles(ctx context.Context, paths []string, budget int, s Summarizer) ([]Instruction, []string) {
	var files []rawFile
	var warnings []string
	for _, p := range paths {
		content, more, err := readBounded(p, MaxInstructionFileSize)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if more {
			warnings = append(warnings, fmt.Sprintf("%s: larger than %d bytes; only that much of it is considered", p, MaxInstructionFileSize))
		}
		files = append(files, newRawFile(p, content))
	}
	out, fitWarnings := fitInstructions(ctx, files, budget, s)
	return out, append(warnings, fitWarnings...)
}

// firstInstruction returns the path of the first name that is a regular
// file in dir.
func firstInstruction(dir string, names []string) (string, bool) {
	for _, name := range names {
		p := filepath.Join(dir, name)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		return p, true
	}
	return "", false
}

// readBounded reads at most limit bytes of the file at p, reporting
// whether the file had more. The bound is on the read, not a check after
// it, so a multi-gigabyte file is never loaded to be discarded.
func readBounded(p string, limit int) (string, bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}
