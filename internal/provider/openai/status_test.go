package openai

import (
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestErrorMessageShapes(t *testing.T) {
	t.Parallel()
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

func TestStatusErrorRetryAfterHTTPDate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		offset time.Duration
		want   int
	}{
		{"future", 7 * time.Second, 7},
		{"past", -time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				resp := &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Status:     "429 Too Many Requests",
					Header:     make(http.Header),
					Body:       http.NoBody,
				}
				resp.Header.Set("Retry-After", time.Now().Add(tc.offset).UTC().Format(http.TimeFormat))
				got := statusError(resp)
				if got.Kind != agentapi.ErrRateLimited || got.RetryAfterSeconds != tc.want {
					t.Fatalf("statusError = %v with RetryAfterSeconds = %d, want rate limit with %d seconds", got.Kind, got.RetryAfterSeconds, tc.want)
				}
			})
		})
	}
}
