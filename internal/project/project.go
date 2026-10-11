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
	// InstructionPaths are every instruction file the run discovered —
	// the global one, then GitRoot down to Dir — whether or not it made
	// it into Instructions. The instructions tool reads these and nothing
	// else.
	InstructionPaths []string
	// Instructions are the instruction files as the prompt shows them,
	// outermost first, laid out under the budget: the HINT.md-style files
	// and the rule files that apply always. [Context.Layout] re-lays them
	// out with the rules a conversation has activated.
	Instructions []Instruction
	// Rules are the rule files found under the instruction directories
	// (see [RuleDirs]), conditional and unconditional alike, outer first.
	Rules []Rule
	// Overview is the rendered shape of Dir, or "" when the Overview
	// chosen renders nothing.
	Overview string
	// Warnings are non-fatal findings (a cut instruction file, git
	// refusing to answer) for the CLI to print once.
	Warnings []string

	// What Layout needs to lay the files out again.
	files      []string // the HINT.md-style files, outer first
	budget     int
	summarizer Summarizer
}

// Layout lays the run's instruction files out under the budget together
// with the rules a conversation has activated, in the order they were
// discovered: outer files first, a directory's own rules after its
// HINT.md. It is what the CLI calls before every request once a rule has
// been touched; Load calls it with no rules for Context.Instructions.
func (pc *Context) Layout(ctx context.Context, active []Rule) ([]Instruction, []string) {
	byPath := map[string]Rule{}
	for _, r := range active {
		byPath[r.Path] = r
	}
	// Discovery order, not activation order: an active rule sits with
	// the always-loaded rules of its own directory.
	paths := orderRules(pc.files, pc.Rules, func(r Rule) bool {
		_, ok := byPath[r.Path]
		return !r.Conditional() || ok
	})
	instructions, warnings := ReadInstructionFiles(ctx, paths, pc.budget, pc.summarizer)
	for i := range instructions {
		if r, ok := byPath[instructions[i].Path]; ok {
			instructions[i].Paths = r.Paths
		}
	}
	return instructions, warnings
}

// loader holds the knobs of [Load].
type loader struct {
	git        string
	budget     int // 0: the default, scaled to window
	window     int
	names      []string
	global     string
	overview   Overview
	summarizer Summarizer
}

// Option configures [Load].
type Option func(*loader)

// WithGlobalInstructions names the user's own instruction file — the CLI
// passes ~/.config/hint/HINT.md — read before any of the repository's
// files under the same budget, so it is the outermost layer that every
// project file refines. A path that is not a regular file is skipped
// silently; empty disables the global file.
func WithGlobalInstructions(path string) Option {
	return func(l *loader) { l.global = path }
}

// WithGit sets the git executable used for ignore rules. Empty disables
// git and uses [Basic] everywhere; when the option is not given, git is
// looked up in PATH.
func WithGit(path string) Option {
	return func(l *loader) { l.git = path }
}

// WithInstructionBudget fixes the instruction budget at n bytes, whatever
// the model's window. Without it the budget is [InstructionBudgetFor] the
// window given by [WithContextWindow].
func WithInstructionBudget(n int) Option {
	return func(l *loader) { l.budget = n }
}

// WithContextWindow tells Load the model's context window in tokens, so
// the default instruction budget can be scaled to what the model can
// afford. Zero means unknown and keeps [DefaultInstructionBudget].
func WithContextWindow(tokens int) Option {
	return func(l *loader) { l.window = tokens }
}

