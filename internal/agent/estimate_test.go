package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestOccupancyAccountsForToolCatalog(t *testing.T) {
	a := &Agent{estimator: charEstimator{}}
	messages := []agentapi.Message{
		agentapi.UserMessage("12345678"),
		agentapi.AssistantMessage("abcdefgh"),
	}
	for _, tt := range []struct {
		name     string
		baseline usageBaseline
		tools    int64
		want     int64
	}{
		{name: "cold", tools: 30, want: 34},
		{name: "warm unchanged", baseline: usageBaseline{100, 1, 30}, tools: 30, want: 102},
		{name: "warm growth", baseline: usageBaseline{100, 1, 30}, tools: 60, want: 132},
		{name: "warm shrink", baseline: usageBaseline{100, 1, 30}, tools: 10, want: 82},
		{name: "shrink stays nonnegative", baseline: usageBaseline{10, 1, 50}, tools: 0, want: 0},
		{name: "missing usage", baseline: usageBaseline{0, 1, 30}, tools: 60, want: 64},
		{name: "negative baseline", baseline: usageBaseline{100, -1, 30}, tools: 60, want: 64},
		{name: "shortened history", baseline: usageBaseline{100, 3, 30}, tools: 60, want: 64},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.occupancy(messages, tt.tools, tt.baseline); got != tt.want {
				t.Errorf("occupancy = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCharEstimatorCountsCatalogFields(t *testing.T) {
	e := charEstimator{}
	if got := e.EstimateTools(nil); got != 0 {
		t.Errorf("empty catalog = %d, want 0", got)
	}
	base := agentapi.ToolSchema{Name: "map", InputSchema: json.RawMessage(`{"type":"object"}`)}
	baseCost := e.EstimateTools([]agentapi.ToolSchema{base})
	if baseCost <= 0 {
		t.Fatalf("nonempty catalog costs %d tokens", baseCost)
	}
	for _, field := range []string{"name", "description", "input_schema"} {
		t.Run(field, func(t *testing.T) {
			schema := base
			switch field {
			case "name":
				schema.Name = strings.Repeat("n", 1024)
			case "description":
				schema.Description = strings.Repeat("д", 1024)
			case "input_schema":
				schema.InputSchema = json.RawMessage(`{"description":"` + strings.Repeat("s", 1024) + `"}`)
			}
			if got := e.EstimateTools([]agentapi.ToolSchema{schema}); got < baseCost+250 {
				t.Errorf("catalog with a large %s costs %d, base = %d", field, got, baseCost)
			}
		})
	}
	base.InputSchema = json.RawMessage(strings.Repeat("invalid", 1024))
	if got := e.EstimateTools([]agentapi.ToolSchema{base}); got < 1000 {
		t.Errorf("malformed raw schema was treated as free: %d", got)
	}
}

func TestMessageOnlyEstimatorStillCountsTools(t *testing.T) {
	a := &Agent{estimator: thinkingAwareEstimator{small: 1}}
	tools := []agentapi.ToolSchema{{Name: "map", Description: strings.Repeat("x", 1024), InputSchema: json.RawMessage(`{}`)}}
	if got := a.estimateTools(tools); got < 256 {
		t.Errorf("fallback catalog estimate = %d, want at least 256", got)
	}
}

func TestCompactThinkingShortcutCountsTools(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY"}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 100, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: thinkingAwareEstimator{big: 1000, small: 10},
	}
	messages := []agentapi.Message{
		agentapi.UserMessage("previous question"),
		{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{
			agentapi.Thinking("long reasoning"), agentapi.Text("previous answer"),
		}},
		agentapi.UserMessage("current question"),
	}
	out := make(chan agentapi.Event, 1)
	_, err := a.compact(t.Context(), messages, 75, out)
	close(out)
	if err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compactor.calls != 1 {
		t.Errorf("compactor called %d times; dropping thinking leaves 85 tokens and must not take the shortcut", compactor.calls)
	}
	event := <-out
	if event.Compaction == nil || event.Compaction.TokensBefore != 1075 || event.Compaction.TokensAfter != 85 {
		t.Errorf("compaction = %+v, want estimated sizes including 75 tool tokens", event.Compaction)
	}
}

func TestCompactReservesToolCatalogBeforeSplittingTail(t *testing.T) {
	compactor := &stubCompactor{summary: "SUMMARY"}
	a := &Agent{
		limits:    Limits{MaxContextTokens: 100, CompactThreshold: 0.8},
		compactor: compactor,
		estimator: charEstimator{},
	}
	messages := []agentapi.Message{agentapi.UserMessage("question")}
	for _, id := range []string{"c1", "c2"} {
		messages = append(messages,
			agentapi.Message{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{{ID: id, Name: "echo", Arguments: json.RawMessage(`{}`)}}},
			agentapi.TextResult(id, "echo", strings.Repeat("x", 32)).Message(),
		)
	}
	out := make(chan agentapi.Event, 1)
	result, err := a.compact(t.Context(), messages, 80, out)
	close(out)
	if err != nil {
		t.Fatalf("compact must release an older tool round from the tail after reserving catalog space: %v", err)
	}
	if compactor.calls != 1 || len(compactor.got) != 2 || compactor.got[1].ToolCallID != "c1" {
		t.Fatalf("compactor received %+v, want the first assistant/tool pair", compactor.got)
	}
	if len(result) != 4 || !result[0].Summary || result[1].Text() != "question" || result[3].ToolCallID != "c2" {
		t.Errorf("compacted tail = %+v, want summary + current question + last complete tool pair", result)
	}
}
