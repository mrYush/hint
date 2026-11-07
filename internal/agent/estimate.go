package agent

import (
	"encoding/json"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Estimator estimates how many tokens a slice of messages would cost. The
// loop uses it to decide when to compact proactively, before a real request
// ever risks the provider's own [agentapi.ErrContextOverflow].
type Estimator interface {
	// Estimate returns the approximate token cost of messages.
	Estimate(messages []agentapi.Message) int64
}

// ToolEstimator optionally extends [Estimator] with a model-specific estimate
// of the tool catalog. Without it, [WithEstimator] still uses the default
// bytes/4 approximation for tools. Implementations must use the same scale
// across requests: changes in this estimate adjust prior provider usage.
type ToolEstimator interface {
	// EstimateTools returns the approximate token cost of tools, including
	// their names, descriptions, input schemas and any provider framing.
	EstimateTools(tools []agentapi.ToolSchema) int64
}

// charEstimator is the default Estimator: total UTF-8 byte count of text
// content and tool-call JSON, divided by 4.
//
// This is not a tokenizer. A real one (tiktoken-style) is per-model, needs
// its vocabulary shipped or fetched, and the project's "no cgo" rule (see
// CONTRIBUTING.md) rules out the common C-binding implementations. bytes/4
// is a rough approximation, not a guarantee for every language or model.
// The provider's ErrContextOverflow is the ground truth this estimate
// merely tries to anticipate.
type charEstimator struct{}

func (charEstimator) Estimate(messages []agentapi.Message) int64 {
	var bytes int64
	for _, m := range messages {
		for _, p := range m.Content {
			bytes += int64(len(p.Text))
		}
		for _, c := range m.ToolCalls {
			bytes += int64(len(c.Name)) + int64(len(c.Arguments))
		}
	}
	return bytes / 4
}

// EstimateTools uses the serialized API catalog as an approximation of the
// provider's prompt. Providers use different framing, so this is not an
// exact wire-size measurement or model token count.
func (charEstimator) EstimateTools(tools []agentapi.ToolSchema) int64 {
	if len(tools) == 0 {
		return 0
	}
	data, err := json.Marshal(tools)
	if err == nil {
		return (int64(len(data)) + 3) / 4
	}
	// A malformed raw schema will be rejected by the provider's validation.
	// Until then, count its fields rather than silently treating it as free.
	var bytes int64
	for _, tool := range tools {
		bytes += int64(len(tool.Name)) + int64(len(tool.Description)) + int64(len(tool.InputSchema))
	}
	return (bytes + 3) / 4
}

func (a *Agent) estimateTools(tools []agentapi.ToolSchema) int64 {
	if e, ok := a.estimator.(ToolEstimator); ok {
		return max(0, e.EstimateTools(tools))
	}
	return (charEstimator{}).EstimateTools(tools)
}
