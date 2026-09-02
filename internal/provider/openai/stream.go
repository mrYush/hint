package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

const (
	// scannerInitBuf and scannerMaxBuf size the SSE line scanner. The default
	// bufio.Scanner cap is 64KiB, which a single data: line with large
	// tool-call arguments can exceed; overflowing it kills the stream with
	// bufio.ErrTooLong instead of a parsed event.
	scannerInitBuf = 64 * 1024
	scannerMaxBuf  = 8 * 1024 * 1024

	// defaultIdleTimeout bounds the silence between stream chunks. A provider
	// that opens a tool call and then stalls would otherwise hang the run
	// forever: the overall request has no deadline by design.
	defaultIdleTimeout = 2 * time.Minute

	sseDataPrefix = "data:"
	sseDoneValue  = "[DONE]"
)

// readStream reads the SSE body, feeds it to a decoder, and emits ChatEvents.
// It owns resp.Body and the channel: exactly one terminal event, then close,
// on every path.
//
// The wire protocol itself lives in decoder, which is deliberately sans-IO
// (fed line by line, no HTTP anywhere), so the dialect quirks are testable
// from string literals.
func (c *Client) readStream(ctx context.Context, resp *http.Response, out chan<- agentapi.ChatEvent) {
	defer close(out)
	defer func() { _ = resp.Body.Close() }()

	dec := &decoder{log: c.debugf}
	delivered := false

	// emit forwards one event unless the consumer cancelled. The contract
	// obliges the consumer to either read or cancel; selecting on ctx keeps
	// an abandoned-after-cancel stream from leaking this goroutine.
	emit := func(ev agentapi.ChatEvent) bool {
		select {
		case out <- ev:
			delivered = true
			return true
		case <-ctx.Done():
			return false
		}
	}
	terminal := func(ev agentapi.ChatEvent) {
		select {
		case out <- ev:
		case <-ctx.Done():
			// Best effort: the consumer may already be gone.
			select {
			case out <- ev:
			default:
			}
		}
	}
	cancelTerminal := func() agentapi.ChatEvent {
		// Per the ChatProvider contract: a stream that already delivered
		// events ends as a user-facing cancel (ChatDone/FinishCanceled); a
		// cancel that beat the first delivery is ChatError/ErrCanceled.
		if delivered {
			return agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishCanceled}
		}
		err := ctx.Err()
		if err == nil {
			err = context.Canceled
		}
		e := agentapi.WrapError(agentapi.ErrCanceled, err, "stream canceled").WithProvider(c.profile.Name)
		return agentapi.ChatEvent{Kind: agentapi.ChatError, Err: e}
	}

	// Stall detection: every received line rearms the timer; if it fires,
	// closing the body unblocks the scanner with a read error. timedOut is
	// only read after the scanner stopped, so the race with a late fire is
	// benign.
	idle := c.idleTimeout
	if idle <= 0 {
		idle = defaultIdleTimeout
	}
	timedOut := false
	stall := time.AfterFunc(idle, func() {
		timedOut = true
		_ = resp.Body.Close()
	})
	defer stall.Stop()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, scannerInitBuf), scannerMaxBuf)

	for scanner.Scan() {
		stall.Reset(idle)
		events, done := dec.feedLine(scanner.Text())
		for _, ev := range events {
			if !emit(ev) {
				terminal(cancelTerminal())
				return
			}
		}
		if dec.failure != nil {
			terminal(agentapi.ChatEvent{Kind: agentapi.ChatError, Err: dec.failure.WithProvider(c.profile.Name)})
			return
		}
		if done {
			break
		}
	}

	if err := scanner.Err(); err != nil && !dec.done {
		switch {
		case ctx.Err() != nil, agentapi.KindOf(err) == agentapi.ErrCanceled:
			terminal(cancelTerminal())
		case timedOut:
			e := agentapi.NewError(agentapi.ErrTimeout,
				"stream stalled: no data for %s", idle).WithProvider(c.profile.Name)
			terminal(agentapi.ChatEvent{Kind: agentapi.ChatError, Err: e})
		default:
			kind := agentapi.KindOf(err)
			if kind == agentapi.ErrUnknown {
				kind = agentapi.ErrNetwork
			}
			e := agentapi.WrapError(kind, err, "stream broken").WithProvider(c.profile.Name)
			terminal(agentapi.ChatEvent{Kind: agentapi.ChatError, Err: e})
		}
		return
	}
	if ctx.Err() != nil {
		terminal(cancelTerminal())
		return
	}

	tail, err := dec.finalize(delivered)
	if err != nil {
		terminal(agentapi.ChatEvent{Kind: agentapi.ChatError, Err: err.WithProvider(c.profile.Name)})
		return
	}
	for _, ev := range tail {
		if ev.Terminal() {
			terminal(ev)
			return
		}
		if !emit(ev) {
			terminal(cancelTerminal())
			return
		}
	}
}

