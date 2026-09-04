package builtin

import (
	"fmt"
	"path"
	"strings"
)

// globMatcher matches slash-separated relative paths against a pattern with
// the usual extensions over path.Match: "**" spans any number of directory
// levels, "{a,b}" alternates, and a pattern without a slash matches the
// base name at any depth (as .gitignore and ripgrep treat it).
//
// It is the pure-Go implementation glob and grep fall back to when ripgrep
// is not installed; ripgrep's own glob syntax covers the same forms.
type globMatcher struct {
	alternatives [][]string // one segment list per brace expansion
}

// compileGlob validates pattern and prepares it for matching.
func compileGlob(pattern string) (*globMatcher, error) {
	pattern = strings.TrimPrefix(strings.ReplaceAll(pattern, "\\", "/"), "./")
	pattern = strings.TrimPrefix(pattern, "/")
	if pattern == "" {
		return nil, fmt.Errorf("empty pattern")
	}
	m := &globMatcher{}
	for _, alt := range expandBraces(pattern) {
		segs := strings.Split(alt, "/")
		for _, s := range segs {
			if s == "**" {
				continue
			}
			if _, err := path.Match(s, ""); err != nil {
				return nil, fmt.Errorf("invalid pattern %q: %w", pattern, err)
			}
		}
		m.alternatives = append(m.alternatives, segs)
	}
	return m, nil
}

// Match reports whether rel (slash-separated, relative to the search root)
// matches.
func (m *globMatcher) Match(rel string) bool {
	rel = strings.ReplaceAll(rel, "\\", "/")
	parts := strings.Split(rel, "/")
	for _, segs := range m.alternatives {
		if len(segs) == 1 {
			// No slash: match the base name wherever the file is.
			if ok, _ := path.Match(segs[0], parts[len(parts)-1]); ok {
				return true
			}
			continue
		}
		if matchSegments(segs, parts) {
			return true
		}
	}
	return false
}

// matchSegments matches pattern segments against path segments, letting
// "**" absorb zero or more of them. Patterns were validated by compileGlob,
// so path.Match errors cannot happen here.
func matchSegments(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegments(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], name[0]); !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// expandBraces rewrites "a{b,c}d" into ["abd", "acd"], recursively for
// several groups. An unbalanced brace is left as literal text.
func expandBraces(p string) []string {
	open := strings.IndexByte(p, '{')
	if open < 0 {
		return []string{p}
	}
	depth := 0
	for i := open; i < len(p); i++ {
		switch p[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				var out []string
				for _, alt := range splitTopLevel(p[open+1 : i]) {
					out = append(out, expandBraces(p[:open]+alt+p[i+1:])...)
				}
				return out
			}
		}
	}
	return []string{p}
}

// splitTopLevel splits s on commas that are not nested inside braces.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
