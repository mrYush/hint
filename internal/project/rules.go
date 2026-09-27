package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/mrYush/hint/internal/glob"
)

// A project that outgrows one HINT.md splits it: the file stays a short
// index and rule files hold the detail (WP0.12 rung 4). A rule file is
// plain Markdown with optional YAML front matter. One with `paths:` globs
// is loaded only once a tool of the conversation reads or writes a
// matching file — until then the prompt lists it by title — and one
// without is loaded always, as another instruction file.

// RuleDirs are the directories, relative to each instruction directory,
// that hold rule files: hint's own, then Cursor's, read as a compatible
// source (its `globs:` front matter maps onto `paths:`) so a team does not
// keep two trees of the same rules.
var RuleDirs = []string{".hint/rules", ".cursor/rules"}

// ruleExtensions are the file names read in a rule directory: Markdown,
// plus Cursor's .mdc.
var ruleExtensions = map[string]bool{".md": true, ".mdc": true}

// Rule is one rule file.
type Rule struct {
	// Path is the file's absolute path.
	Path string
	// Root is the directory the globs are relative to: the project
	// directory that holds the rule directory.
	Root string
	// Title names the rule in the prompt's index: the front matter's
	// title or description, else the file's first heading, else its name.
	Title string
	// Paths are the globs the rule applies to, relative to Root, in the
	// syntax of the glob tool. Empty means the rule applies always.
	Paths []string
}

// Conditional reports a rule that waits for a matching path.
func (r Rule) Conditional() bool { return len(r.Paths) > 0 }

// Matches reports whether abs, an absolute path, is one the rule applies
// to. A pattern without a slash matches the file's name at any depth; a
// pattern the glob package rejects matches nothing.
func (r Rule) Matches(abs string) bool {
	rel, err := filepath.Rel(r.Root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	rel = filepath.ToSlash(rel)
	for _, p := range r.Paths {
		m, err := glob.Compile(p)
		if err == nil && m.Match(rel) {
			return true
		}
	}
	return false
}

// ruleHeader is the front matter a rule file may carry. Cursor's `globs`
// and `alwaysApply` are read alongside hint's `paths`.
type ruleHeader struct {
	Title       string   `yaml:"title"`
	Description string   `yaml:"description"`
	Paths       globList `yaml:"paths"`
	Globs       globList `yaml:"globs"`
	AlwaysApply *bool    `yaml:"alwaysApply"`
}

// globList accepts a YAML sequence of patterns or one scalar with patterns
// separated by commas, the way Cursor writes `globs: *.ts, *.tsx`.
type globList []string

func (l *globList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.SequenceNode:
		var items []string
		if err := n.Decode(&items); err != nil {
			return err
		}
		*l = items
	case yaml.ScalarNode:
		*l = strings.Split(n.Value, ",")
	case yaml.DocumentNode, yaml.MappingNode, yaml.AliasNode:
		return fmt.Errorf("paths must be a list or a string")
	}
	var out globList
	for _, p := range *l {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	*l = out
	return nil
}

// splitFrontMatter separates a leading YAML block delimited by "---" lines
// from the Markdown body. ok is false when there is no such block.
func splitFrontMatter(content string) (header, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", content, false
	}
	rest := content[strings.Index(content, "\n")+1:]
	for _, end := range []string{"\n---\n", "\n---\r\n"} {
		if i := strings.Index(rest, end); i >= 0 {
			return strings.TrimRight(rest[:i], "\r"), rest[i+len(end):], true
		}
	}
	if strings.HasSuffix(strings.TrimRight(rest, "\r\n"), "\n---") || strings.TrimRight(rest, "\r\n") == "---" {
		// A header that closes on the last line of the file.
		trimmed := strings.TrimRight(rest, "\r\n")
		return strings.TrimSuffix(trimmed, "---"), "", true
	}
	return "", content, false
}

// DiscoverRules lists the rule files under dirs, outer directory first and
// within one directory hint's before Cursor's, each in name order. A rule
// whose front matter cannot be read is reported and loaded always: a
// header the author got wrong should not hide the rule.
func DiscoverRules(dirs []string) ([]Rule, []string) {
	var rules []Rule
	var warnings []string
	for _, dir := range dirs {
		for _, sub := range RuleDirs {
			entries, err := os.ReadDir(filepath.Join(dir, sub))
			if err != nil {
				continue
			}
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				if e.Type().IsRegular() && ruleExtensions[filepath.Ext(e.Name())] {
					names = append(names, e.Name())
				}
			}
			sort.Strings(names)
			for _, name := range names {
				p := filepath.Join(dir, sub, name)
				r, warn := readRule(p, dir)
				if warn != "" {
					warnings = append(warnings, warn)
				}
				rules = append(rules, r)
			}
		}
	}
	return rules, warnings
}

