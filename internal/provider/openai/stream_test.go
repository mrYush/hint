package openai

import (
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

// feedAll pushes lines through a decoder and returns everything it produced.
func feedAll(d *decoder, lines ...string) []agentapi.ChatEvent {
	var events []agentapi.ChatEvent
	for _, line := range lines {
		evs, done := d.feedLine(line)
		events = append(events, evs...)
		if done {
			break
		}
	}
	return events
}

func TestDecoderJoinsMultiLineData(t *testing.T) {
	// The SSE grammar splits one record across several data: lines, joined
	// with newlines at the blank-line boundary. JSON tolerates the inserted
	// newline between tokens.
	d := &decoder{}
	events := feedAll(d,
		`data: {"choices":[{"index":0,`,
		`data: "delta":{"content":"joined"}}]}`,
		``,
	)
	if len(events) != 1 || events[0].Text != "joined" {
		t.Fatalf("events = %+v, want one delta %q", events, "joined")
	}
}

func TestDecoderToleratesBareJSONLines(t *testing.T) {
	// Some servers stream plain JSON lines without SSE framing.
	d := &decoder{}
	events := feedAll(d,
		`{"choices":[{"index":0,"delta":{"content":"bare"}}]}`,
		``,
	)
	if len(events) != 1 || events[0].Text != "bare" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDecoderSkipsCommentsAndOtherFields(t *testing.T) {
	d := &decoder{}
	events := feedAll(d,
		`: keep-alive ping`,
		`event: chunk`,
		`id: 42`,
		`data: {"choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		``,
	)
	if len(events) != 1 || events[0].Text != "ok" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDecoderSkipsMalformedChunk(t *testing.T) {
	d := &decoder{}
	events := feedAll(d,
		`data: {not json`,
		``,
		`data: {"choices":[{"index":0,"delta":{"content":"survived"}}]}`,
		``,
	)
	if len(events) != 1 || events[0].Text != "survived" {
		t.Fatalf("events = %+v", events)
	}
}

func TestDecoderFinalRecordWithoutTrailingBlankLine(t *testing.T) {
	d := &decoder{}
	feedAll(d,
		`data: {"choices":[{"index":0,"delta":{"content":"a"}}]}`,
		``,
	)
	// The finish_reason record arrives with no trailing blank line before
	// EOF; finalize must still parse it.
	d.feedLine(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	events, err := d.finalize(true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishStop {
		t.Errorf("terminal = %+v", term)
	}
}

func TestDecoderMissingFinishReasonWithOutput(t *testing.T) {
	d := &decoder{}
	feedAll(d,
		`data: {"choices":[{"index":0,"delta":{"content":"partial answer"}}]}`,
		``,
	)
	events, err := d.finalize(true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	term := events[len(events)-1]
	if term.Kind != agentapi.ChatDone || term.FinishReason != agentapi.FinishStop {
		t.Errorf("terminal = %+v, want done/stop", term)
	}
}

func TestDecoderIDKeyedToolFragments(t *testing.T) {
	// Copilot-style dialect: no index field, fragments correlated by id.
	d := &decoder{}
	feedAll(d,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_a","function":{"name":"grep","arguments":"{\"q\":"}}]}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_a","function":{"arguments":"\"x\"}"}}]}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
	)
	events, err := d.finalize(true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	var calls []agentapi.ToolCall
	for _, ev := range events {
		if ev.Kind == agentapi.ChatToolCall {
			calls = append(calls, *ev.Call)
		}
	}
	if len(calls) != 1 {
		t.Fatalf("calls = %+v", calls)
	}
	if calls[0].ID != "call_a" || string(calls[0].Arguments) != `{"q":"x"}` {
		t.Errorf("call = %+v args=%s", calls[0], calls[0].Arguments)
	}
}

func TestDecoderSynthesizesMissingCallID(t *testing.T) {
	d := &decoder{}
	feedAll(d,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"ping","arguments":"{}"}}]}}]}`,
		``,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		``,
	)
	events, err := d.finalize(true)
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	for _, ev := range events {
		if ev.Kind == agentapi.ChatToolCall {
			if ev.Call.ID == "" {
				t.Error("call escaped without an id")
			}
			if err := ev.Call.Validate(); err != nil {
				t.Errorf("invalid call: %v", err)
			}
			return
		}
	}
	t.Fatalf("no tool call in %+v", events)
}

func TestDecoderFinishReasonMapping(t *testing.T) {
	// Exhaustive over the dialect spellings, mirroring the ErrorKind routing
	// test: an unmapped upstream value must never surface raw.
	tests := map[string]agentapi.FinishReason{
		"stop":           agentapi.FinishStop,
		"end_turn":       agentapi.FinishStop,
		"tool_calls":     agentapi.FinishToolCalls,
		"function_call":  agentapi.FinishToolCalls,
		"length":         agentapi.FinishLength,
		"max_tokens":     agentapi.FinishLength,
		"content_filter": agentapi.FinishContentFilter,
		"weird_future":   agentapi.FinishStop,
	}
	for raw, want := range tests {
		if got := mapFinishReason(raw); got != want {
			t.Errorf("mapFinishReason(%q) = %s, want %s", raw, got, want)
		}
		if !mapFinishReason(raw).Valid() {
			t.Errorf("mapFinishReason(%q) produced an invalid reason", raw)
		}
	}
}

func TestDecoderEmptyChoicesChunks(t *testing.T) {
	// Azure prompt-filter annotations and usage-only chunks have no choices;
	// they must not panic or produce events.
	d := &decoder{}
	events := feedAll(d,
		`data: {"choices":[],"prompt_filter_results":[{"prompt_index":0}]}`,
		``,
		`data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1}}`,
		``,
	)
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
	if d.usage == nil || d.usage.InputTokens != 5 {
		t.Errorf("usage = %+v", d.usage)
	}
}
