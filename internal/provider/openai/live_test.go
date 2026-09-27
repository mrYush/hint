//go:build live

package openai

// Live matrix against real endpoints. Not part of the default test run:
//
//	go test -tags live ./internal/provider/openai/
//
// Endpoints are selected by environment (see testdata/README.md):
// API_BAR_KEY for the api-bar gateway, OPENAI_API_KEY for api.openai.com,
// and an Ollama daemon on localhost:11434 (skipped when not reachable).

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider/ollama"
	"github.com/mrYush/hint/pkg/agentapi"
)

func liveProfiles(t *testing.T) []config.Profile {
	t.Helper()
	var profiles []config.Profile
	if key := os.Getenv("API_BAR_KEY"); key != "" {
		profiles = append(profiles, config.Profile{
			Name: "api-bar", Kind: config.KindOpenAI,
			BaseURL: "https://api-bar.ru/route/openai", APIKey: key, Model: "gpt-4o",
		})
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		profiles = append(profiles, config.Profile{
			Name: "openai", Kind: config.KindOpenAI,
			BaseURL: "https://api.openai.com/v1", APIKey: key, Model: "gpt-4o-mini",
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := ollama.New("http://localhost:11434/v1").Ready(ctx); err == nil {
		model := os.Getenv("HINT_LIVE_OLLAMA_MODEL")
		if model == "" {
			model = "qwen2.5:7b"
		}
		profiles = append(profiles, config.Profile{
			Name: "ollama", Kind: config.KindOllama,
			BaseURL: "http://localhost:11434/v1", Model: model,
		})
	}
	if len(profiles) == 0 {
		t.Skip("no live endpoints configured (API_BAR_KEY / OPENAI_API_KEY / local Ollama)")
	}
	return profiles
}

func TestLiveStreamingText(t *testing.T) {
	for _, p := range liveProfiles(t) {
		t.Run(p.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			c := New(p)
			ch, err := c.Stream(ctx, agentapi.ChatRequest{
				Messages: []agentapi.Message{agentapi.UserMessage("Reply with the single word: pong")},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			var text strings.Builder
			var term agentapi.ChatEvent
			deltas := 0
			for ev := range ch {
				switch ev.Kind {
				case agentapi.ChatTextDelta:
					deltas++
					text.WriteString(ev.Text)
				}
				if ev.Terminal() {
					term = ev
				}
			}
			if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishStop {
				t.Fatalf("terminal = %+v", term)
			}
			if deltas == 0 || !strings.Contains(strings.ToLower(text.String()), "pong") {
				t.Errorf("deltas=%d text=%q", deltas, text.String())
			}
		})
	}
}

func TestLiveToolCallRoundTrip(t *testing.T) {
	weatherTool := agentapi.ToolSchema{
		Name:        "get_weather",
		Description: "Get the current weather for a city",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
	}
	for _, p := range liveProfiles(t) {
		t.Run(p.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			c := New(p)

			// Turn 1: the model must decide to call the tool.
			ch, err := c.Stream(ctx, agentapi.ChatRequest{
				Messages: []agentapi.Message{agentapi.UserMessage("What is the weather in Paris? Use the tool.")},
				Tools:    []agentapi.ToolSchema{weatherTool},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			var call *agentapi.ToolCall
			var term agentapi.ChatEvent
			for ev := range ch {
				if ev.Kind == agentapi.ChatToolCall {
					call = ev.Call
				}
				if ev.Terminal() {
					term = ev
				}
			}
			if call == nil {
				t.Fatalf("no tool call; terminal = %+v", term)
			}
			if err := call.Validate(); err != nil {
				t.Fatalf("invalid call: %v", err)
			}

			// Turn 2: feed the result back and expect a text answer.
			ch, err = c.Stream(ctx, agentapi.ChatRequest{
				Messages: []agentapi.Message{
					agentapi.UserMessage("What is the weather in Paris? Use the tool."),
					{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{*call}},
					agentapi.TextResult(call.ID, call.Name, `{"temp_c":21,"sky":"clear"}`).Message(),
				},
				Tools: []agentapi.ToolSchema{weatherTool},
			})
			if err != nil {
				t.Fatalf("Stream (turn 2): %v", err)
			}
			var answer strings.Builder
			for ev := range ch {
				if ev.Kind == agentapi.ChatTextDelta {
					answer.WriteString(ev.Text)
				}
				if ev.Terminal() {
					term = ev
				}
			}
			if term.Kind != agentapi.ChatDone {
				t.Fatalf("terminal = %+v", term)
			}
			if answer.Len() == 0 {
				t.Error("no text answer after tool result")
			}
		})
	}
}
