package project

import (
	"strings"
	"testing"
)

func TestSplitFrontMatter(t *testing.T) {
	cases := []struct {
		in, header, body string
		ok               bool
	}{
		{"---\na: 1\n---\nbody\n", "a: 1", "body\n", true},
		{"---\r\na: 1\r\n---\r\nbody", "a: 1", "body", true},
		{"---\na: 1\n---", "a: 1\n", "", true},
		{"no header\n---\nnot one\n", "", "no header\n---\nnot one\n", false},
		{"---\nnever closed\n", "", "---\nnever closed\n", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		header, body, ok := splitFrontMatter(c.in)
		if header != c.header || body != c.body || ok != c.ok {
			t.Errorf("splitFrontMatter(%q) = %q, %q, %v; want %q, %q, %v", c.in, header, body, ok, c.header, c.body, c.ok)
		}
	}
}

func TestOrderRules(t *testing.T) {
	files := []string{"/r/HINT.md", "/r/a/b/AGENTS.md"}
	rules := []Rule{
		{Path: "/r/.hint/rules/x.md", Root: "/r"},
		{Path: "/r/.hint/rules/go.md", Root: "/r", Paths: []string{"*.go"}},
		{Path: "/r/a/.hint/rules/mid.md", Root: "/r/a"},      // a directory with rules but no file of its own
		{Path: "/r/a/b/.hint/rules/leaf.md", Root: "/r/a/b"}, // right after its own file
	}
	always := func(r Rule) bool { return !r.Conditional() }
	got := orderRules(files, rules, always)
	want := []string{"/r/HINT.md", "/r/.hint/rules/x.md", "/r/a/.hint/rules/mid.md", "/r/a/b/AGENTS.md", "/r/a/b/.hint/rules/leaf.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("orderRules(always) = %v, want %v", got, want)
	}
	got = orderRules(files, rules, func(Rule) bool { return true })
	want = []string{"/r/HINT.md", "/r/.hint/rules/x.md", "/r/.hint/rules/go.md", "/r/a/.hint/rules/mid.md", "/r/a/b/AGENTS.md", "/r/a/b/.hint/rules/leaf.md"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("orderRules(all) = %v, want %v", got, want)
	}
}

func TestQuoteBareGlobs(t *testing.T) {
	in := "description: x\nglobs: *.ts, *.tsx\npaths:\n  - \"**/*.go\"\nalwaysApply: false\n"
	want := "description: x\nglobs: \"*.ts, *.tsx\"\npaths:\n  - \"**/*.go\"\nalwaysApply: false\n"
	if got := quoteBareGlobs(in); got != want {
		t.Errorf("quoteBareGlobs:\n%s\nwant:\n%s", got, want)
	}
}
