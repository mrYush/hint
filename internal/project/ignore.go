package project

import (
	"context"
	"errors"
	"path"
	"sort"
	"strings"
)

// Ignorer decides which entries a walk of a directory skips. rel is the
// slash-separated path of the entry relative to the directory the Ignorer
// was built for ("." names that directory and is never ignored); isDir
// lets a whole subtree be pruned in one answer.
type Ignorer interface {
	Ignored(rel string, isDir bool) bool
}

// skippedDirs are directory names no walk descends into: dependency and
// build output trees that dwarf the source they belong to. Inside a git
// repository they are usually ignored anyway; the list keeps the tools'
// behaviour the same outside one, and prunes a committed vendor tree that
// would otherwise swamp a listing.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"__pycache__":  true,
	"vendor":       true,
	"target":       true,
	"dist":         true,
}

// SkippedDirs returns the directory names every Ignorer prunes, sorted,
// for callers that translate the rule into another tool's flags.
func SkippedDirs() []string {
	names := make([]string, 0, len(skippedDirs))
	for name := range skippedDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsHidden reports whether name is a dot-file; "." and ".." are not.
func IsHidden(name string) bool {
	return len(name) > 1 && name[0] == '.' && name != ".."
}

// basic is the rule that applies everywhere: hidden entries and the
// dependency directories are skipped, nothing else is.
type basic struct{}

// Basic is the Ignorer used outside a git repository, and the floor every
// other Ignorer builds on.
var Basic Ignorer = basic{}

// Ignored looks at every component of rel, not only the last, so the
// answer for a nested path is the same whether or not a walk pruned its
// parent first.
func (basic) Ignored(rel string, isDir bool) bool {
	if rel == "." || rel == "" {
		return false
	}
	parts := strings.Split(path.Clean(rel), "/")
	for i, name := range parts {
		if IsHidden(name) {
			return true
		}
		if skippedDirs[name] && (i < len(parts)-1 || isDir) {
			return true
		}
	}
	return false
}

// gitIgnorer layers git's view of a directory over [Basic]: an entry is
// kept only when `git ls-files` listed it (tracked, or untracked and not
// ignored) or listed something beneath it.
type gitIgnorer struct {
	files map[string]struct{} // every path git listed, relative to the directory
	dirs  map[string]struct{} // every ancestor directory of a listed path
}

func newGitIgnorer(listed []string) *gitIgnorer {
	g := &gitIgnorer{
		files: make(map[string]struct{}, len(listed)),
		dirs:  make(map[string]struct{}),
	}
	for _, p := range listed {
		p = path.Clean(p)
		g.files[p] = struct{}{}
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if _, seen := g.dirs[d]; seen {
				break
			}
			g.dirs[d] = struct{}{}
		}
	}
	return g
}

func (g *gitIgnorer) Ignored(rel string, isDir bool) bool {
	if Basic.Ignored(rel, isDir) {
		return true
	}
	rel = path.Clean(rel)
	if rel == "." {
		return false
	}
	if isDir {
		if _, ok := g.dirs[rel]; ok {
			return false
		}
	} else if _, ok := g.files[rel]; ok {
		return false
	}
	// git lists a submodule as one entry at its mount point; everything
	// beneath such an entry is kept, since git cannot say more about it.
	for d := path.Dir(rel); d != "." && d != "/"; d = path.Dir(d) {
		if _, ok := g.files[d]; ok {
			return false
		}
	}
	return true
}

// Rules builds the [Ignorer] for a directory. With a git executable it
// asks git inside a repository and falls back to [Basic] elsewhere; with
// none it is [Basic] everywhere.
type Rules struct {
	git string
}

// NewRules returns Rules using the git executable at gitPath, or the
// [Basic] rule alone when gitPath is empty.
func NewRules(gitPath string) Rules {
	return Rules{git: gitPath}
}

// Ignorer returns the rules for dir. The Ignorer is always usable: when
// git cannot answer for an unexpected reason (a corrupt index, a killed
// process) the result is [Basic] and the error says why, so a caller
// that has somewhere to report it can. Not being in a repository, or
// having no git at all, is the expected case and reports no error.
func (r Rules) Ignorer(ctx context.Context, dir string) (Ignorer, error) {
	if r.git == "" {
		return Basic, nil
	}
	listed, err := gitListFiles(ctx, r.git, dir)
	if err != nil {
		if errors.Is(err, ErrNotRepository) {
			return Basic, nil
		}
		return Basic, err
	}
	return newGitIgnorer(listed), nil
}