// wireChunk is one decoded SSE payload of a streamed chat completion.
type wireChunk struct {
	Choices []wireChoice `json:"choices"`
	Usage   *wireUsage   `json:"usage"`
	// Error carries a gateway's in-stream failure report. Not part of the
	// official dialect, but aggregators emit it inside an HTTP 200 stream
	// instead of breaking the connection.
	Error *wireError `json:"error"`
}

type wireChoice struct {
	Delta        wireDelta `json:"delta"`
	FinishReason string    `json:"finish_reason"`
}

type wireDelta struct {
	Content string `json:"content"`
	// ReasoningContent (DeepSeek and most gateways) and Reasoning
	// (OpenRouter) are the two spellings of a streamed thinking trace.
	ReasoningContent string             `json:"reasoning_content"`
	Reasoning        string             `json:"reasoning"`
	ToolCalls        []wireToolCallFrag `json:"tool_calls"`
}

type wireToolCallFrag struct {
	// Index correlates fragments of the same call. It is a pointer because
	// zero is a meaningful index and some dialects omit the field entirely,
	// correlating by ID instead — a plain int could not tell those apart.
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func (u *wireUsage) toUsage() *agentapi.Usage {
	return &agentapi.Usage{
		InputTokens:       u.PromptTokens,
		OutputTokens:      u.CompletionTokens,
		CachedInputTokens: u.PromptTokensDetails.CachedTokens,
		ReasoningTokens:   u.CompletionTokensDetails.ReasoningTokens,
	}
}

// decoder turns SSE lines into ChatEvents. It is sans-IO: feedLine consumes
// one line at a time and finalize flushes what must wait for the end of the
// stream (assembled tool calls, usage, the terminal event).
type decoder struct {
	log func(string, ...any)

	// data collects the data: lines of the SSE record being read; the SSE
	// grammar joins them with newlines at the blank-line record boundary.
	data []string

	calls  callAccumulator
	usage  *agentapi.Usage
	finish agentapi.FinishReason
	// done is set by the [DONE] sentinel.
	done bool
	// failure is a provider-reported in-stream error; it terminates the
	// stream immediately.
	failure *agentapi.Error
}

// feedLine consumes one line of the SSE body and returns the events it
// produced. done reports that the [DONE] sentinel arrived.
func (d *decoder) feedLine(line string) (events []agentapi.ChatEvent, done bool) {
	line = strings.TrimSuffix(line, "\r")
	switch {
	case line == "":
		// Blank line: record boundary. Dispatch the collected data.
		return d.flushRecord(), d.done
	case strings.HasPrefix(line, ":"):
		// SSE comment / keep-alive.
		return nil, false
	case strings.HasPrefix(line, sseDataPrefix):
		d.data = append(d.data, strings.TrimPrefix(strings.TrimPrefix(line, sseDataPrefix), " "))
		return nil, false
	case strings.HasPrefix(line, "event:"), strings.HasPrefix(line, "id:"), strings.HasPrefix(line, "retry:"):
		// Other SSE fields carry no meaning in this dialect. They are matched
		// by name, not by "contains a colon" — a bare JSON payload contains
		// colons too.
		return nil, false
	default:
		// A line without SSE framing. Some servers emit plain JSON lines;
		// tolerate them as data.
		d.data = append(d.data, line)
		return nil, false
	}
}

// flushRecord processes the record collected so far.
func (d *decoder) flushRecord() []agentapi.ChatEvent {
	if len(d.data) == 0 {
		return nil
	}
	payload := strings.Join(d.data, "\n")
	d.data = d.data[:0]

	if strings.TrimSpace(payload) == sseDoneValue {
		d.done = true
		return nil
	}

	var chunk wireChunk
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		// A single malformed frame from a flaky gateway must not kill the
		// turn; log and keep reading.
		d.logf("skipping unparseable stream chunk: %v", err)
		return nil
	}
	if chunk.Error != nil {
		d.failure = agentapi.NewError(agentapi.ErrUnavailable,
			"provider reported: %s", chunk.Error.describe())
		return nil
	}
	if chunk.Usage != nil {
		d.usage = chunk.Usage.toUsage()
	}
	// A chunk may carry no choices at all: the usage-only final chunk, or
	// Azure's prompt-filter annotations. Indexing blindly would panic.
	if len(chunk.Choices) == 0 {
		return nil
	}
	choice := chunk.Choices[0]

	var events []agentapi.ChatEvent
	if choice.Delta.Content != "" {
		events = append(events, agentapi.ChatEvent{Kind: agentapi.ChatTextDelta, Text: choice.Delta.Content})
	}
	if t := firstNonEmpty(choice.Delta.ReasoningContent, choice.Delta.Reasoning); t != "" {
		events = append(events, agentapi.ChatEvent{Kind: agentapi.ChatThinkingDelta, Text: t})
	}
	for _, frag := range choice.Delta.ToolCalls {
		d.calls.add(frag)
	}
	if choice.FinishReason != "" {
		d.finish = mapFinishReason(choice.FinishReason)
		// Keep reading: the usage chunk arrives after finish_reason and
		// before [DONE].
	}
	return events
}

