package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const instructionsName = "instructions"

const instructionsDescription = `Read the project's instruction files (HINT.md, AGENTS.md, CLAUDE.md and the global one) in full, one section at a time, or find where a rule comes from.

The system prompt shows these files under a budget: a section ending in [...] is only its heading and first sentence, a file marked summary="true" is a model-written summary. This tool returns the original text. With no arguments it lists the files and their headings. With path it returns the whole file; with path and section, that section (by heading, case-insensitive) with its subsections. With quote it finds the file and line an instruction came from. It reads only the instruction files of this run, wherever they are, and nothing else.`

type instructionsArgs struct {
	Path    string `json:"path,omitempty" jsonschema_description:"An instruction file, as named in the system prompt (absolute, or just its file name when unambiguous)"`
	Section string `json:"section,omitempty" jsonschema_description:"A heading of that file; returns the section with its subsections"`
	Quote   string `json:"quote,omitempty" jsonschema_description:"A phrase from an instruction; returns which file and line it comes from"`
}

var instructionsSchema = tool.MustSchema(instructionsArgs{})

// instructions is the read-only tool over the run's discovered instruction
// files. It is confined by construction: the paths come from project.Load,
// so a parent directory's HINT.md — outside tool.Root, which read_file
// refuses — is readable, while an arbitrary path never is.
type instructions struct {
	paths []string
}

// NewInstructions returns the instructions tool over paths, the
// instruction files project.Load discovered for this run.
func NewInstructions(paths []string, opts ...Option) agentapi.Tool {
	o := buildOptions(opts)
	return tool.WithLimits(&instructions{paths: append([]string(nil), paths...)}, o.limits)
}

func (*instructions) Name() string                 { return instructionsName }
func (*instructions) Description() string          { return instructionsDescription }
func (*instructions) InputSchema() json.RawMessage { return instructionsSchema }
func (*instructions) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *instructions) Run(_ context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in instructionsArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, instructionsName, err), nil
	}
	in.Path, in.Section, in.Quote = strings.TrimSpace(in.Path), strings.TrimSpace(in.Section), strings.TrimSpace(in.Quote)

	switch {
	case in.Quote != "":
		if in.Path != "" || in.Section != "" {
			return tool.InvalidArgs(callID, instructionsName, errors.New("quote cannot be combined with path or section")), nil
		}
		return t.locate(callID, in.Quote)
	case in.Path != "":
		return t.read(callID, in.Path, in.Section)
	case in.Section != "":
		return tool.InvalidArgs(callID, instructionsName, errors.New("section needs a path")), nil
	default:
		return t.index(callID)
	}
}

// index lists every instruction file with its size and headings.
func (t *instructions) index(callID string) (agentapi.ToolResult, error) {
	if len(t.paths) == 0 {
		return agentapi.TextResult(callID, instructionsName, "This run has no instruction files."), nil
	}
	var b strings.Builder
	for _, p := range t.paths {
		content, err := os.ReadFile(p)
		if err != nil {
			fmt.Fprintf(&b, "%s: %v\n", p, err)
			continue
		}
		fmt.Fprintf(&b, "%s (%d bytes)\n", p, len(content))
		for _, s := range project.Sections(string(content)) {
			if s.Level > 0 {
				fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", s.Level), s.Title)
			}
		}
	}
	return agentapi.TextResult(callID, instructionsName, strings.TrimRight(b.String(), "\n")), nil
}

