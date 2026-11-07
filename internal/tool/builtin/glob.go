package builtin

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/glob"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	globName = "glob"
	// maxGlobResults caps the paths returned, newest first.
	maxGlobResults = 200
)

const globDescription = `Find files by name pattern under the working directory. Returns matching paths, most recently modified first.

Pattern syntax: * matches within a path segment, ** matches across directories, ? matches one character, [abc] a character class, {a,b} alternatives. A pattern without a slash matches the file name at any depth: "*.go" finds every Go file; "cmd/**/*.go" only those under cmd. Hidden files, dependency directories and, inside a git repository, anything .gitignore excludes are skipped. Results are cut at 200; narrow the pattern or the path if that happens.`

type globArgs struct {
	Pattern string `json:"pattern" jsonschema_description:"Glob pattern to match file paths against"`
	Path    string `json:"path,omitempty" jsonschema_description:"Directory to search under, relative to the working directory (default: the working directory)"`
}

var globSchema = tool.MustSchema(globArgs{})

type globTool struct {
	root    tool.Root
	rg      string
	rules   project.Rules
	environ func() []string
}

func newGlob(root tool.Root, o options) agentapi.Tool {
	return tool.WithLimits(&globTool{root: root, rg: o.rg, rules: o.rules(), environ: o.env}, o.limits)
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
	matcher, err := glob.Compile(in.Pattern)
	if err != nil {
		return tool.InvalidArgs(callID, globName, err), nil
	}

	dir, err := t.root.Resolve(in.Path)
	if err != nil {
		return agentapi.ErrorResult(callID, globName, err.Error()), nil
	}
	if info, err := t.root.Stat(dir); err != nil {
		return agentapi.ErrorResult(callID, globName, pathError(t.root, dir, err)), nil
	} else if !info.IsDir() {
		return agentapi.ErrorResult(callID, globName, fmt.Sprintf("%s is not a directory", t.root.Rel(dir))), nil
	}

	var hits globHits
	if t.rg != "" {
		var paths []string
		paths, err = t.globRipgrep(ctx, dir, in.Pattern)
		if err == nil {
			for _, path := range paths {
				if info, statErr := t.root.Stat(path); statErr == nil {
					retainGlobHit(&hits, globHit{path: path, mod: info.ModTime()})
				}
			}
		}
	}
	if t.rg == "" || err != nil {
		// Ripgrep unavailable or unhappy: the pure-Go walk is the
		// reference behaviour. Its lazy policy retains and reports failures.
		ig, _ := t.rules.Ignorer(ctx, dir)
		hits, err = globWalk(ctx, t.root, dir, matcher, ig)
	}
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		return agentapi.ErrorResult(callID, globName, pathError(t.root, dir, err)), nil
	}

	sort.Slice(hits, func(i, j int) bool { return betterGlobHit(hits[i], hits[j]) })

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
// Ripgrep reads .gitignore itself; the Go walk gets the same answer from
// project.Rules, so the two agree inside a repository.
func (t *globTool) globRipgrep(ctx context.Context, dir, pattern string) ([]string, error) {
	args := append([]string{"--files", "--null", "--glob", pattern}, rgExcludeArgs(t.root, dir)...)
	cmd := exec.CommandContext(ctx, t.rg, args...)
	cmd.Dir = dir
	cmd.Env = t.environ()
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

// globWalk retains only the newest result window and one truncation marker.
// Match count and directory width never grow the retained hit collection.
func globWalk(ctx context.Context, root tool.Root, dir string, m *glob.Matcher, ig project.Ignorer) (globHits, error) {
	var out globHits
	err := root.WalkDirBatch(dir, func(parent string, entries []fs.DirEntry) error {
		return project.PrepareIgnore(ig, relSlash(dir, parent), entries)
	}, func(path string, d fs.DirEntry, err error) error {
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
		rel := relSlash(dir, path)
		if d.IsDir() {
			if ig.Ignored(rel, true) || root.Denied(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if ig.Ignored(rel, false) || !d.Type().IsRegular() || root.Denied(path) {
			return nil
		}
		if m.Match(rel) {
			if info, statErr := root.Stat(path); statErr == nil {
				retainGlobHit(&out, globHit{path: path, mod: info.ModTime()})
			}
		}
		return nil
	})
	if err == nil {
		err = project.IgnoreError(ig)
	}
	return out, err
}

// globHits is a worst-first heap: eviction removes the oldest match, breaking
// equal-time ties by path so filesystem enumeration order cannot affect output.
type globHit struct {
	path string
	mod  time.Time
}
type globHits []globHit

func betterGlobHit(a, b globHit) bool {
	return a.mod.After(b.mod) || a.mod.Equal(b.mod) && a.path < b.path
}
func (h globHits) Len() int           { return len(h) }
func (h globHits) Less(i, j int) bool { return betterGlobHit(h[j], h[i]) }
func (h globHits) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *globHits) Push(value any)    { *h = append(*h, value.(globHit)) }
func (h *globHits) Pop() any {
	old := *h
	value := old[len(old)-1]
	*h = old[:len(old)-1]
	return value
}

func retainGlobHit(h *globHits, candidate globHit) {
	if len(*h) < maxGlobResults+1 {
		heap.Push(h, candidate)
		return
	}
	if betterGlobHit(candidate, (*h)[0]) {
		(*h)[0] = candidate
		heap.Fix(h, 0)
	}
}
