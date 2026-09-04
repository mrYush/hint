package builtin

import "testing"

func TestGlobMatcher(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"*.go", "main.go", true},
		{"*.go", "cmd/hint/main.go", true}, // no slash: base name at any depth
		{"*.go", "main.go.bak", false},
		{"cmd/*.go", "cmd/main.go", true},
		{"cmd/*.go", "cmd/hint/main.go", false},
		{"cmd/**/*.go", "cmd/main.go", true}, // ** spans zero levels
		{"cmd/**/*.go", "cmd/hint/main.go", true},
		{"cmd/**/*.go", "cmd/a/b/c/main.go", true},
		{"cmd/**/*.go", "internal/main.go", false},
		{"**/*_test.go", "a/b/x_test.go", true},
		{"**/*_test.go", "x_test.go", true},
		{"**", "anything/at/all", true},
		{"internal/**", "internal/x/y", true},
		{"internal/**", "pkg/x", false},
		{"*.{go,md}", "README.md", true},
		{"*.{go,md}", "main.go", true},
		{"*.{go,md}", "main.rs", false},
		{"docs/{plan,api}/*.md", "docs/plan/a.md", true},
		{"docs/{plan,api}/*.md", "docs/other/a.md", false},
		{"./cmd/*.go", "cmd/main.go", true},
		{"src/?.js", "src/a.js", true},
		{"src/?.js", "src/ab.js", false},
		{"[ab]*.txt", "apple.txt", true},
		{"[ab]*.txt", "cherry.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.pattern+" "+tt.path, func(t *testing.T) {
			m, err := compileGlob(tt.pattern)
			if err != nil {
				t.Fatalf("compile: %v", err)
			}
			if got := m.Match(tt.path); got != tt.want {
				t.Fatalf("Match(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

func TestGlobMatcher_Invalid(t *testing.T) {
	for _, p := range []string{"", "[unclosed", "a/[b"} {
		if _, err := compileGlob(p); err == nil {
			t.Errorf("compileGlob(%q) accepted", p)
		}
	}
}

func TestExpandBraces(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"plain", []string{"plain"}},
		{"a{b,c}", []string{"ab", "ac"}},
		{"{x,y}/{1,2}", []string{"x/1", "x/2", "y/1", "y/2"}},
		{"a{b,{c,d}}", []string{"ab", "ac", "ad"}},
		{"unbalanced{", []string{"unbalanced{"}},
	}
	for _, tt := range tests {
		got := expandBraces(tt.in)
		if len(got) != len(tt.want) {
			t.Fatalf("expandBraces(%q) = %v, want %v", tt.in, got, tt.want)
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Fatalf("expandBraces(%q) = %v, want %v", tt.in, got, tt.want)
			}
		}
	}
}
