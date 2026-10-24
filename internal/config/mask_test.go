package config

import (
	"fmt"
	"strings"
	"testing"
)

func TestMask(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty stays empty", "", ""},
		{"short is fully hidden", "sk-short", "***"},
		{"long keeps the ends", "sk-abcdef1234567890xyz", "sk-abc…xyz"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Mask(tt.in); got != tt.want {
				t.Errorf("Mask(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}

	t.Run("never contains the full secret", func(t *testing.T) {
		secret := "sk-abcdef1234567890xyz"
		if strings.Contains(Mask(secret), secret) {
			t.Error("Mask output contains the full secret")
		}
	})
}

func TestRedact(t *testing.T) {
	secrets := []string{"key-one-1234", "key-two-5678", "", "short"}
	in := "url?a=key-one-1234&b=key-two-5678 and key-one-1234 again, a short word"
	got := Redact(in, secrets)
	if strings.Contains(got, "key-one-1234") || strings.Contains(got, "key-two-5678") {
		t.Errorf("Redact left a secret in %q", got)
	}
	// A secret shorter than eight characters is not one, and replacing
	// it would eat ordinary words.
	if got != "url?a=***&b=*** and *** again, a short word" {
		t.Errorf("Redact = %q", got)
	}
}

func TestProfileStringMasksKey(t *testing.T) {
	p := Profile{
		Name:    "api-bar",
		Kind:    KindOpenAI,
		BaseURL: "https://api-bar.ru/route/openai",
		APIKey:  "sk-verysecretkey1234567890",
		Model:   "gpt-4o",
	}
	for _, format := range []string{"%v", "%+v", "%s"} {
		out := fmt.Sprintf(format, p)
		if strings.Contains(out, p.APIKey) {
			t.Errorf("fmt.Sprintf(%q, profile) leaks the key: %s", format, out)
		}
	}
}
