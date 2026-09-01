package config

import "testing"

func TestExpandValue(t *testing.T) {
	env := map[string]string{
		"KEY":   "secret",
		"MODEL": "gpt-4o",
	}
	look := func(k string) (string, bool) {
		v, ok := env[k]
		return v, ok
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"braced", "${KEY}", "secret"},
		{"bare", "$KEY", "secret"},
		{"embedded", "Bearer ${KEY}!", "Bearer secret!"},
		{"two refs", "$MODEL/${KEY}", "gpt-4o/secret"},
		{"unset is empty", "${MISSING}", ""},
		{"no refs untouched", "plain-value", "plain-value"},
		{"empty input", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := expandValue(tt.in, look); got != tt.want {
				t.Errorf("expandValue(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
