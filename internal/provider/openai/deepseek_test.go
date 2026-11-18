package openai

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestDeepSeekResumedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, host, mode, wantMode                   string
		tools, stripped, summarized, replay, invalid bool
	}{
		{name: "default", host: "API.DeepSeek.com:443", tools: true, wantMode: "enabled", replay: true},
		{name: "explicit", host: "deepseek.com", mode: "enabled", tools: true, wantMode: "enabled", replay: true},
		{name: "without tools", host: "api.deepseek.com", wantMode: "enabled"},
		{name: "disabled after compaction", host: "api.deepseek.com", mode: "disabled", tools: true, stripped: true, wantMode: "disabled"},
		{name: "resumed summary", host: "api.deepseek.com", tools: true, summarized: true, invalid: true},
		{name: "summary without tools", host: "api.deepseek.com", summarized: true, wantMode: "enabled"},
		{name: "missing trace", host: "api.deepseek.com", tools: true, stripped: true, invalid: true},
		{name: "gateway", host: "deepseek.com.example.org", mode: "enabled", tools: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := session.NewStore(t.TempDir())
			journal, err := store.Create(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			history := []agentapi.Message{
				agentapi.UserMessage("read"),
				{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{agentapi.Thinking("проверка\n<file>\t\x01")}, ToolCalls: []agentapi.ToolCall{{ID: "call", Name: "read", Arguments: json.RawMessage(`{}`)}}},
				agentapi.TextResult("call", "read", "result").Message(),
				{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{agentapi.Thinking(""), agentapi.Text("answer")}},
			}
			for _, m := range history {
				if err := journal.AppendMessage(m); err != nil {
					t.Fatal(err)
				}
			}
			if tc.stripped {
				history[1].Content = nil
				if err := journal.AppendCompaction(agentapi.Compaction{History: history}); err != nil {
					t.Fatal(err)
				}
			}
			if tc.summarized {
				summary := agentapi.SummaryMessage("finished reading")
				if err := journal.AppendCompaction(agentapi.Compaction{MessagesReplaced: len(history), Summary: summary.Text(), History: []agentapi.Message{summary}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := journal.Close(); err != nil {
				t.Fatal(err)
			}
			resumed, err := store.Open(journal.Path())
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resumed.Close(); err != nil {
					t.Error(err)
				}
			}()
			req := agentapi.ChatRequest{Messages: append(resumed.Messages(), agentapi.UserMessage("continue"))}
			if tc.tools {
				req.Tools = []agentapi.ToolSchema{{Name: "read", InputSchema: json.RawMessage(`{"type":"object"}`)}}
			}
			srv, capture := serveFixture(t, "text.sse")
			profile := testProfile("https://" + tc.host + "/v1")
			profile.Thinking = tc.mode
			c := New(profile, WithHTTPClient(srv.Client()))
			c.url = srv.URL
			events, err := c.Stream(t.Context(), req)
			if tc.invalid {
				if err == nil || agentapi.KindOf(err) != agentapi.ErrInvalidRequest || capture.path != "" || !strings.Contains(err.Error(), "thinking: disabled") || !strings.Contains(err.Error(), "new session") {
					t.Fatalf("error=%v request=%q", err, capture.path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			collect(t, events)
			var body struct {
				Thinking map[string]string            `json:"thinking"`
				Messages []map[string]json.RawMessage `json:"messages"`
			}
			if err := json.Unmarshal(capture.body, &body); err != nil {
				t.Fatal(err)
			}
			var wantThinking map[string]string
			if tc.wantMode != "" {
				wantThinking = map[string]string{"type": tc.wantMode}
			}
			if !reflect.DeepEqual(body.Thinking, wantThinking) {
				t.Fatalf("thinking=%v want %v", body.Thinking, wantThinking)
			}
			if len(body.Messages) != len(req.Messages) {
				t.Fatal("lost messages")
			}
			for i, m := range body.Messages {
				got, present := m["reasoning_content"]
				want := req.Messages[i].Thinking()
				if present != (tc.replay && req.Messages[i].Role == agentapi.RoleAssistant) {
					t.Fatalf("message %d reasoning presence=%v", i, present)
				}
				if present {
					var trace *string
					if err := json.Unmarshal(got, &trace); err != nil || trace == nil || *trace != want {
						t.Fatalf("message %d reasoning=%s want %q (decode error: %v)", i, got, want, err)
					}
				}
			}
		})
	}
}

// TestStreamReasoningPresence exercises the SSE decoder and agent message builder together.
func TestStreamReasoningPresence(t *testing.T) {
	for _, tc := range []struct {
		name, host, mode, field string
		present                 bool
		want                    string
	}{
		{name: "missing", host: "api.deepseek.com"},
		{name: "null", host: "api.deepseek.com", field: `,"reasoning_content":null`},
		{name: "explicit empty", host: "api.deepseek.com", field: `,"reasoning_content":""`, present: true},
		{name: "disabled", host: "api.deepseek.com", mode: "disabled", field: `,"reasoning_content":""`},
		{name: "gateway placeholder", host: "gateway.example", field: `,"reasoning_content":""`},
		{name: "gateway alias", host: "gateway.example", field: `,"reasoning_content":"","reasoning":"trace"`, present: true, want: "trace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer\"%s},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", tc.field)
			}))
			defer srv.Close()
			profile := testProfile("https://" + tc.host)
			profile.Thinking = tc.mode
			client := New(profile, WithHTTPClient(srv.Client()))
			client.url = srv.URL
			a := agent.New(client)
			var msg *agentapi.Message
			for ev := range a.RunTurn(t.Context(), []agentapi.Message{agentapi.UserMessage("hello")}) {
				if ev.Kind == agentapi.EventError {
					t.Fatal(ev.Err)
				}
				if ev.Kind == agentapi.EventMessage {
					msg = ev.Message
				}
			}
			if msg == nil {
				t.Fatal("missing assistant message")
			}
			expected := []agentapi.ContentPart{agentapi.Text("answer")}
			if tc.present {
				expected = append([]agentapi.ContentPart{agentapi.Thinking(tc.want)}, expected...)
			}
			if !reflect.DeepEqual(msg.Content, expected) {
				t.Fatalf("parts=%+v want %+v", msg.Content, expected)
			}
		})
	}
}
