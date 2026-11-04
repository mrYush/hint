package project

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mrYush/hint/internal/text"
)

// InstructionSource is an instruction document whose content is already in
// memory. Path is its stable display identity, not necessarily a disk path.
type InstructionSource struct {
	Path    string
	Content string
}

// ContextSnapshot is a consistent copy of the instruction state that can
// change when configured MCP resources are loaded.
type ContextSnapshot struct {
	InstructionPaths []string
	Instructions     []Instruction
	Rules            []Rule
	Warnings         []string
	Sources          []InstructionSource
	Revision         uint64
}

// Context is what the CLI knows about the working directory before the
// first request. Its instruction state remains safe to read while another
// session adds configured MCP resources.
type Context struct {
	// Dir is the working directory, absolute.
	Dir string
	// GitRoot is the repository root containing Dir, or "" outside one.
	GitRoot string
	// InstructionPaths are every instruction document the run discovered —
	// the global one, then GitRoot down to Dir, then configured sources —
	// whether or not it made it into Instructions. The instructions tool
	// reads these and nothing else.
	InstructionPaths []string
	// Instructions are the instruction documents as the prompt shows them,
	// outermost first, laid out under the budget: the HINT.md-style files,
	// applicable rules and configured sources. [Context.Layout] re-lays them
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

	mu sync.RWMutex
	// What Layout needs to lay the documents out again.
	files      []string // the HINT.md-style files, outer first
	sources    []InstructionSource
	budget     int
	summarizer Summarizer
	revision   uint64
}

// Snapshot returns a consistent copy of the mutable instruction state.
func (pc *Context) Snapshot() ContextSnapshot {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return ContextSnapshot{
		InstructionPaths: append([]string(nil), pc.InstructionPaths...),
		Instructions:     append([]Instruction(nil), pc.Instructions...),
		Rules:            append([]Rule(nil), pc.Rules...),
		Warnings:         append([]string(nil), pc.Warnings...),
		Sources:          append([]InstructionSource(nil), pc.sources...),
		Revision:         pc.revision,
	}
}

// InstructionSources returns the run's in-memory instruction documents.
func (pc *Context) InstructionSources() []InstructionSource {
	pc.mu.RLock()
	defer pc.mu.RUnlock()
	return append([]InstructionSource(nil), pc.sources...)
}

// AddInstructionSources adds configured in-memory documents after the file
// instructions, recomputes the initial layout and returns its new warnings.
func (pc *Context) AddInstructionSources(ctx context.Context, sources []InstructionSource) []string {
	if len(sources) == 0 {
		return nil
	}
	for {
		pc.mu.RLock()
		files := append([]string(nil), pc.files...)
		rules := append([]Rule(nil), pc.Rules...)
		combined := append(append([]InstructionSource(nil), pc.sources...), sources...)
		budget, summarizer, revision := pc.budget, pc.summarizer, pc.revision
		pc.mu.RUnlock()

		instructions, warnings := layoutInstructions(ctx, files, rules, combined, budget, summarizer, nil)
		pc.mu.Lock()
		if pc.revision != revision {
			pc.mu.Unlock()
			continue
		}
		pc.sources = combined
		for _, source := range sources {
			pc.InstructionPaths = append(pc.InstructionPaths, source.Path)
		}
		pc.Instructions = instructions
		pc.Warnings = append(pc.Warnings, warnings...)
		pc.revision++
		pc.mu.Unlock()
		return warnings
	}
}

// Layout lays the run's instruction documents out under the budget together
// with the rules a conversation has activated, in the order they were
// discovered: outer files first, a directory's own rules after its
// HINT.md, then configured sources. It is what the CLI calls before every
// request once a rule has been touched; Load calls it with no rules for
// Context.Instructions.
func (pc *Context) Layout(ctx context.Context, active []Rule) ([]Instruction, []string) {
	pc.mu.RLock()
	files := append([]string(nil), pc.files...)
	rules := append([]Rule(nil), pc.Rules...)
	sources := append([]InstructionSource(nil), pc.sources...)
	budget, summarizer := pc.budget, pc.summarizer
	pc.mu.RUnlock()
	return layoutInstructions(ctx, files, rules, sources, budget, summarizer, active)
}

func layoutInstructions(ctx context.Context, files []string, rules []Rule, sources []InstructionSource, budget int, summarizer Summarizer, active []Rule) ([]Instruction, []string) {
	byPath := map[string]Rule{}
	for _, r := range active {
		byPath[r.Path] = r
	}
	// Discovery order, not activation order: an active rule sits with
	// the always-loaded rules of its own directory.
	paths := orderRules(files, rules, func(r Rule) bool {
		_, ok := byPath[r.Path]
		return !r.Conditional() || ok
	})
	raw, warnings := readInstructionFiles(ctx, paths)
	for _, source := range sources {
		content := source.Content
		if len(content) > MaxInstructionFileSize {
			content = text.Cut(content, MaxInstructionFileSize)
			warnings = append(warnings, fmt.Sprintf("%s: larger than %d bytes; only that much of it is considered", source.Path, MaxInstructionFileSize))
		}
		if strings.TrimSpace(content) != "" {
			raw = append(raw, newRawFile(source.Path, content))
		}
	}
	instructions, fitWarnings := fitInstructions(ctx, raw, budget, summarizer)
	warnings = append(warnings, fitWarnings...)
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
	sources    []InstructionSource
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

// WithInstructionSources adds in-memory instruction documents after the
// discovered files, under the same budget and outline rules.
func WithInstructionSources(sources ...InstructionSource) Option {
	return func(l *loader) { l.sources = append([]InstructionSource(nil), sources...) }
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
	pc.files, pc.sources, pc.budget, pc.summarizer = files, l.sources, budget, l.summarizer
	pc.InstructionPaths = append(orderRules(files, pc.Rules, func(r Rule) bool { return !r.Conditional() }), conditionalPaths(pc.Rules)...)
	for _, source := range pc.sources {
		pc.InstructionPaths = append(pc.InstructionPaths, source.Path)
	}
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

// RenderInstructions formats instruction documents for the prompt: each
// document in its own block labelled with its identity, so the model can tell
// where a rule came from, with an attribute and a closing note for a document
// shown as less than itself — outlined, summarized or cut.
// File instructions are the user's own words; an MCP resource enters here only
// when the user names it in the configuration. Model-requested resources stay
// quoted as untrusted tool output instead.
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
