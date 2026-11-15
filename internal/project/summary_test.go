package project_test

import (
	"context"
	"testing"

	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider/router"
	"github.com/mrYush/hint/pkg/agentapi"
)

type summaryProvider struct {
	name string
	call func() (string, error)
}

func (p summaryProvider) Name() string { return p.name }

func (p summaryProvider) Stream(context.Context, agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	text, err := p.call()
	if err != nil {
		return nil, err
	}
	ch := make(chan agentapi.ChatEvent, 2)
	ch <- agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: text}
	ch <- agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishStop}
	close(ch)
	return ch, nil
}

func TestCachedSummarySkipsFallback(t *testing.T) {
	var primaryCalls, fallbackCalls int
	primary := summaryProvider{name: "primary", call: func() (string, error) {
		primaryCalls++
		if primaryCalls == 1 {
			return "", agentapi.NewError(agentapi.ErrAuth, "primary temporarily unavailable")
		}
		return "primary summary", nil
	}}
	fallback := summaryProvider{name: "fallback", call: func() (string, error) {
		fallbackCalls++
		return "fallback summary", nil
	}}
	cache := project.CachedSummarizer{
		Dir:         t.TempDir(),
		ProviderKey: "primary-model",
		Inner:       project.ChatSummarizer{Provider: router.New(primary, router.WithFallback(fallback))},
	}
	for _, want := range []string{"fallback summary", "primary summary", "primary summary"} {
		got, err := cache.Summarize(t.Context(), "/p/HINT.md", "instruction text", 200)
		if err != nil || got != want {
			t.Fatalf("summary = %q, %v, want %q", got, err, want)
		}
	}
	if primaryCalls != 2 || fallbackCalls != 1 {
		t.Fatalf("primary calls = %d, fallback calls = %d, want 2 and 1", primaryCalls, fallbackCalls)
	}
}
