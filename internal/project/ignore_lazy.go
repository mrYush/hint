package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WithEnviron supplies the same sanitized environment used by file tools.
func (r Rules) WithEnviron(environ func() []string) Rules { r.environ = environ; return r }

type lazyGitIgnorer struct {
	mu       sync.Mutex
	ctx      context.Context
	git, dir string
	environ  func() []string
	cache    map[string]bool
	err      error
}

func newLazyGitIgnorer(ctx context.Context, git, dir string, environ func() []string) (Ignorer, error) {
	g := &lazyGitIgnorer{ctx: ctx, git: git, dir: dir, environ: environ, cache: make(map[string]bool)}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := g.command(probe, "rev-parse", "--is-inside-work-tree")
	stdout := &boundedIgnoreOutput{limit: 32}
	stderr := &boundedIgnoreOutput{limit: 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 128 && strings.Contains(stderr.String(), "not a git repository") {
			return Basic, nil
		}
		g.err = fmt.Errorf("git worktree policy unavailable: %w", err)
		return g, g.err
	}
	if strings.TrimSpace(stdout.String()) != "true" {
		return Basic, nil
	}
	return g, nil
}

// PrepareIgnore batches one bounded directory page when the implementation
// supports it; a custom/basic Ignorer needs no preparation.
func PrepareIgnore(ig Ignorer, parent string, entries []fs.DirEntry) error {
	if batch, ok := ig.(interface {
		prepare(string, []fs.DirEntry) error
	}); ok {
		return batch.prepare(parent, entries)
	}
	return nil
}

// IgnoreError reports a failed policy observation instead of an empty tree.
func IgnoreError(ig Ignorer) error {
	if checked, ok := ig.(*lazyGitIgnorer); ok {
		checked.mu.Lock()
		defer checked.mu.Unlock()
		return checked.err
	}
	return nil
}

func (g *lazyGitIgnorer) prepare(parent string, entries []fs.DirEntry) error {
	var names []string
	for _, entry := range entries {
		rel := path.Join(parent, entry.Name())
		if !Basic.Ignored(rel, entry.IsDir()) {
			names = append(names, rel)
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.check(names)
}

func (g *lazyGitIgnorer) command(ctx context.Context, args ...string) *exec.Cmd {
	// Even a read-only ls-files probe can invoke repository-configured
	// fsmonitor hooks unless the override precedes the command.
	args = append([]string{"-c", "core.fsmonitor=false"}, args...)
	cmd := exec.CommandContext(ctx, g.git, args...)
	cmd.Dir = g.dir
	cmd.Stderr = io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	if g.environ != nil {
		cmd.Env = g.environ()
	}
	return cmd
}

func (g *lazyGitIgnorer) check(names []string) error {
	if g.err != nil {
		return g.err
	}
	if len(names) == 0 {
		return nil
	}
	if len(names) > 32 {
		g.err = errors.New("git ignore batch exceeds 32 paths")
		return g.err
	}
	var input strings.Builder
	for _, name := range names {
		if len(name) > 16<<10 || strings.ContainsRune(name, 0) {
			g.err = errors.New("git ignore path exceeds its bound")
			return g.err
		}
		input.WriteString(name)
		input.WriteByte(0)
	}
	ctx, cancel := context.WithTimeout(g.ctx, 2*time.Second)
	defer cancel()
	cmd := g.command(ctx, "check-ignore", "-z", "--stdin")
	cmd.Stdin = strings.NewReader(input.String())
	output := &boundedIgnoreOutput{limit: input.Len()}
	cmd.Stdout = output
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		g.err = fmt.Errorf("git ignore policy unavailable: %w", err)
		return g.err
	}
	if ctx.Err() != nil {
		g.err = ctx.Err()
		return g.err
	}
	if len(g.cache)+len(names) > 128 {
		g.cache = make(map[string]bool)
	}
	for _, name := range names {
		g.cache[name] = false
	}
	requested := make(map[string]bool, len(names))
	for _, name := range names {
		requested[name] = true
	}
	for _, name := range bytes.Split(output.Bytes(), []byte{0}) {
		if len(name) > 0 {
			if !requested[string(name)] {
				g.err = errors.New("git ignore returned an unrequested path")
				return g.err
			}
			g.cache[string(name)] = true
		}
	}
	return nil
}

func (g *lazyGitIgnorer) Ignored(rel string, isDir bool) bool {
	if Basic.Ignored(rel, isDir) {
		return true
	}
	rel = path.Clean(rel)
	if rel == "." {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	ignored, cached := g.cache[rel]
	if !cached {
		if err := g.check([]string{rel}); err != nil {
			return true
		}
		ignored = g.cache[rel]
	}
	if g.err != nil {
		return true
	}
	if isDir && ignored {
		// An ignored directory may still contain a force-added tracked file.
		// Stop the scoped probe at its first byte instead of buffering a list.
		ctx, cancel := context.WithTimeout(g.ctx, 2*time.Second)
		output := &firstIgnoreOutput{cancel: cancel}
		cmd := g.command(ctx, "--literal-pathspecs", "ls-files", "-z", "--cached", "--", rel+"/")
		cmd.Stdout = output
		err := cmd.Run()
		cancel()
		if output.found && g.ctx.Err() == nil {
			ignored = false
			g.cache[rel] = false
		} else if err != nil {
			g.err = fmt.Errorf("tracked directory policy unavailable: %w", err)
			return true
		}
	}
	if isDir && !ignored {
		file, err := os.Open(filepath.Join(g.dir, filepath.FromSlash(rel)))
		if err == nil {
			_, readErr := file.ReadDir(1)
			closeErr := file.Close()
			if closeErr != nil {
				g.err = closeErr
				return true
			}
			if errors.Is(readErr, io.EOF) {
				return true
			}
		}
	}
	return ignored
}

type boundedIgnoreOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedIgnoreOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.Len() {
		return 0, errors.New("git ignore response exceeds request bound")
	}
	return b.Buffer.Write(data)
}

type firstIgnoreOutput struct {
	cancel context.CancelFunc
	found  bool
}

func (o *firstIgnoreOutput) Write(data []byte) (int, error) {
	if len(data) > 0 {
		o.found = true
		o.cancel()
	}
	return len(data), nil
}