// finalize flushes the end-of-stream events: assembled tool calls, usage,
// and the terminal ChatDone. delivered says whether any event reached the
// consumer, which decides how an empty stream is classified.
func (d *decoder) finalize(delivered bool) ([]agentapi.ChatEvent, *agentapi.Error) {
	// A final record may be unterminated (EOF without a trailing blank
	// line); a spec-lawyer would drop it, real servers expect it parsed.
	events := d.flushRecord()
	tail, err := d.finalizeAfter(delivered || len(events) > 0)
	if err != nil {
		return nil, err
	}
	return append(events, tail...), nil
}

func (d *decoder) finalizeAfter(delivered bool) ([]agentapi.ChatEvent, *agentapi.Error) {
	calls, err := d.calls.finalize()
	if err != nil {
		// A half-built call must never escape: the contract promises every
		// ChatToolCall is complete and JSON-valid.
		return nil, agentapi.WrapError(agentapi.ErrUnknown, err, "malformed tool call in stream")
	}

	finish := d.finish
	if finish == "" {
		switch {
		case len(calls) > 0:
			// The server closed the stream without a finish_reason but the
			// model did stop to call tools.
			finish = agentapi.FinishToolCalls
		case !delivered && !d.done:
			// Nothing arrived and the connection just ended: a dead gateway
			// behind a healthy TCP accept. Reporting FinishStop here would
			// present an empty answer as a successful turn.
			return nil, agentapi.NewError(agentapi.ErrUnavailable,
				"stream ended before the provider sent anything")
		default:
			finish = agentapi.FinishStop
		}
	}
	if finish == agentapi.FinishToolCalls && len(calls) == 0 {
		return nil, agentapi.NewError(agentapi.ErrUnknown,
			"provider finished with tool_calls but sent none")
	}

	var events []agentapi.ChatEvent
	for i := range calls {
		events = append(events, agentapi.ChatEvent{Kind: agentapi.ChatToolCall, Call: &calls[i]})
	}
	if d.usage != nil {
		events = append(events, agentapi.ChatEvent{Kind: agentapi.ChatUsage, Usage: d.usage})
	}
	events = append(events, agentapi.ChatEvent{Kind: agentapi.ChatDone, FinishReason: finish})
	return events, nil
}

