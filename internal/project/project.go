package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Context is what the CLI knows about the working directory before the
// first request.
type Context struct {
	// Dir is the working directory, absolute.
	Dir string
	// GitRoot is the repository root containing Dir, or "" outside one.
	GitRoot string
	// Instructions are the instruction files found from GitRoot down to
	// Dir, outermost first.
	Instructions []Instruction
	// Overview is the rendered shape of Dir, or "" when the Overview
	// chosen renders nothing.
	Overview string
	// Warnings are non-fatal findings (a cut instruction file, git
	// refusing to answer) for the CLI to print once.
	Warnings []string
}

// loader holds the knobs of [Load].
type loader struct {
	git      string
	budget   int
	names    []string
	overview Overview
}

// Option configures [Load].
type Option func(*loader)

// WithGit sets the git executable used for ignore rules. Empty disables
// git and uses [Basic] everywhere; when the option is not given, git is
// looked up in PATH.
func WithGit(path string) Option {
	return func(l *loader) { l.git = path }
}

// WithInstructionBudget overrides [DefaultInstructionBudget].
func WithInstructionBudget(n int) Option {
	return func(l *loader) { l.budget = n }
}

// WithInstructionNames overrides [InstructionNames].
func WithInstructionNames(names ...string) Option {
	return func(l *loader) { l.names = names }
}

// WithOverview chooses how the directory's shape is rendered; the default
// is a [Tree] with its default limits.
func WithOverview(o Overview) Option {
	return func(l *loader) { l.overview = o }
}

// Load gathers the project context of dir: the repository root, the
// instruction files between it and dir, and an overview of dir filtered
// by the ignore rules. Only a directory that cannot be read at all is an
// error; everything else degrades to a warning.
func Load(ctx context.Context, dir string, opts ...Option) (*Context, error) {
	l := loader{git: lookupGit(), budget: DefaultInstructionBudget, names: InstructionNames, overview: Tree{}}
	for _, opt := range opts {
		opt(&l)
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	if info, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("project: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("project: %s is not a directory", abs)
	}

	pc := &Context{Dir: abs}
	pc.GitRoot, _ = FindGitRoot(abs)

	var warnings []string
	pc.Instructions, warnings = ReadInstructions(InstructionDirs(pc.GitRoot, abs), l.names, l.budget)
	pc.Warnings = append(pc.Warnings, warnings...)

	ig, err := NewRules(l.git).Ignorer(ctx, abs)
	if err != nil {
		pc.Warnings = append(pc.Warnings, fmt.Sprintf("%v; .gitignore not applied", err))
	}
	if pc.Overview, err = l.overview.Render(ctx, os.DirFS(abs), ig); err != nil {
		return nil, fmt.Errorf("project: overview of %s: %w", abs, err)
	}
	return pc, nil
}

// lookupGit is [exec.LookPath] for git, or "" when it is not installed;
// a variable so tests can pin the fallback.
var lookupGit = func() string {
	p, err := exec.LookPath("git")
	if err != nil {
		return ""
	}
	return p
}

// RenderInstructions formats instruction files for the prompt: each file
// in its own block labelled with its path, so the model can tell which
// directory a rule came from, and a note where one was cut. Instruction
// files are the user's own words to the agent, which is why they are the
// one file content that is placed in the system prompt rather than quoted
// as untrusted tool output.
func RenderInstructions(instructions []Instruction) string {
	if len(instructions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_instructions>\n")
	for _, in := range instructions {
		fmt.Fprintf(&b, "<file path=%q>\n", in.Path)
		b.WriteString(strings.TrimRight(in.Content, "\n"))
		if in.Truncated {
			fmt.Fprintf(&b, "\n[... truncated at the instruction budget; the full file is %s]", in.Path)
		}
		b.WriteString("\n</file>\n")
	}
	b.WriteString("</project_instructions>")
	return b.String()
}
