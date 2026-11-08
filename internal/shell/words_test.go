package shell

import (
	"reflect"
	"testing"
)

func TestSplitWords(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{line: "/review main.go", want: []string{"/review", "main.go"}},
		{line: `/review "the auth flow"`, want: []string{"/review", "the auth flow"}},
		{line: `/review 'the auth flow'`, want: []string{"/review", "the auth flow"}},
		{line: `/review the\ auth\ flow`, want: []string{"/review", "the auth flow"}},
		{line: `/review "say \"hello\""`, want: []string{"/review", `say "hello"`}},
		{line: `/review ''`, want: []string{"/review", ""}},
		{line: `/review pre"two words"post`, want: []string{"/review", "pretwo wordspost"}},
		{line: `/review C:\temp`, want: []string{"/review", `C:\temp`}},
		{line: `/review "C:\temp"`, want: []string{"/review", `C:\temp`}},
		{line: `/review C:\temp\file`, want: []string{"/review", `C:\temp\file`}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got, err := SplitWords(tt.line)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SplitWords = %q, %v; want %q", got, err, tt.want)
			}
		})
	}
	for _, line := range []string{`/review "open`, `/review open\`} {
		if _, err := SplitWords(line); err == nil {
			t.Errorf("SplitWords(%q) accepted incomplete input", line)
		}
	}
}

func TestQuoteWordRoundTrip(t *testing.T) {
	for _, want := range []string{"main.go", "the auth flow", `say "hello"`, `a\\b`, ""} {
		quoted := QuoteWord(want)
		got, err := SplitWords(quoted)
		if err != nil || len(got) != 1 || got[0] != want {
			t.Errorf("round trip %q through %q = %q, %v", want, quoted, got, err)
		}
	}
}

func TestEndsInSpace(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{line: "word ", want: true},
		{line: `word\ `, want: false},
		{line: `"word `, want: false},
		{line: `"word" `, want: true},
		{line: `word\\ `, want: true},
		{line: "", want: false},
	}
	for _, tt := range tests {
		if got := EndsInSpace(tt.line); got != tt.want {
			t.Errorf("EndsInSpace(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}
