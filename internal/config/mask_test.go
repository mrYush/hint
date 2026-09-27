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
	secrets := []string{"key-one", "key-two", ""}
	in := "url?a=key-one&b=key-two and key-one again"
	got := Redact(in, secrets)
	if strings.Contains(got, "key-one") || strings.Contains(got, "key-two") {
		t.Errorf("Redact left a secret in %q", got)
	}
	if got != "url?a=***&b=*** and *** again" {
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
