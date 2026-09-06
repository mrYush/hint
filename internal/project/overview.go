package project

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
)

// Overview renders the shape of the working directory for the prompt.
// Implementations differ in how many tokens they spend up front: [Tree]
// shows the first levels so the model can orient without a tool call,
// [None] spends nothing and leaves discovery to list_dir and glob. The
// choice is the caller's today; the interface is what would let the
// agent itself pick a shape from the conversation later.
type Overview interface {
	// Render returns the overview of fsys, honouring ig, or "" for none.
	Render(ctx context.Context, fsys fs.FS, ig Ignorer) (string, error)
}

// Tree renders the directory as an indented listing down to Depth levels,
// stopping after MaxEntries entries so that a monorepo costs the same as
// a small project. Both zero values mean the defaults.
type Tree struct {
	Depth      int
	MaxEntries int
}

// Default limits of a [Tree]: two levels is enough to see a project's
// layout, and 100 entries is a few hundred tokens.
const (
	DefaultTreeDepth      = 2
	DefaultTreeMaxEntries = 100
)

// Render implements [Overview].
func (t Tree) Render(ctx context.Context, fsys fs.FS, ig Ignorer) (string, error) {
	depth, maxEntries := t.Depth, t.MaxEntries
	if depth <= 0 {
		depth = DefaultTreeDepth
	}
	if maxEntries <= 0 {
		maxEntries = DefaultTreeMaxEntries
	}
	if ig == nil {
		ig = Basic
	}

	var b strings.Builder
	entries := 0
	truncated := false
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if p == "." {
			return err
		}
		if err != nil {
			// An unreadable subtree is noted, not fatal: the rest of the
			// tree is still worth showing.
			fmt.Fprintf(&b, "%s%s (unreadable)\n", indent(p), d.Name())
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if ig.Ignored(p, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if entries >= maxEntries {
			truncated = true
			return fs.SkipAll
		}
		entries++
		name := d.Name()
		if d.IsDir() {
			name += "/"
		}
		fmt.Fprintf(&b, "%s%s\n", indent(p), name)
		if d.IsDir() && level(p) >= depth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if entries == 0 {
		return "(empty directory)", nil
	}
	if truncated {
		fmt.Fprintf(&b, "... (cut at %d entries; use list_dir or glob to see more)\n", maxEntries)
	} else {
		fmt.Fprintf(&b, "(showing %s; use list_dir or glob to see deeper)\n", plural(depth, "level"))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// plural is "1 level" or "N levels".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// level is how many directories deep p sits below the root: 1 for a
// top-level entry.
func level(p string) int {
	return strings.Count(p, "/") + 1
}

// indent is two spaces per level below the top.
func indent(p string) string {
	return strings.Repeat("  ", level(p)-1)
}

// None is the [Overview] that renders nothing.
type None struct{}

// Render implements [Overview].
func (None) Render(context.Context, fs.FS, Ignorer) (string, error) { return "", nil }