// WithSummarizer allows instruction files that do not fit the budget even
// as outlines to be summarized by s. Unset, such files are cut instead:
// a paraphrase of the user's own instructions is a choice, never a
// default.
func WithSummarizer(s Summarizer) Option {
	return func(l *loader) { l.summarizer = s }
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
	l := loader{git: lookupGit(), names: InstructionNames, overview: Tree{}}
	for _, opt := range opts {
		opt(&l)
	}
	budget := l.budget
	if budget <= 0 {
		budget = InstructionBudgetFor(l.window)
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

	dirs := InstructionDirs(pc.GitRoot, abs)
	files := InstructionPaths(l.global, dirs, l.names)
	var warnings []string
	pc.Rules, warnings = DiscoverRules(dirs)
	pc.Warnings = append(pc.Warnings, warnings...)
	// The files that load regardless of what the conversation touches:
	// the HINT.md-style files, each directory's unconditional rules right
	// after its own file so "nearest wins" keeps holding.
	pc.files, pc.budget, pc.summarizer = files, budget, l.summarizer
	pc.InstructionPaths = append(orderRules(files, pc.Rules, func(r Rule) bool { return !r.Conditional() }), conditionalPaths(pc.Rules)...)
	pc.Instructions, warnings = pc.Layout(ctx, nil)
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

// orderRules interleaves the instruction files with the rules include
// selects: a rule directory's rules follow the instruction file of the
// same directory, or the nearest outer one when that directory has none,
// so "nearest wins" holds for rules as it does for files.
func orderRules(files []string, rules []Rule, include func(Rule) bool) []string {
	var out []string
	pending := map[string][]string{} // rule root -> rule paths, discovery order
	var roots []string
	for _, r := range rules {
		if !include(r) {
			continue
		}
		if _, ok := pending[r.Root]; !ok {
			roots = append(roots, r.Root)
		}
		pending[r.Root] = append(pending[r.Root], r.Path)
	}
	for _, f := range files {
		out = append(out, f)
		dir := filepath.Dir(f)
		for _, root := range roots {
			if root == dir || (isUnder(root, dir) && !hasFileUnder(files, root)) {
				out = append(out, pending[root]...)
				delete(pending, root)
			}
		}
	}
	for _, root := range roots {
		out = append(out, pending[root]...)
	}
	return out
}

// isUnder reports whether dir is root or below it.
func isUnder(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// hasFileUnder reports whether one of files sits in root itself.
func hasFileUnder(files []string, root string) bool {
	for _, f := range files {
		if filepath.Dir(f) == root {
			return true
		}
	}
	return false
}

// conditionalPaths lists the files of the rules that wait for a touch.
func conditionalPaths(rules []Rule) []string {
	var out []string
	for _, r := range rules {
		if r.Conditional() {
			out = append(out, r.Path)
		}
	}
	return out
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
// directory a rule came from, with an attribute and a closing note for a
// file that is shown as less than itself — outlined, summarized or cut.
// Instruction files are the user's own words to the agent, which is why
// they are the one file content that is placed in the system prompt
// rather than quoted as untrusted tool output.
func RenderInstructions(instructions []Instruction) string {
	if len(instructions) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_instructions>\n")
	for _, in := range instructions {
		fmt.Fprintf(&b, "<file path=%q", in.Path)
		if len(in.Paths) > 0 {
			fmt.Fprintf(&b, " paths=%q", strings.Join(in.Paths, ", "))
		}
		if in.Outlined {
			b.WriteString(` outline="true"`)
		}
		if in.Summarized {
			b.WriteString(` summary="true"`)
		}
		b.WriteString(">\n")
		b.WriteString(strings.TrimRight(in.Content, "\n"))
		if in.Outlined {
			fmt.Fprintf(&b, "\n[... sections ending in [...] were left out at the instruction budget; read one with the instructions tool: path %s, section = its heading]", in.Path)
		}
		if in.Summarized {
			fmt.Fprintf(&b, "\n[... this is a model-written summary of the %d-byte file; the instructions tool returns the original %s]", in.Size, in.Path)
		}
		if in.Truncated {
			fmt.Fprintf(&b, "\n[... truncated at the instruction budget; the full file is %s]", in.Path)
		}
		b.WriteString("\n</file>\n")
	}
	b.WriteString("</project_instructions>")
	return b.String()
}
