package config

import "testing"

func TestDeepSeekThinking(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ baseURL, mode, want string }{
		{"https://deepseek.com", "", ThinkingEnabled},
		{"https://API.DeepSeek.COM/v1", "", ThinkingEnabled},
		{"https://api.deepseek.com:8443/v1", ThinkingDisabled, ThinkingDisabled},
		{"https://deepseek.com./v1", ThinkingEnabled, ThinkingEnabled},
		{"https://deepseek.com.example.org", ThinkingEnabled, ""},
		{"https://notdeepseek.com", "", ""},
		{"https://gateway.example/deepseek.com", "", ""},
		{"https://%zz", "", ""},
	} {
		t.Run(tc.baseURL, func(t *testing.T) {
			p := Profile{BaseURL: tc.baseURL, Thinking: tc.mode}
			if got := p.DeepSeekThinking(); got != tc.want {
				t.Errorf("DeepSeekThinking() = %q, want %q", got, tc.want)
			}
		})
	}
}
