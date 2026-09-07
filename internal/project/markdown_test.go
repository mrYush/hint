package project_test

import (
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/project"
)

const guideDoc = "# Guide\n\nIntro sentence here. More intro.\n\n## Build\n\nRun make. Then wait.\n\n```\n# not a heading\n```\n\n## Never\n\n- Never push to main. Ever.\n"

func TestSections(t *testing.T) {
	got := project.Sections(guideDoc)
	if len(got) != 3 {
		t.Fatalf("sections = %+v, want 3", got)
	}
	want := []struct {
		level int
		title string
		line  int
		first string
	}{
		{1, "Guide", 1, "Intro sentence here."},
		{2, "Build", 5, "Run make."},
		{2, "Never", 13, "Never push to main."},
	}
	for i, w := range want {
		s := got[i]
		if s.Level != w.level || s.Title != w.title || s.Line != w.line {
			t.Errorf("section %d = %d %q line %d, want %d %q line %d", i, s.Level, s.Title, s.Line, w.level, w.title, w.line)
		}
		if fs := project.FirstSentence(s.Body); fs != w.first {
			t.Errorf("first sentence of %q = %q, want %q", s.Title, fs, w.first)
		}
	}
	// The heading inside the fence stayed in Build's body.
	if !strings.Contains(got[1].Body, "# not a heading") {
		t.Errorf("fenced heading was split out: %q", got[1].Body)
	}
	// Text puts the heading back so a section reads as it did in the file.
	if !strings.HasPrefix(got[2].Text(), "## Never\n") || !strings.HasSuffix(got[2].Text(), "- Never push to main. Ever.") {
		t.Errorf("Text = %q", got[2].Text())
	}
}

func TestSections_PreambleAndHeadless(t *testing.T) {
	got := project.Sections("Be brief.\n\n# Rules\nOne.\n")
	if len(got) != 2 || got[0].Level != 0 || got[0].Body != "Be brief." || got[0].Line != 1 || got[1].Title != "Rules" || got[1].Line != 3 {
		t.Errorf("preamble: %+v", got)
	}
	got = project.Sections("no headings at all\njust text\n")
	if len(got) != 1 || got[0].Level != 0 || got[0].Body != "no headings at all\njust text" {
		t.Errorf("headless: %+v", got)
	}
	// Closing #s and indentation are cosmetics; # in a word is not a heading.
	got = project.Sections("  ## Closed ##\n#hashtag\n")
	if len(got) != 1 || got[0].Title != "Closed" || !strings.Contains(got[0].Body, "#hashtag") {
		t.Errorf("closed heading: %+v", got)
	}
}

func TestSpan(t *testing.T) {
	doc := "# A\nA body.\n## A1\nA1 body.\n### A1a\nDeep.\n## A2\nA2 body.\n# B\nB body.\n"
	s := project.Sections(doc)
	if got := project.Span(s, 1); got != "## A1\nA1 body.\n\n### A1a\nDeep." {
		t.Errorf("span of A1 = %q", got)
	}
	if got := project.Span(s, 0); !strings.Contains(got, "## A2") || strings.Contains(got, "# B") {
		t.Errorf("span of A = %q", got)
	}
	if got := project.Span(s, 4); got != "# B\nB body." {
		t.Errorf("span of B = %q", got)
	}
}

func TestFirstSentence(t *testing.T) {
	cases := map[string]string{
		"":                                   "",
		"\n\n  \n":                           "",
		"```\ncode\n```\nAfter.":             "",
		"One line\nsame paragraph. Second.":  "One line same paragraph.",
		"> quoted rule! Then more.":          "quoted rule!",
		"No terminator at all":               "No terminator at all",
		"e.g. this. Next.":                   "e.g.",
		strings.Repeat("word ", 60) + "end.": strings.TrimRight(strings.Repeat("word ", 60)[:200], " ") + "…",
	}
	for in, want := range cases {
		if got := project.FirstSentence(in); got != want {
			t.Errorf("FirstSentence(%q) = %q, want %q", in, got, want)
		}
	}
}
