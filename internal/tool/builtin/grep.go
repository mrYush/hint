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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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

const grepDescription = `Search file contents under the working directory for a regular expression. Returns "path:line: text" for each matching line.

Use it to find where something is defined or used before reading the file. The pattern is a regular expression (RE2 syntax: no backreferences or lookaround); set literal to true to search for the text as-is. Restrict the search with path (a file or directory) and include (a file-name glob such as "*.go"). Hidden files, dependency directories and binary files are skipped. Results are cut at 200 matches.`

type grepArgs struct {
	Pattern string `json:"pattern" jsonschema_description:"Regular expression to search for"`
	Path    string `json:"path,omitempty" jsonschema_description:"File or directory to search, relative to the working directory (default: the working directory)"`
	Include string `json:"include,omitempty" jsonschema_description:"Only search files matching this glob: a file-name pattern such as \"*.go\" or \"*.{ts,tsx}\", or a path pattern relative to the working directory such as \"cmd/**/*.go\""`
	Literal bool   `json:"literal,omitempty" jsonschema_description:"Treat pattern as literal text rather than a regular expression"`
}

var grepSchema = tool.MustSchema(grepArgs{})

// grepMatch is one matching line.
type grepMatch struct {
	path string
	line int
	text string
}

type grepTool struct {
	root tool.Root
	rg   string
}

func newGrep(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&grepTool{root: root, rg: o.rg}, o.limits)
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
	var include *globMatcher
	if in.Include != "" {
		include, err = compileGlob(in.Include)
		if err != nil {
			return tool.InvalidArgs(callID, grepName, fmt.Errorf("include: %w", err)), nil
		}
	}

	target, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, grepName, err.Error()), nil
	}
	if _, err := os.Stat(target); err != nil {
		return agentapi.ErrorResult(callID, grepName, pathError(t.root, target, err)), nil
	}

	var matches []grepMatch
	if t.rg != "" {
		matches, err = t.grepRipgrep(ctx, target, in)
	}
	if t.rg == "" || err != nil {
		matches, err = grepWalk(ctx, t.root, target, re, include)
	}
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, grepName, pathError(t.root, target, err)), nil
	}

	if len(matches) == 0 {
		return agentapi.TextResult(callID, grepName, "No matches found"), nil
	}
	truncated := len(matches) > maxGrepMatches
	if truncated {
		matches = matches[:maxGrepMatches]
	}
	var b strings.Builder
	for _, m := range matches {
		text := m.text
		if len(text) > maxGrepLine {
			text = text[:maxGrepLine] + "... [line truncated]"
		}
		fmt.Fprintf(&b, "%s:%d: %s\n", t.root.Rel(m.path), m.line, text)
	}
	if truncated {
		fmt.Fprintf(&b, "\n(showing the first %d matches; narrow the pattern, path or include to see others)", maxGrepMatches)
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
func (t *grepTool) grepRipgrep(ctx context.Context, target string, in grepArgs) ([]grepMatch, error) {
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
	args = append(args, rgExcludeArgs()...)
	args = append(args, "--regexp", in.Pattern, "--", t.root.Rel(target))

	cmd := exec.CommandContext(ctx, t.rg, args...)
	cmd.Dir = t.root.Dir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
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
		matches = append(matches, grepMatch{path: filepath.Join(t.root.Dir(), path), line: line, text: text})
	}
	if len(matches) > maxGrepMatches {
		cancel() // enough; stop rg early
	}
	_, _ = io.Copy(io.Discard, stdout)

	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		switch {
		case len(matches) > maxGrepMatches:
			// Killed by us after the cap; the matches gathered are valid.
		case errors.As(err, &exit) && exit.ExitCode() == 1:
			return nil, nil // no matches
		case errors.As(err, &exit) && exit.ExitCode() == 2:
			return nil, fmt.Errorf("rg: %s", strings.TrimSpace(stderr.String()))
		default:
			return nil, fmt.Errorf("rg: %w", err)
		}
	}
	return matches, nil
}

// grepWalk is the pure-Go search: walk target (or read it, if a file), skip
// what glob skips plus binary files, and test every line. include is
// matched against the path relative to the root, so a slash-free pattern
// selects by file name anywhere and a path pattern is anchored to the
// working directory — the same reading ripgrep gives it.
func grepWalk(ctx context.Context, root tool.Root, target string, re *regexp.Regexp, include *globMatcher) ([]grepMatch, error) {
	var matches []grepMatch
	visit := func(path string) error {
		if include != nil && !include.Match(filepath.ToSlash(root.Rel(path))) {
			return nil
		}
		found, err := grepFile(path, re, maxGrepMatches+1-len(matches))
		if err != nil {
			return nil // unreadable or binary: skip
		}
		matches = append(matches, found...)
		if len(matches) > maxGrepMatches {
			return filepath.SkipAll
		}
		return nil
	}

	info, err := os.Stat(target)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return matches, visit(target)
	}
	err = filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == target {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if path != target && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		return visit(path)
	})
	return matches, err
}

// grepFile returns up to limit matching lines of one file, or an error for
// a binary or unreadable one.
func grepFile(path string, re *regexp.Regexp, limit int) ([]grepMatch, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if bin, err := isBinary(f); err != nil || bin {
		return nil, errors.New("binary")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	var out []grepMatch
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	line := 0
	for sc.Scan() && len(out) < limit {
		line++
		if re.Match(sc.Bytes()) {
			out = append(out, grepMatch{path: path, line: line, text: strings.TrimRight(sc.Text(), "\r")})
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return nil, err
	}
	return out, nil
}
