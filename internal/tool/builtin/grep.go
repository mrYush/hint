package builtin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mrYush/hint/internal/glob"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/text"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	grepName = "grep"
	// maxGrepMatches caps the matching lines returned.
	maxGrepMatches = 200
	// maxGrepLine caps a displayed matching line.
	maxGrepLine = 500
)

// grepTruncatedNote tells the model that a search ended early because a
// line did not fit a reader's buffer.
const grepTruncatedNote = "(some matches may be missing: a line is longer than the read limit)"

// grepResult is what one search found, plus whether a line stopped the
// scan: a reader that runs out of buffer before the end of a line cannot
// know what follows it.
type grepResult struct {
	matches   []grepMatch
	truncated bool
}

const grepDescription = `Search file contents under the working directory for a regular expression. Returns "path:line: text" for each matching line.

Use it to find where something is defined or used before reading the file. The pattern is a regular expression (RE2 syntax: no backreferences or lookaround); set literal to true to search for the text as-is. Restrict the search with path (a file or directory) and include (a file-name glob such as "*.go"). Hidden files, dependency directories, binary files and, inside a git repository, anything .gitignore excludes are skipped. Results are cut at 200 matches.`

type grepArgs struct {
	Pattern string `json:"pattern" jsonschema_description:"Regular expression to search for"`
	Path    string `json:"path,omitempty" jsonschema_description:"File or directory to search, relative to the working directory (default: the working directory)"`
	Include string `json:"include,omitempty" jsonschema_description:"Only search files matching this glob: a file-name pattern such as \"*.go\" or \"*.{ts,tsx}\", or a path pattern relative to the working directory such as \"cmd/**/*.go\""`
	Literal bool   `json:"literal,omitempty" jsonschema_description:"Treat pattern as literal text rather than a regular expression"`
}

var grepSchema = tool.MustSchema(grepArgs{})

// grepMatch is one matching line.
type grepMatch struct {
	truncated bool
	path      string
	line      int
	text      string
}

type grepTool struct {
	root    tool.Root
	rg      string
	rules   project.Rules
	environ func() []string
}

func newGrep(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&grepTool{root: root, rg: o.rg, rules: o.rules(), environ: o.env}, o.limits)
}

func (*grepTool) Name() string                 { return grepName }
func (*grepTool) Description() string          { return grepDescription }
func (*grepTool) InputSchema() json.RawMessage { return grepSchema }
func (*grepTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *grepTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in grepArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, grepName, err), nil
	}
	if in.Pattern == "" {
		return tool.InvalidArgs(callID, grepName, errors.New("pattern is required")), nil
	}
	expr := in.Pattern
	if in.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	// Compile with Go's engine in every case so an invalid pattern gets one
	// message regardless of which engine will run it.
	re, err := regexp.Compile(expr)
	if err != nil {
		return tool.InvalidArgs(callID, grepName, fmt.Errorf("invalid regular expression: %w", err)), nil
	}
	var include *glob.Matcher
	if in.Include != "" {
		include, err = glob.Compile(in.Include)
		if err != nil {
			return tool.InvalidArgs(callID, grepName, fmt.Errorf("include: %w", err)), nil
		}
	}

	target, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, grepName, err.Error()), nil
	}
	if _, err := t.root.Stat(target); err != nil {
		return agentapi.ErrorResult(callID, grepName, pathError(t.root, target, err)), nil
	}

	var found grepResult
	if t.rg != "" {
		found, err = t.grepRipgrep(ctx, target, in)
	}
	if t.rg == "" || err != nil {
		// See listDir.Run for why a git failure is not reported here.
		ig, _ := t.rules.Ignorer(ctx, target)
		found, err = grepWalk(ctx, t.root, target, re, include, ig)
	}
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, grepName, pathError(t.root, target, err)), nil
	}

	matches := found.matches
	if len(matches) == 0 {
		text := "No matches found"
		if found.truncated {
			text += "\n" + grepTruncatedNote
		}
		return agentapi.TextResult(callID, grepName, text), nil
	}
	capped := len(matches) > maxGrepMatches
	if capped {
		matches = matches[:maxGrepMatches]
	}
	var b strings.Builder
	for _, m := range matches {
		line := m.text
		if m.truncated || len(line) > maxGrepLine {
			line = text.Cut(line, maxGrepLine) + "... [line truncated]"
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", t.root.Rel(m.path), m.line, line)
	}
	if capped {
		fmt.Fprintf(&b, "\n(showing the first %d matches; narrow the pattern, path or include to see others)", maxGrepMatches)
	}
	if found.truncated {
		b.WriteString("\n" + grepTruncatedNote)
	}
	return agentapi.TextResult(callID, grepName, strings.TrimRight(b.String(), "\n")), nil
}

