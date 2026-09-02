package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/pkg/agentapi"
)

func testProfile(baseURL string) config.Profile {
	return config.Profile{
		Name:    "test",
		Kind:    config.KindOpenAI,
		BaseURL: baseURL,
		APIKey:  "sk-test-1234567890abcdef",
		Model:   "test-model",
	}
}

func simpleRequest() agentapi.ChatRequest {
	return agentapi.ChatRequest{
		Messages: []agentapi.Message{agentapi.UserMessage("hi")},
	}
}

// serveFixture returns a server that replays testdata/<name> as the response
// and records the last request body and headers.
func serveFixture(t *testing.T, name string) (*httptest.Server, *capturedRequest) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	cap := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.record(r)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

type capturedRequest struct {
	path   string
	auth   string
	accept string
	body   []byte
}

func (c *capturedRequest) record(r *http.Request) {
	c.path = r.URL.Path
	c.auth = r.Header.Get("Authorization")
	c.accept = r.Header.Get("Accept")
	body := make([]byte, r.ContentLength)
	_, _ = r.Body.Read(body)
	c.body = body
}

// collect drains the stream within a deadline, guarding against a hung test.
func collect(t *testing.T, ch <-chan agentapi.ChatEvent) []agentapi.ChatEvent {
	t.Helper()
	var events []agentapi.ChatEvent
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timeout:
			t.Fatalf("stream did not close; got %d events so far: %+v", len(events), events)
		}
	}
}

func kinds(events []agentapi.ChatEvent) []agentapi.ChatEventKind {
	out := make([]agentapi.ChatEventKind, 0, len(events))
	for _, ev := range events {
		out = append(out, ev.Kind)
	}
	return out
}

func last(t *testing.T, events []agentapi.ChatEvent) agentapi.ChatEvent {
	t.Helper()
	if len(events) == 0 {
		t.Fatal("stream produced no events")
	}
	return events[len(events)-1]
}