// readRule builds the Rule for the file at p under root.
func readRule(p, root string) (Rule, string) {
	r := Rule{Path: p, Root: root}
	data, err := os.ReadFile(p)
	if err != nil {
		return r, fmt.Sprintf("%s: %v", p, err)
	}
	header, body, ok := splitFrontMatter(string(data))
	var h ruleHeader
	var warn string
	if ok {
		if err := yaml.Unmarshal([]byte(quoteBareGlobs(header)), &h); err != nil {
			warn = fmt.Sprintf("%s: front matter not read (%v); the rule is loaded always", p, err)
			h = ruleHeader{}
		}
	}
	r.Paths = h.Paths
	if len(r.Paths) == 0 {
		r.Paths = h.Globs
	}
	if h.AlwaysApply != nil && *h.AlwaysApply {
		r.Paths = nil
	}
	var valid []string
	for _, pat := range r.Paths {
		if _, err := glob.Compile(pat); err != nil {
			warn = fmt.Sprintf("%s: %v; the pattern is ignored", p, err)
			continue
		}
		valid = append(valid, pat)
	}
	r.Paths = valid
	r.Title = ruleTitle(h, body, p)
	return r, warn
}

// bareGlob matches a `paths:` or `globs:` line whose value starts with
// `*` — which YAML reads as an alias — the way Cursor writes them.
var bareGlob = regexp.MustCompile(`(?m)^(\s*(?:paths|globs)\s*:\s*)(\*[^\n]*)$`)

// quoteBareGlobs quotes such values so the header parses as the author
// meant it: `globs: *.ts, *.tsx` becomes `globs: "*.ts, *.tsx"`, which
// globList then splits on the commas.
func quoteBareGlobs(header string) string {
	return bareGlob.ReplaceAllStringFunc(header, func(line string) string {
		m := bareGlob.FindStringSubmatch(line)
		return m[1] + strconv.Quote(strings.TrimSpace(m[2]))
	})
}

// ruleTitle picks the name the index shows for a rule.
func ruleTitle(h ruleHeader, body, p string) string {
	if t := strings.TrimSpace(h.Title); t != "" {
		return t
	}
	if d := strings.TrimSpace(h.Description); d != "" {
		return d
	}
	for _, s := range Sections(body) {
		if s.Level > 0 && strings.TrimSpace(s.Title) != "" {
			return s.Title
		}
	}
	return strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
}

// Activation tracks which conditional rules a conversation has reached.
// It is fed every path a tool call touches and hands back, in discovery
// order, the rules those paths have activated; a rule once active stays
// active for the run. Safe for use from several goroutines, although the
// agent loop is the only expected caller.
type Activation struct {
	mu     sync.Mutex
	rules  []Rule
	active []bool
}

// NewActivation tracks the conditional rules among rules; the rest are
// ignored, they are loaded always.
func NewActivation(rules []Rule) *Activation {
	a := &Activation{}
	for _, r := range rules {
		if r.Conditional() {
			a.rules = append(a.rules, r)
		}
	}
	a.active = make([]bool, len(a.rules))
	return a
}

// Touch records that paths were read or written and returns the rules
// that became active because of it, in discovery order.
func (a *Activation) Touch(paths ...string) []Rule {
	a.mu.Lock()
	defer a.mu.Unlock()
	var activated []Rule
	for i, r := range a.rules {
		if a.active[i] {
			continue
		}
		for _, p := range paths {
			if r.Matches(p) {
				a.active[i] = true
				activated = append(activated, r)
				break
			}
		}
	}
	return activated
}

// Active returns the rules touched so far, in discovery order.
func (a *Activation) Active() []Rule { return a.pick(true) }

// Pending returns the rules not touched yet, in discovery order.
func (a *Activation) Pending() []Rule { return a.pick(false) }

func (a *Activation) pick(active bool) []Rule {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Rule
	for i, r := range a.rules {
		if a.active[i] == active {
			out = append(out, r)
		}
	}
	return out
}

// RenderRuleIndex lists rules that are not loaded yet, so the model knows
// they exist, what they cover, and that the instructions tool can read
// them before a touch loads them. Empty when there are none.
func RenderRuleIndex(pending []Rule) string {
	if len(pending) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<project_rules>\n")
	b.WriteString("These rules apply to particular paths and load into the instructions above once a tool reads or writes a matching file; until then only their titles are listed. The instructions tool can read any of them now.\n")
	for _, r := range pending {
		fmt.Fprintf(&b, "- %s (%s) for %s\n", r.Title, r.Path, strings.Join(r.Paths, ", "))
	}
	b.WriteString("</project_rules>")
	return b.String()
}
