package agent

import (
	"context"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// compactSystemPrompt instructs the default Compactor's summary call. It
// asks for prose, not tool use — the summary itself becomes one
// [agentapi.SystemMessage], so there is nothing for the model to act on.
const compactSystemPrompt = "Summarize the conversation so far for a colleague " +
	"who will continue the work. Keep task-critical facts, file paths, and " +
	"decisions. Reply with the summary text only."

// llmCompactor is the [Compactor] [New] installs by default: it asks the
// same [agentapi.ChatProvider] the agent already talks to for a plain-text
// summary, with no tools offered.
type llmCompactor struct {
	provider agentapi.ChatProvider
}

func (c *llmCompactor) Compact(ctx context.Context, messages []agentapi.Message) (string, error) {
	req := agentapi.ChatRequest{
		Messages: append([]agentapi.Message{agentapi.SystemMessage(compactSystemPrompt)}, messages...),
	}

	events, err := c.provider.Stream(ctx, req)
	if err != nil {
		return "", err
	}

	var text strings.Builder
	for ev := range events {
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			text.WriteString(ev.Text)
		case agentapi.ChatError:
			return "", ev.Err
		case agentapi.ChatThinkingDelta, agentapi.ChatToolCall, agentapi.ChatUsage, agentapi.ChatDone:
			// A summary is plain text: reasoning, tool calls and
			// accounting from the summarizing model are ignored.
		}
	}
	return text.String(), nil
}

// overflowing reports whether tokens should still be considered "too many"
// for the configured window.
//
// With no window configured (MaxContextTokens <= 0, reactive-only mode: the
// agent only ever compacts in response to the provider's own
// ErrContextOverflow) there is no number to measure against, so this always
// answers true — the drop-thinking shortcut in [Agent.compact] can only be
// taken when a real window says it is enough; otherwise compaction always
// falls through to the full prefix/body/tail summarization.
func (a *Agent) overflowing(tokens int64) bool {
	if a.limits.MaxContextTokens <= 0 {
		return true
	}
	return float64(tokens) >= a.limits.CompactThreshold*float64(a.limits.MaxContextTokens)
}

// compact runs one compaction attempt over messages: it first drops every
// [agentapi.PartThinking] part (cheap, and the contract already promises
// this happens before anything heavier), and only if that is not enough
// against the configured window does it summarize the middle of the
// conversation via the [Compactor].
//
// Either way the emitted [agentapi.EventCompaction] carries the resulting
// history in Compaction.History: the region a summary replaces is not
// contiguous once the tail has been shrunk, so an observer (the WP0.7
// session store) could not rebuild the post-compaction state from the
// summary and a count alone.
//
// One call here is "one attempt" in the loop's "no more than one compaction
// attempt per iteration" rule — both steps happen without going back to the
// provider in between, so the caller only ever needs to retry the actual
// [agentapi.ChatProvider.Stream] call once after this returns.
func (a *Agent) compact(ctx context.Context, messages []agentapi.Message, out chan<- agentapi.Event) ([]agentapi.Message, error) {
	before := a.estimator.Estimate(messages)

	stripped, dropped := dropThinking(messages)
	if dropped > 0 {
		after := a.estimator.Estimate(stripped)
		if !a.overflowing(after) {
			out <- agentapi.Event{Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{
				TokensBefore: int(before),
				TokensAfter:  int(after),
				History:      stripped,
			}}
			return stripped, nil
		}
		messages = stripped
	}

	prefix, body, tail := splitForCompaction(messages, a.estimator.Estimate, int64(a.limits.MaxContextTokens))
	if len(body) == 0 {
		// Nothing left to compact: dropping thinking did not save it, and
		// the whole remaining history is either the leading system prompt
		// or the tail we refuse to cut a ToolCall/RoleTool pair out of.
		// Retrying will not change that, so this is fatal rather than
		// another retry loop.
		return nil, agentapi.NewError(agentapi.ErrContextOverflow, "context overflow: nothing left to compact")
	}

	summary, err := a.compactor.Compact(ctx, body)
	if err != nil {
		return nil, err
	}

	result := make([]agentapi.Message, 0, len(prefix)+1+len(tail))
	result = append(result, prefix...)
	result = append(result, agentapi.SystemMessage(summary))
	result = append(result, tail...)

	out <- agentapi.Event{Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{
		MessagesReplaced: len(body),
		TokensBefore:     int(before),
		TokensAfter:      int(a.estimator.Estimate(result)),
		Summary:          summary,
		History:          result,
	}}
	return result, nil
}

// dropThinking returns a copy of messages with every PartThinking content
// part removed, and how many parts were dropped. It never mutates its
// argument: messages is also the caller's in-flight turn history.
func dropThinking(messages []agentapi.Message) (out []agentapi.Message, dropped int) {
	out = make([]agentapi.Message, len(messages))
	for i, m := range messages {
		var content []agentapi.ContentPart
		for _, p := range m.Content {
			if p.Kind == agentapi.PartThinking {
				dropped++
				continue
			}
			content = append(content, p)
		}
		m.Content = content
		out[i] = m
	}
	return out, dropped
}

// splitForCompaction partitions messages into three regions:
//
//   - prefix: the leading run of RoleSystem messages (the system prompt,
//     and later HINT.md) — never summarized, always kept verbatim.
//   - tail: from the last RoleUser message onward — the current,
//     unanswered exchange. Cutting into it would show the model a summary
//     of a conversation turn it has not finished yet.
//   - body: everything between prefix and tail — the only region a
//     [Compactor] ever sees.
//
// If tail alone is heavier than half the window, it is shrunk to the
// anchoring user message plus only its last assistant/tool exchange,
// pushing the earlier rounds of the same unanswered turn into body. The
// shrink always keeps whole groups — a RoleAssistant message together with
// every RoleTool message answering its ToolCalls — so a tool call is never
// separated from its own result.
func splitForCompaction(messages []agentapi.Message, estimate func([]agentapi.Message) int64, window int64) (prefix, body, tail []agentapi.Message) {
	i := 0
	for i < len(messages) && messages[i].Role == agentapi.RoleSystem {
		i++
	}
	prefix = messages[:i]
	rest := messages[i:]

	tailStart := -1
	for j := len(rest) - 1; j >= 0; j-- {
		if rest[j].Role == agentapi.RoleUser {
			tailStart = j
			break
		}
	}
	if tailStart < 0 {
		// No RoleUser in rest at all: there is no "current exchange" to
		// protect, so everything after prefix is fair game for the
		// summarizer.
		return prefix, rest, nil
	}
	tail = rest[tailStart:]
	body = append([]agentapi.Message{}, rest[:tailStart]...)

	if window > 0 && estimate(tail) > window/2 {
		groups := tailGroups(tail)
		if len(groups) > 2 {
			middle := groups[1 : len(groups)-1]
			for _, g := range middle {
				body = append(body, g...)
			}
			tail = append(append([]agentapi.Message{}, groups[0]...), groups[len(groups)-1]...)
		}
	}
	return prefix, body, tail
}

// tailGroups splits tail — which starts with the anchoring RoleUser message
// — into units that must never be pulled apart: the user message is its own
// group, and every following RoleAssistant message is grouped with the
// RoleTool messages that answer its ToolCalls.
func tailGroups(tail []agentapi.Message) [][]agentapi.Message {
	var groups [][]agentapi.Message
	i := 0
	for i < len(tail) {
		start := i
		i++
		for i < len(tail) && tail[i].Role == agentapi.RoleTool {
			i++
		}
		groups = append(groups, tail[start:i])
	}
	return groups
}
