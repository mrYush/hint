package agent_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/agent"
	"github.com/mrYush/hint/pkg/agentapi"
)

// catalogEstimator gives tests independent control of message and schema
// costs. One description byte is one token; each message is one token.
type catalogEstimator struct{}

func (catalogEstimator) Estimate(messages []agentapi.Message) int64 { return int64(len(messages)) }

func (catalogEstimator) EstimateTools(tools []agentapi.ToolSchema) int64 {
	var tokens int64
	for _, tool := range tools {
		tokens += int64(len(tool.Description))
	}
	return tokens
}

type changingCatalogTool struct {
	echoTool
	description string
	schema      json.RawMessage
	schemaReads int
	afterRun    func()
}

func (t *changingCatalogTool) Description() string { return t.description }

func (t *changingCatalogTool) InputSchema() json.RawMessage {
	t.schemaReads++
	return t.schema
}

func (t *changingCatalogTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	result, err := t.echoTool.Run(ctx, callID, args)
	if t.afterRun != nil {
		t.afterRun()
	}
	return result, err
}

func TestRunTurnColdCatalogTriggersCompaction(t *testing.T) {
	tool := &changingCatalogTool{description: strings.Repeat("x", 90), schema: json.RawMessage(`{}`)}
	compactor := &stubCompactor{summary: "SUMMARY"}
	p := &fakeProvider{scripts: []script{{events: []agentapi.ChatEvent{done(agentapi.FinishStop)}}}}
	a := agent.New(p, agent.WithTools(tool), agent.WithCompactor(compactor), agent.WithEstimator(catalogEstimator{}),
		agent.WithLimits(agent.Limits{MaxIterations: 3, MaxContextTokens: 100, CompactThreshold: 0.8}))
	events := collect(t, a.RunTurn(t.Context(), history4()))
	if compactor.calls != 1 || len(p.calls) != 1 || !p.calls[0].Messages[1].Summary {
		t.Fatalf("cold request omitted catalog cost: compactions=%d, requests=%+v", compactor.calls, p.calls)
	}
	if len(p.calls[0].Tools) != 1 || p.calls[0].Tools[0].Description != tool.description || tool.schemaReads != 1 {
		t.Fatalf("provider did not receive the estimated snapshot: tools=%+v, reads=%d", p.calls[0].Tools, tool.schemaReads)
	}
	if comp := lastEvent(events, agentapi.EventCompaction); comp == nil || comp.Compaction.TokensBefore != 94 {
		t.Errorf("compaction = %+v, want cold occupancy of 4 messages + 90 schema tokens", comp)
	}
}

func TestRunTurnWarmCatalogAccounting(t *testing.T) {
	for _, tt := range []struct {
		name        string
		before      int
		after       int
		usage       int64
		preamble    bool
		compactions int
	}{
		{name: "unchanged is not counted twice", before: 50, after: 50, usage: 60},
		{name: "growth adjusts reported usage", before: 10, after: 40, usage: 50, compactions: 1},
		{name: "shrink adjusts reported usage", before: 50, after: 10, usage: 90},
		{name: "prefix change invalidates usage", before: 10, after: 40, usage: 90, preamble: true},
		{name: "absent usage counts full catalog", before: 10, after: 90, usage: 0, compactions: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tool := &changingCatalogTool{description: strings.Repeat("x", tt.before), schema: json.RawMessage(`{}`)}
			tool.afterRun = func() { tool.description = strings.Repeat("y", tt.after) }
			compactor := &stubCompactor{summary: "SUMMARY"}
			p := &fakeProvider{scripts: []script{
				{events: []agentapi.ChatEvent{toolCallEvent("c1", "echo", `{"text":"x"}`), usageEvent(tt.usage), done(agentapi.FinishToolCalls)}},
				{events: []agentapi.ChatEvent{done(agentapi.FinishStop)}},
			}}
			opts := []agent.Option{agent.WithTools(tool), agent.WithCompactor(compactor), agent.WithEstimator(catalogEstimator{}),
				agent.WithLimits(agent.Limits{MaxIterations: 3, MaxContextTokens: 100, CompactThreshold: 0.8})}
			if tt.preamble {
				opts = append(opts, agent.WithPreamble(&shiftingPreamble{}))
			}
			events := collect(t, agent.New(p, opts...).RunTurn(t.Context(), history4()))
			if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
				t.Fatalf("turn did not finish: %+v", end)
			}
			if compactor.calls != tt.compactions {
				t.Errorf("compactor called %d times, want %d", compactor.calls, tt.compactions)
			}
			if len(p.calls) != 2 || tool.schemaReads != 2 {
				t.Fatalf("requests=%d, schema reads=%d, want 2 of each", len(p.calls), tool.schemaReads)
			}
			if p.calls[0].Tools[0].Description != strings.Repeat("x", tt.before) || p.calls[1].Tools[0].Description != strings.Repeat("y", tt.after) {
				t.Errorf("provider catalogs do not match each round's estimate: %+v", p.calls)
			}
		})
	}
}

type callbackCompactor struct{ mutate func() }

func (c callbackCompactor) Compact(context.Context, []agentapi.Message) (string, error) {
	c.mutate()
	return "SUMMARY", nil
}

func TestRunTurnReactiveRetryPreservesCatalogSnapshot(t *testing.T) {
	before := json.RawMessage(`{"type":"object","title":"before"}`)
	tool := &changingCatalogTool{description: "original", schema: append(json.RawMessage(nil), before...)}
	compactor := callbackCompactor{mutate: func() {
		tool.description = "replacement"
		copy(tool.schema, `{"type":"object","title":"after_"}`)
	}}
	p := &fakeProvider{scripts: []script{
		{events: []agentapi.ChatEvent{failure(agentapi.ErrContextOverflow)}},
		{events: []agentapi.ChatEvent{done(agentapi.FinishStop)}},
	}}
	a := agent.New(p, agent.WithTools(tool), agent.WithCompactor(compactor),
		agent.WithLimits(agent.Limits{MaxIterations: 3, MaxContextTokens: 0}))
	events := collect(t, a.RunTurn(t.Context(), history4()))
	if end := lastEvent(events, agentapi.EventTurnEnd); end == nil || end.FinishReason != agentapi.FinishStop {
		t.Fatalf("turn did not finish after retry: %+v", end)
	}
	if len(p.calls) != 2 || tool.schemaReads != 1 {
		t.Fatalf("requests=%d, schema reads=%d; a retry must reuse one snapshot", len(p.calls), tool.schemaReads)
	}
	want := []agentapi.ToolSchema{{Name: "echo", Description: "original", InputSchema: before}}
	for i, req := range p.calls {
		if !reflect.DeepEqual(req.Tools, want) {
			t.Errorf("request %d catalog = %+v, want original snapshot %+v", i, req.Tools, want)
		}
	}
	collect(t, a.RunTurn(t.Context(), question("continue")))
	if tool.schemaReads != 2 || p.calls[2].Tools[0].Description != "replacement" || string(p.calls[2].Tools[0].InputSchema) != string(tool.schema) {
		t.Errorf("next turn did not refresh the catalog: reads=%d, request=%+v", tool.schemaReads, p.calls[2])
	}
}