// grepRipgrep runs ripgrep and parses its `path\0line:text` output. It
// stops reading after the cap so a pattern that matches everything does
// not have to finish scanning the tree.
//
// ripgrep runs with the working directory as its cwd and a target path
// relative to it, because `--glob` patterns containing a slash are anchored
// to rg's cwd: this is what makes `include: "cmd/**/*.go"` mean the same
// thing here as in the pure-Go walk, which matches it against the path
// relative to the root.
func (t *grepTool) grepRipgrep(ctx context.Context, target string, in grepArgs) (grepResult, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	args := []string{"--line-number", "--with-filename", "--no-heading", "--color", "never",
		"--no-messages", "--null", "--sort", "path"}
	if in.Literal {
		args = append(args, "--fixed-strings")
	}
	if in.Include != "" {
		args = append(args, "--glob", in.Include)
	}
	args = append(args, rgExcludeArgs(t.root, t.root.Dir())...)
	args = append(args, "--regexp", in.Pattern, "--", t.root.Rel(target))

	cmd := exec.CommandContext(ctx, t.rg, args...)
	cmd.Dir = t.root.Dir()
	cmd.Env = t.environ()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return grepResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return grepResult{}, err
	}

	var matches []grepMatch
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() && len(matches) <= maxGrepMatches {
		path, rest, ok := strings.Cut(sc.Text(), "\x00")
		if !ok {
			continue
		}
		lineStr, text, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		line, err := strconv.Atoi(lineStr)
		if err != nil {
			continue
		}
		matches = append(matches, boundedGrepMatch(filepath.Join(t.root.Dir(), path), line, text))
	}
	capped := len(matches) > maxGrepMatches
	scanErr := sc.Err()
	if capped || scanErr != nil {
		cancel() // enough, or the scan cannot continue
	}
	_, _ = io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		switch {
		case capped:
			// Killed by us after the cap; the matches gathered are valid.
		case scanErr != nil:
			// The scan stopped before the exit status could mean anything.
			return grepResult{}, fmt.Errorf("rg: %w", scanErr)
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return grepResult{}, nil // no matches
		case errors.As(err, &exit) && exit.ExitCode() == 2:
			return grepResult{}, fmt.Errorf("rg: %s", strings.TrimSpace(stderr.String()))
		default:
			return grepResult{}, fmt.Errorf("rg: %w", err)
		}
	}
	if scanErr != nil && !capped {
		// A line longer than the scanner's buffer, or a read error: the
		// caller retries with the pure-Go walk, which flags the truncation
		// instead of losing the tail of the output.
		return grepResult{}, fmt.Errorf("rg: %w", scanErr)
	}
	return grepResult{matches: matches}, nil
}

// grepWalk is the pure-Go search: walk target (or read it, if a file), skip
// what glob skips plus binary files, and test every line. include is
// matched against the path relative to the root, so a slash-free pattern
// selects by file name anywhere and a path pattern is anchored to the
// working directory — the same reading ripgrep gives it.
func grepWalk(ctx context.Context, root tool.Root, target string, re *regexp.Regexp, include *glob.Matcher, ig project.Ignorer) (grepResult, error) {
	var res grepResult
	visit := func(path string) error {
		if include != nil && !include.Match(filepath.ToSlash(root.Rel(path))) {
			return nil
		}
		found, truncated, err := grepFile(ctx, root, path, re, maxGrepMatches+1-len(res.matches))
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil //nolint:nilerr // unreadable or binary: skip the file, keep searching
		}
		res.truncated = res.truncated || truncated
		res.matches = append(res.matches, found...)
		if len(res.matches) > maxGrepMatches {
			return filepath.SkipAll
		}
		return nil
	}

	info, err := root.Stat(target)
	if err != nil {
		return grepResult{}, err
	}
	if !info.IsDir() {
		err := visit(target)
		if errors.Is(err, filepath.SkipAll) {
			err = nil
		}
		return res, err
	}
	err = root.WalkDirBatch(target, func(parent string, entries []fs.DirEntry) error {
		return project.PrepareIgnore(ig, relSlash(target, parent), entries)
	}, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == target {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel := relSlash(target, path)
		if d.IsDir() {
			if ig.Ignored(rel, true) || root.Denied(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if ig.Ignored(rel, false) || !d.Type().IsRegular() || root.Denied(path) {
			return nil
		}
		return visit(path)
	})
	if err == nil {
		err = project.IgnoreError(ig)
	}
	return res, err
}

// grepFile returns up to limit matching lines of one file, or an error for
// a binary or unreadable one. truncated is true when a line was longer
// than the scanner's buffer, so the file was not searched to its end.
func grepFile(ctx context.Context, root tool.Root, path string, re *regexp.Regexp, limit int) ([]grepMatch, bool, error) {
	f, err := root.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	if bin, err := isBinary(f); err != nil || bin {
		return nil, false, errors.New("binary")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, false, err
	}

	var out []grepMatch
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() && len(out) < limit {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line++
		if re.Match(sc.Bytes()) {
			out = append(out, boundedGrepMatch(path, line, strings.TrimRight(sc.Text(), "\r")))
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			return out, true, nil
		}
		return nil, false, err
	}
	return out, false, nil
}

func boundedGrepMatch(path string, line int, content string) grepMatch {
	return grepMatch{path: path, line: line, text: strings.Clone(text.Cut(content, maxGrepLine)), truncated: len(content) > maxGrepLine}
}
