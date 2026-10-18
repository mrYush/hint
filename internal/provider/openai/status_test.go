package openai

import "testing"

func TestErrorMessageShapes(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"openai object", `{"error":{"message":"bad key","code":"invalid_api_key"}}`, "bad key (invalid_api_key)"},
		{"gateway string", `{"error": "Unauthorized"}`, "Unauthorized"},
		{"gateway message", `{"message":"Forbidden"}`, "Forbidden"},
		{"fastapi detail", `{"detail":"Access denied"}`, "Access denied"},
		{"plain text", "  upstream timed out \n", "upstream timed out"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := errorMessage([]byte(c.body)); got != c.want {
			t.Errorf("%s: errorMessage(%q) = %q, want %q", c.name, c.body, got, c.want)
		}
	}
}