func TestStreamTextWithUsage(t *testing.T) {
	srv, cap := serveFixture(t, "text.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, ch)

	var text strings.Builder
	var usage *agentapi.Usage
	for _, ev := range events {
		switch ev.Kind { //nolint:exhaustive // only collecting text and usage
		case agentapi.ChatTextDelta:
			text.WriteString(ev.Text)
		case agentapi.ChatUsage:
			usage = ev.Usage
		}
	}
	if got := text.String(); got != "Hello, world" {
		t.Errorf("text = %q, want %q", got, "Hello, world")
	}
	if usage == nil {
		t.Fatal("no usage event")
	}
	if usage.InputTokens != 12 || usage.OutputTokens != 4 || usage.CachedInputTokens != 3 {
		t.Errorf("usage = %+v", usage)
	}
	term := last(t, events)
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishStop {
		t.Errorf("terminal = %+v, want done/stop", term)
	}

	// Wire checks: URL, headers, stream options.
	if cap.path != "/chat/completions" {
		t.Errorf("path = %q", cap.path)
	}
	if !strings.HasPrefix(cap.auth, "Bearer sk-test-") {
		t.Errorf("auth = %q", cap.auth)
	}
	if cap.accept != "text/event-stream" {
		t.Errorf("accept = %q", cap.accept)
	}
	var wire map[string]any
	if err := json.Unmarshal(cap.body, &wire); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if wire["stream"] != true {
		t.Error("request is not streaming")
	}
	if wire["model"] != "test-model" {
		t.Errorf("model = %v, want profile default", wire["model"])
	}
	if _, ok := wire["stream_options"]; !ok {
		t.Error("stream_options.include_usage not sent")
	}
}

func TestStreamSplitToolCalls(t *testing.T) {
	srv, _ := serveFixture(t, "tools.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, ch)

	var calls []agentapi.ToolCall
	for _, ev := range events {
		if ev.Kind == agentapi.ChatToolCall {
			calls = append(calls, *ev.Call)
		}
	}
	if len(calls) != 2 {
		t.Fatalf("got %d tool calls, want 2: %+v", len(calls), events)
	}
	if calls[0].ID != "call_abc" || calls[0].Name != "read_file" {
		t.Errorf("calls[0] = %+v", calls[0])
	}
	if string(calls[0].Arguments) != `{"path":"main.go"}` {
		t.Errorf("calls[0].Arguments = %s", calls[0].Arguments)
	}
	if calls[1].ID != "call_def" || string(calls[1].Arguments) != "{}" {
		t.Errorf("calls[1] = %+v args=%s", calls[1], calls[1].Arguments)
	}
	for _, call := range calls {
		if err := call.Validate(); err != nil {
			t.Errorf("emitted invalid call: %v", err)
		}
	}
	term := last(t, events)
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishToolCalls {
		t.Errorf("terminal = %+v, want done/tool_calls", term)
	}
}

func TestStreamReasoningDelta(t *testing.T) {
	srv, _ := serveFixture(t, "reasoning.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, ch)

	want := []agentapi.ChatEventKind{agentapi.ChatThinkingDelta, agentapi.ChatTextDelta, agentapi.ChatDone}
	got := kinds(events)
	if len(got) != len(want) {
		t.Fatalf("kinds = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", got, want)
		}
	}
	if events[0].Text != "thinking hard" {
		t.Errorf("thinking = %q", events[0].Text)
	}
}

func TestStreamFinishLength(t *testing.T) {
	srv, _ := serveFixture(t, "length.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	term := last(t, collect(t, ch))
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishLength {
		t.Errorf("terminal = %+v, want done/length", term)
	}
}

func TestStreamInlineErrorEvent(t *testing.T) {
	srv, _ := serveFixture(t, "error_event.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	term := last(t, collect(t, ch))
	if term.Kind != agentapi.ChatError {
		t.Fatalf("terminal = %+v, want error", term)
	}
	if term.Err.Kind != agentapi.ErrUnavailable {
		t.Errorf("kind = %s, want unavailable", term.Err.Kind)
	}
	if !strings.Contains(term.Err.Message, "upstream exploded") {
		t.Errorf("message = %q", term.Err.Message)
	}
	if term.Err.Provider != "test" {
		t.Errorf("provider = %q", term.Err.Provider)
	}
}

func TestStreamTruncatedToolArguments(t *testing.T) {
	srv, _ := serveFixture(t, "truncated_tool.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, ch)
	for _, ev := range events {
		if ev.Kind == agentapi.ChatToolCall {
			t.Fatalf("half-built tool call escaped: %+v", ev.Call)
		}
	}
	term := last(t, events)
	if term.Kind != agentapi.ChatError {
		t.Fatalf("terminal = %+v, want error", term)
	}
}

func TestStreamHTTPStatuses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		headers   map[string]string
		wantKind  agentapi.ErrorKind
		wantRetry int
		wantInMsg string
	}{
		{
			name: "401 auth", status: 401,
			body:     `{"error":{"message":"bad key","code":"invalid_api_key"}}`,
			wantKind: agentapi.ErrAuth, wantInMsg: "bad key",
		},
		{
			name: "429 with retry-after", status: 429,
			body:      `{"error":{"message":"slow down"}}`,
			headers:   map[string]string{"Retry-After": "7"},
			wantKind:  agentapi.ErrRateLimited,
			wantRetry: 7,
		},
		{
			name: "500", status: 500, body: "internal error",
			wantKind: agentapi.ErrUnavailable,
		},
		{
			name: "404 model", status: 404,
			body:     `{"error":{"message":"model not found"}}`,
			wantKind: agentapi.ErrModelNotFound,
		},
		{
			name: "400 invalid", status: 400,
			body:     `{"error":{"message":"unknown field"}}`,
			wantKind: agentapi.ErrInvalidRequest,
		},
		{
			name: "400 context overflow", status: 400,
			body:     `{"error":{"message":"This model's maximum context length is 8192 tokens","code":"context_length_exceeded"}}`,
			wantKind: agentapi.ErrContextOverflow,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

			_, err := c.Stream(context.Background(), simpleRequest())
			if err == nil {
				t.Fatal("Stream succeeded, want error")
			}
			var e *agentapi.Error
			if !asAgentError(err, &e) {
				t.Fatalf("error is not *agentapi.Error: %v", err)
			}
			if e.Kind != tt.wantKind {
				t.Errorf("kind = %s, want %s", e.Kind, tt.wantKind)
			}
			if e.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", e.StatusCode, tt.status)
			}
			if tt.wantRetry != 0 && e.RetryAfterSeconds != tt.wantRetry {
				t.Errorf("retry-after = %d, want %d", e.RetryAfterSeconds, tt.wantRetry)
			}
			if tt.wantInMsg != "" && !strings.Contains(e.Message, tt.wantInMsg) {
				t.Errorf("message = %q, want containing %q", e.Message, tt.wantInMsg)
			}
			if e.Provider != "test" {
				t.Errorf("provider = %q, want test", e.Provider)
			}
		})
	}
}

func TestStreamConnectionRefused(t *testing.T) {
	// A closed server: the URL is valid but nothing listens.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	c := New(testProfile(url))
	_, err := c.Stream(context.Background(), simpleRequest())
	if err == nil {
		t.Fatal("Stream succeeded against a dead server")
	}
	var e *agentapi.Error
	if !asAgentError(err, &e) || e.Kind != agentapi.ErrNetwork {
		t.Fatalf("err = %v, want network kind", err)
	}
}

func TestStreamCancelBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv, _ := serveFixture(t, "text.sse")
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))

	_, err := c.Stream(ctx, simpleRequest())
	if err == nil {
		t.Fatal("Stream succeeded with a dead context")
	}
	if kind := agentapi.KindOf(err); kind != agentapi.ErrCanceled {
		t.Errorf("kind = %s, want canceled", kind)
	}
}