func (d *decoder) logf(format string, args ...any) {
	if d.log != nil {
		d.log(format, args...)
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// mapFinishReason folds every dialect spelling onto the agentapi vocabulary.
// Forwarding a raw value is forbidden by the contract: FinishReason.Valid
// would reject it downstream.
func mapFinishReason(raw string) agentapi.FinishReason {
	switch raw {
	case "stop", "end_turn":
		return agentapi.FinishStop
	case "tool_calls", "function_call":
		return agentapi.FinishToolCalls
	case "length", "max_tokens":
		return agentapi.FinishLength
	case "content_filter":
		return agentapi.FinishContentFilter
	default:
		// An unknown reason still ended the stream; treat it as a natural
		// stop rather than failing a completed answer.
		return agentapi.FinishStop
	}
}

// callAccumulator assembles streamed tool-call fragments. Fragments of one
// call share an index (the common dialect) or an ID (dialects that omit the
// index); id, name, and arguments concatenate across fragments. Calls are
// finalized in index order — map iteration order would shuffle parallel
// calls between runs.
type callAccumulator struct {
	byKey map[string]*callBuild
	order []string
	next  int
}

type callBuild struct {
	index int
	id    string
	name  string
	args  strings.Builder
}

func (a *callAccumulator) add(frag wireToolCallFrag) {
	if a.byKey == nil {
		a.byKey = make(map[string]*callBuild)
	}
	var key string
	switch {
	case frag.Index != nil:
		key = fmt.Sprintf("i%d", *frag.Index)
	case frag.ID != "":
		key = "id:" + frag.ID
	default:
		// Neither index nor id: treat the fragment as a new call. A dialect
		// this loose sends one complete call per fragment in practice.
		key = fmt.Sprintf("anon%d", a.next)
	}
	b, ok := a.byKey[key]
	if !ok {
		b = &callBuild{index: a.next}
		a.next++
		a.byKey[key] = b
		a.order = append(a.order, key)
	}
	if frag.Index != nil {
		b.index = *frag.Index
	}
	if frag.ID != "" && b.id == "" {
		b.id = frag.ID
	}
	if frag.Function.Name != "" {
		b.name += frag.Function.Name
	}
	b.args.WriteString(frag.Function.Arguments)
}

// finalize validates and orders the assembled calls. It fails on arguments
// that are not a complete JSON object — a truncated stream must surface as
// an error, not as a tool run on half an argument list.
func (a *callAccumulator) finalize() ([]agentapi.ToolCall, error) {
	if len(a.byKey) == 0 {
		return nil, nil
	}
	builds := make([]*callBuild, 0, len(a.byKey))
	for _, key := range a.order {
		builds = append(builds, a.byKey[key])
	}
	sort.SliceStable(builds, func(i, j int) bool { return builds[i].index < builds[j].index })

	calls := make([]agentapi.ToolCall, 0, len(builds))
	for _, b := range builds {
		args := strings.TrimSpace(b.args.String())
		if args == "" {
			// A no-argument tool streams either nothing or ""; both mean {}.
			args = "{}"
		}
		id := b.id
		if id == "" {
			// The contract requires an ID for result correlation; synthesize
			// a stable one when the dialect sent none.
			id = fmt.Sprintf("call_%d", b.index)
		}
		call := agentapi.ToolCall{ID: id, Name: b.name, Arguments: json.RawMessage(args)}
		if err := call.Validate(); err != nil {
			return nil, err
		}
		calls = append(calls, call)
	}
	return calls, nil
}
