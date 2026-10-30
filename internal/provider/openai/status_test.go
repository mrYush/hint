package openai

import "testing"

func TestErrorMessageShapes(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"openai object", `{"error":{"message":"bad key","code":"invalid_api_key"}}`, "bad key (invalid_api_key)"},
		{"gateway string", `{"error": "Unauthorized"}`, "Unauthorized"},
		{"gateway message", `{"message":"Forbidden"}`, "Forbidden"},
		{"fastapi detail", `{"detail":"Access denied"}`, "Access denied"},
		{"nestjs message and error", `{"statusCode":401,"message":"Invalid API key","error":"Unauthorized"}`, "Invalid API key (Unauthorized)"},
		{"message equals error", `{"message":"Unauthorized","error":"Unauthorized"}`, "Unauthorized"},
		{"message repeats error ignoring case", `{"message":"unauthorized","error":"UNAUTHORIZED"}`, "unauthorized"},
		{"trimmed message and error", `{"message":" Invalid API key ","error":" Unauthorized "}`, "Invalid API key (Unauthorized)"},
		{"short message still leads", `{"message":"no","error":"The API key is invalid"}`, "no (The API key is invalid)"},
		{"blank message falls through to detail", `{"message":"  ","detail":"Access denied","error":"Forbidden"}`, "Access denied (Forbidden)"},
		{"detail with error", `{"error":"Forbidden","detail":"Access denied"}`, "Access denied (Forbidden)"},
		{"plain text", "  upstream timed out \n", "upstream timed out"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		if got := errorMessage([]byte(c.body)); got != c.want {
			t.Errorf("%s: errorMessage(%q) = %q, want %q", c.name, c.body, got, c.want)
		}
	}
}
