package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	globName = "glob"
	// maxGlobResults caps the paths returned, newest first.
	maxGlobResults = 200
)

const globDescription = `Find files by name pattern under the working directory. Returns matching paths, most recently modified first.

Pattern syntax: * matches within a path segment, ** matches across directories, ? matches one character, [abc] a character class, {a,b} alternatives. A pattern without a slash matches the file name at any depth: "*.go" finds every Go file; "cmd/**/*.go" only those under cmd. Hidden files and dependency directories are skipped. Results are cut at 200; narrow the pattern or the path if that happens.`

type globArgs struct {
	Pattern string `json:"pattern" jsonschema_description:"Glob pattern to match file paths against"`
	Path    string `json:"path,omitempty" jsonschema_description:"Directory to search under, relative to the working directory (default: the working directory)"`
}

var globSchema = tool.MustSchema(globArgs{})

type globTool struct {
	root tool.Root
	rg   string
}

func newGlob(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&globTool{root: root, rg: o.rg}, o.limits)
}

func (*globTool) Name() string                 { return globName }
func (*globTool) Description() string          { return globDescription }
func (*globTool) InputSchema() json.RawMessage { return globSchema }
func (*globTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }

// Run implements agentapi.Tool.
func (t *globTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	var in globArgs
	if err := tool.DecodeArgs(args, &in); err != nil {
		return tool.InvalidArgs(callID, globName, err), nil
	}
	if strings.TrimSpace(in.Pattern) == "" {
		return tool.InvalidArgs(callID, globName, errors.New("pattern is required")), nil
	}
	// Validate with the Go matcher even when ripgrep will run: it gives a
	// clear message, and the two implementations must agree on what is a
	// pattern at all.
	matcher, err := compileGlob(in.Pattern)
	if err != nil {
		return tool.InvalidArgs(callID, globName, err), nil
	}

	dir, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, globName, err.Error()), nil
	}
	if info, err := os.Stat(dir); err != nil {
		return agentapi.ErrorResult(callID, globName, pathError(t.root, dir, err)), nil
	} else if !info.IsDir() {
		return agentapi.ErrorResult(callID, globName, fmt.Sprintf("%s is not a directory", t.root.Rel(dir))), nil
	}

	var paths []string
	if t.rg != "" {
		paths, err = t.globRipgrep(ctx, dir, in.Pattern)
	}
	if t.rg == "" || err != nil {
		// Ripgrep unavailable or unhappy: the pure-Go walk is the
		// reference behaviour anyway.
		paths, err = globWalk(ctx, dir, matcher)
	}
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, globName, pathError(t.root, dir, err)), nil
	}

	type hit struct {
		path string
		mod  time.Time
	}
	hits := make([]hit, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		hits = append(hits, hit{path: p, mod: info.ModTime()})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].mod.After(hits[j].mod) })

	if len(hits) == 0 {
		return agentapi.TextResult(callID, globName, "No files found"), nil
	}
	truncated := len(hits) > maxGlobResults
	if truncated {
		hits = hits[:maxGlobResults]
	}
	var b strings.Builder
	for _, h := range hits {
		b.WriteString(t.root.Rel(h.path))
		b.WriteByte('\n')
	}
	if truncated {
		fmt.Fprintf(&b, "\n(showing the %d most recently modified matches; narrow the pattern or path to see others)", maxGlobResults)
	}
	return agentapi.TextResult(callID, globName, strings.TrimRight(b.String(), "\n")), nil
}

// globRipgrep lists files under dir matching pattern with `rg --files`.
// Ripgrep honours .gitignore on top of the hidden-file rule, which the Go
// walk does not (WP0.8); the plan accepts that difference for the speed.
func (t *globTool) globRipgrep(ctx context.Context, dir, pattern string) ([]string, error) {
	args := append([]string{"--files", "--null", "--glob", pattern}, rgExcludeArgs()...)
	cmd := exec.CommandContext(ctx, t.rg, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil // no matches
		}
		return nil, fmt.Errorf("rg: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	var out []string
	for _, p := range bytes.Split(stdout.Bytes(), []byte{0}) {
		if len(p) == 0 {
			continue
		}
		out = append(out, filepath.Join(dir, string(p)))
	}
	return out, nil
}

// globWalk is the pure-Go listing: every regular file under dir, outside
// hidden and dependency directories, whose path relative to dir matches.
func globWalk(ctx context.Context, dir string, m *globMatcher) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if path == dir {
			return nil
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return nil
		}
		if m.Match(filepath.ToSlash(rel)) {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
