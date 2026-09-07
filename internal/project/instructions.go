package project

import (
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

// Instruction is one instruction file as it goes into the prompt.
type Instruction struct {
	// Path is the file's absolute path.
	Path string
	// Content is the file's text, cut at the budget when Truncated.
	Content string
	// Truncated reports that Content is a prefix of the file.
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
// [ReadInstructionFiles] over [InstructionPaths] with no global file.
func ReadInstructions(dirs []string, names []string, budget int) ([]Instruction, []string) {
	return ReadInstructionFiles(InstructionPaths("", dirs, names), budget)
}

// ReadInstructionFiles reads paths, in order, under one shared byte
// budget. A file that would overflow the budget is cut at it and marked
// Truncated; once the budget is spent, later files are skipped. Files that
// are empty or whitespace cost nothing and are left out. Every skip, cut
// or read failure is returned as a warning rather than an error:
// instructions are a convenience, and a project with a broken one still
// deserves an answer.
func ReadInstructionFiles(paths []string, budget int) ([]Instruction, []string) {
	var out []Instruction
	var warnings []string
	remaining := budget
	for _, p := range paths {
		if remaining <= 0 {
			warnings = append(warnings, fmt.Sprintf("%s: skipped, the %d-byte instruction budget is spent", p, budget))
			continue
		}
		content, truncated, err := readBounded(p, remaining)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, err))
			continue
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if truncated {
			warnings = append(warnings, fmt.Sprintf("%s: cut at the %d-byte instruction budget", p, budget))
		}
		remaining -= len(content)
		out = append(out, Instruction{Path: p, Content: content, Truncated: truncated})
	}
	return out, warnings
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
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return "", false, err
	}
	if len(data) > limit {
		return string(data[:limit]), true, nil
	}
	return string(data), false, nil
}