func TestStreamCancelMidStream(t *testing.T) {
	firstDelta := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		f.Flush()
		close(firstDelta)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))
	ch, err := c.Stream(ctx, simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	var events []agentapi.ChatEvent
	timeout := time.After(10 * time.Second)
	for {
		var ev agentapi.ChatEvent
		var ok bool
		select {
		case ev, ok = <-ch:
		case <-timeout:
			t.Fatal("stream did not close after cancel")
		}
		if !ok {
			break
		}
		events = append(events, ev)
		if ev.Kind == agentapi.ChatTextDelta {
			<-firstDelta
			cancel()
		}
	}
	term := last(t, events)
	// Deltas were delivered, so cancellation ends the stream as a
	// user-facing cancel, not a failure (pinned decision, see plan).
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishCanceled {
		t.Errorf("terminal = %+v, want done/canceled", term)
	}
}

func TestStreamStallTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"then silence\"}}]}\n\n"))
		f.Flush()
		<-release
	}))
	// LIFO: release the handler before srv.Close waits on the connection.
	defer srv.Close()
	defer close(release)

	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()), WithIdleTimeout(100*time.Millisecond))
	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	term := last(t, collect(t, ch))
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrTimeout {
		t.Errorf("terminal = %+v, want error/timeout", term)
	}
}

func TestStreamEmptyBodyIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// 200 with an immediately closed empty body.
	}))
	defer srv.Close()

	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))
	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	term := last(t, collect(t, ch))
	if term.Kind != agentapi.ChatError || term.Err.Kind != agentapi.ErrUnavailable {
		t.Errorf("terminal = %+v, want error/unavailable", term)
	}
}

func TestCompletionsURLNotDoubled(t *testing.T) {
	tests := []struct {
		base string
		want string
	}{
		{"https://api-bar.ru/route/openai", "https://api-bar.ru/route/openai/chat/completions"},
		{"https://api-bar.ru/route/openai/", "https://api-bar.ru/route/openai/chat/completions"},
		{"http://localhost:11434/v1", "http://localhost:11434/v1/chat/completions"},
		{"https://x.example/v1/chat/completions", "https://x.example/v1/chat/completions"},
	}
	for _, tt := range tests {
		if got := completionsURL(tt.base); got != tt.want {
			t.Errorf("completionsURL(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}

func TestLoggerMasksAPIKey(t *testing.T) {
	srv, _ := serveFixture(t, "text.sse")
	var lines []string
	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()), WithLogger(func(s string) {
		lines = append(lines, s)
	}))

	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	collect(t, ch)

	if len(lines) == 0 {
		t.Fatal("logger received nothing")
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "sk-test-1234567890abcdef") {
		t.Fatal("raw API key leaked into debug log")
	}
	if !strings.Contains(joined, config.Mask("sk-test-1234567890abcdef")) {
		t.Error("masked key hint missing from debug log")
	}
}

func TestStreamInvalidRequestRejectedLocally(t *testing.T) {
	c := New(testProfile("http://unused.invalid"))
	_, err := c.Stream(context.Background(), agentapi.ChatRequest{})
	if err == nil {
		t.Fatal("empty request accepted")
	}
	if kind := agentapi.KindOf(err); kind != agentapi.ErrInvalidRequest {
		t.Errorf("kind = %s, want invalid_request", kind)
	}
}

func TestStreamLargeToolArgumentLine(t *testing.T) {
	// One data: line larger than bufio.Scanner's 64KiB default cap.
	big := strings.Repeat("x", 100*1024)
	payload := `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_big","function":{"name":"write_file","arguments":"{\"content\":\"` + big + `\"}"}}]}}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + payload + "\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()

	c := New(testProfile(srv.URL), WithHTTPClient(srv.Client()))
	ch, err := c.Stream(context.Background(), simpleRequest())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(t, ch)
	var gotCall bool
	for _, ev := range events {
		if ev.Kind == agentapi.ChatToolCall {
			gotCall = true
			if len(ev.Call.Arguments) < 100*1024 {
				t.Error("large arguments truncated")
			}
		}
	}
	if !gotCall {
		t.Fatalf("no tool call: %v", kinds(events))
	}
}