// read returns a file, or one section of it with its subsections.
func (t *instructions) read(callID, path, section string) (agentapi.ToolResult, error) {
	abs, err := t.resolve(path)
	if err != nil {
		return agentapi.ErrorResult(callID, instructionsName, err.Error()), nil
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return agentapi.ErrorResult(callID, instructionsName, err.Error()), nil
	}
	if section == "" {
		return agentapi.TextResult(callID, instructionsName, string(content)), nil
	}

	sections := project.Sections(string(content))
	want := normalizeHeading(section)
	var found []int
	for i, s := range sections {
		if s.Level > 0 && normalizeHeading(s.Title) == want {
			found = append(found, i)
		}
	}
	if len(found) == 0 {
		var titles []string
		for _, s := range sections {
			if s.Level > 0 {
				titles = append(titles, s.Title)
			}
		}
		return agentapi.ErrorResult(callID, instructionsName,
			fmt.Sprintf("%s has no section %q; its headings are: %s", abs, section, strings.Join(titles, "; "))), nil
	}
	text := project.Span(sections, found[0])
	if len(found) > 1 {
		var lines []string
		for _, i := range found[1:] {
			lines = append(lines, fmt.Sprint(sections[i].Line))
		}
		text += fmt.Sprintf("\n\n(the heading %q appears again at line %s)", sections[found[0]].Title, strings.Join(lines, ", "))
	}
	return agentapi.TextResult(callID, instructionsName, text), nil
}

// locate finds the file and line a quoted instruction comes from. The
// match ignores case, line breaks and runs of whitespace, so a rule the
// model repeats from memory still lands on its source.
func (t *instructions) locate(callID, quote string) (agentapi.ToolResult, error) {
	want := tokens(quote)
	if len(want) == 0 {
		return tool.InvalidArgs(callID, instructionsName, errors.New("quote is empty")), nil
	}
	const maxHits = 10
	var b strings.Builder
	hits := 0
	for _, p := range t.paths {
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		var words []struct {
			text string
			line int
		}
		for i, l := range lines {
			for _, w := range tokens(l) {
				words = append(words, struct {
					text string
					line int
				}{w, i + 1})
			}
		}
	search:
		for i := 0; i+len(want) <= len(words); i++ {
			for j, w := range want {
				if words[i+j].text != w {
					continue search
				}
			}
			first, last := words[i].line, words[i+len(want)-1].line
			fmt.Fprintf(&b, "%s:%d\n%s\n\n", p, first, strings.Join(lines[first-1:last], "\n"))
			if hits++; hits >= maxHits {
				break
			}
			i += len(want) - 1
		}
		if hits >= maxHits {
			break
		}
	}
	if hits == 0 {
		return agentapi.ErrorResult(callID, instructionsName,
			fmt.Sprintf("no instruction file of this run contains %q (files: %s)", quote, strings.Join(t.paths, ", "))), nil
	}
	if hits >= maxHits {
		b.WriteString("(more matches not shown)")
	}
	return agentapi.TextResult(callID, instructionsName, strings.TrimRight(b.String(), "\n")), nil
}

// resolve maps what the model named to one discovered file: the absolute
// path as shown in the prompt, or a suffix of it (its file name, or the
// path relative to a parent) when that names exactly one file.
func (t *instructions) resolve(path string) (string, error) {
	clean := filepath.Clean(path)
	var matches []string
	for _, p := range t.paths {
		if p == clean || strings.HasSuffix(p, string(filepath.Separator)+clean) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if len(t.paths) == 0 {
			return "", errors.New("this run has no instruction files")
		}
		return "", fmt.Errorf("%s is not an instruction file of this run; the files are: %s", path, strings.Join(t.paths, ", "))
	default:
		sort.Strings(matches)
		return "", fmt.Errorf("%s names several instruction files, use the full path: %s", path, strings.Join(matches, ", "))
	}
}

// normalizeHeading folds a heading, or what the model typed as one, for
// comparison: case, surrounding #s and the outline marker do not count.
func normalizeHeading(s string) string {
	s = strings.TrimSpace(strings.TrimRight(strings.TrimSpace(s), "#"))
	s = strings.TrimSpace(strings.TrimLeft(s, "#"))
	s = strings.TrimSuffix(s, "[...]")
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// tokens splits text into lower-cased words with surrounding punctuation
// removed, the unit a quote is matched in.
func tokens(text string) []string {
	var out []string
	for _, f := range strings.Fields(strings.ToLower(text)) {
		if w := strings.Trim(f, ".,;:!?\"'`()[]{}<>*_-"); w != "" {
			out = append(out, w)
		}
	}
	return out
}
