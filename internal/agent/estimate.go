package agent

import "github.com/mrYush/hint/pkg/agentapi"

// Estimator estimates how many tokens a slice of messages would cost. The
// loop uses it to decide when to compact proactively, before a real request
// ever risks the provider's own [agentapi.ErrContextOverflow].
type Estimator interface {
	// Estimate returns the approximate token cost of messages.
	Estimate(messages []agentapi.Message) int64
}

// charEstimator is the default Estimator: total rune count of text content
// and tool-call JSON, divided by 4.
//
// This is not a tokenizer. A real one (tiktoken-style) is per-model, needs
// its vocabulary shipped or fetched, and the project's "no cgo" rule (see
// CONTRIBUTING.md) rules out the common C-binding implementations. chars/4
// is the same rough constant opencode and Crush fall back on for the same
// reason. It only has to be good enough to trigger compaction a bit before
// the real limit; the provider's ErrContextOverflow is the ground truth
// this estimate merely tries to anticipate.
type charEstimator struct{}

func (charEstimator) Estimate(messages []agentapi.Message) int64 {
	var chars int64
	for _, m := range messages {
		for _, p := range m.Content {
			chars += int64(len(p.Text))
		}
		for _, c := range m.ToolCalls {
			chars += int64(len(c.Name)) + int64(len(c.Arguments))
		}
	}
	return chars / 4
}
