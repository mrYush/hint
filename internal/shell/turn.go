// Package shell is the client side of a turn, shared by every shell of
// the core: the REPL and the one-shot in cmd/hint, and the TUI. It owns
// the meaning of the event stream — which events end a turn, what an
// error event means before turn_end, how a permission question is
// answered — so that two shells cannot drift apart on it. What the shell
// shows is its own business, expressed through [Sink].
package shell

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// ErrInterrupted is the outcome of a turn cancelled by the user.
var ErrInterrupted = errors.New("interrupted")

// cancelWait bounds the turn.cancel sent after the run's own context has
// ended: that context is gone, and the server may already be too.
const cancelWait = 5 * time.Second

// startWait bounds turn.start, which outlives the user's cancel: a core
// that took the request and never answers must not hold the shell
// forever. A variable so that tests can shorten it.
var startWait = 30 * time.Second

// Sink is what a shell shows of a turn while it runs. The methods are
// called in event order from the goroutine that runs the turn; a Sink
// that hands them to another goroutine (a UI loop) does its own
// marshalling.
type Sink interface {
	// Text is a fragment of the answer, in order.
	Text(delta string)
	// ToolStart says a tool call has begun.
	ToolStart(call agentapi.ToolCall)
	// ToolEnd carries a tool's result, error or not.
	ToolEnd(result agentapi.ToolResult)
	// Notice is a one-line remark about the turn itself: a compaction, a
	// truncated answer.
	Notice(line string)
	// Permission puts a question to the user and returns the decision.
	// ctx ends when the turn is cancelled; a Sink then returns whatever it
	// has, the turn is over either way.
	Permission(ctx context.Context, req agentapi.PermissionRequest) (permission.Decision, error)
}

// Result is what one turn produced, in the shape the outputs print.
type Result struct {
	// Answer is every text fragment the model wrote this turn, joined:
	// what a streaming output already showed, what a JSON output reports.
	Answer string
	Finish agentapi.FinishReason
	Usage  agentapi.Usage
	// Err is the failure that ended the turn, when Finish is FinishError.
	Err *agentapi.Error
}

// RunTurn runs one user turn through the core: it starts the turn,
// follows the event stream to the terminal event, answers permission
// questions through sink, and returns what came back.
//
// The returned error is the turn's outcome — nil for a normal answer,
// ErrInterrupted for a cancel, the classified failure otherwise — and is
// what a one-shot exits with and what an interactive shell shows before
// the next prompt.
func RunTurn(ctx context.Context, cl *rpc.Client, sessionID, text string, sink Sink) (Result, error) {
	// The start request runs to its answer even if ctx ends under it:
	// once turn.start is on the wire the server may well begin the turn,
	// and only a client that knows the turn id can cancel it. Giving up
	// on the response would leave the turn running with no owner. A dead
	// connection still ends the call through the client's own error, and
	// a live one that never answers through startWait.
	startCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), startWait)
	t, err := cl.StartTurn(startCtx, sessionID, text)
	stop()
	if errors.Is(err, context.DeadlineExceeded) {
		return Result{}, fmt.Errorf("the core did not answer turn.start within %s", startWait)
	}
	if err != nil {
		return Result{}, err
	}
	// A cancel from the user cancels the turn on the server — at once,
	// if it already happened while the turn was starting; the events are
	// still read to the end, so the turn closes the way the server
	// recorded it.
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			cancelTurn(t)
		case <-finished:
		}
	}()

	var res Result
	var answer strings.Builder
	// lastErr remembers the most recent EventError. Per the contract,
	// EventError does not necessarily end the turn — a non-terminal
	// compaction failure is reported this way too, and the turn carries on
	// — so it only becomes the turn's result if EventTurnEnd actually
	// closes with FinishError.
	var lastErr *agentapi.Error
	var runErr error
	for ev := range t.Events() {
		switch ev.Kind {
		case agentapi.EventTextDelta:
			answer.WriteString(ev.Text)
			sink.Text(ev.Text)
		case agentapi.EventPermission:
			reply(ctx, t, *ev.Permission, sink)
		case agentapi.EventToolStart:
			sink.ToolStart(*ev.Call)
		case agentapi.EventToolEnd:
			sink.ToolEnd(*ev.Result)
		case agentapi.EventCompaction:
			sink.Notice(fmt.Sprintf("compacted %d messages into a summary", ev.Compaction.MessagesReplaced))
		case agentapi.EventUsage:
			res.Usage = *ev.Usage
		case agentapi.EventError:
			lastErr = ev.Err
		case agentapi.EventTurnEnd:
			res.Finish = ev.FinishReason
			switch ev.FinishReason {
			case agentapi.FinishStop, agentapi.FinishToolCalls, agentapi.FinishContentFilter:
				// A normal stop. FinishToolCalls never actually reaches
				// EventTurnEnd (a tool round always loops back into the
				// agent, never ends the turn directly).
			case agentapi.FinishCanceled:
				runErr = ErrInterrupted
			case agentapi.FinishLength:
				sink.Notice("response was truncated by the provider's token limit")
			case agentapi.FinishError:
				// The contract sends an error event first; a core that
				// breaks it must not leave a nil *Error inside a non-nil
				// error for the shell to dereference.
				if lastErr == nil {
					lastErr = agentapi.NewError(agentapi.ErrUnknown, "the turn failed without an error event")
				}
				res.Err = lastErr
				runErr = lastErr
			}
		case agentapi.EventTurnStart, agentapi.EventThinkingDelta, agentapi.EventMessage:
			// Nothing to show: thinking is not rendered, and a message is
			// the sum of deltas already shown.
		}
	}
	if err := t.Err(); err != nil {
		// The stream ended without turn.done: the connection to the core
		// is gone, and so is whatever the turn still had to say.
		return res, fmt.Errorf("lost the connection to the core: %w", err)
	}
	res.Answer = answer.String()
	return res, runErr
}

// reply puts a permission question to the sink and sends the decision
// back. ctx ending at the question is not answered at all: the cancel
// watcher in RunTurn ends the turn, and the server denies.
func reply(ctx context.Context, t *rpc.Turn, req agentapi.PermissionRequest, sink Sink) {
	decision, err := sink.Permission(ctx, req)
	if err != nil {
		return
	}
	if err := t.Reply(ctx, req.CallID, decision); err != nil && ctx.Err() == nil {
		sink.Notice(fmt.Sprintf("warning: the answer did not reach the core: %v", err))
	}
}

// cancelTurn asks the server to stop t. A turn that has already ended
// makes this an error nobody needs to hear about.
func cancelTurn(t *rpc.Turn) {
	ctx, cancel := context.WithTimeout(context.Background(), cancelWait)
	defer cancel()
	_ = t.Cancel(ctx)
}

// SummarizeArgs renders a tool call's arguments on one short line.
func SummarizeArgs(raw []byte) string {
	const max = 120
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// FirstLine returns the first line of s, for a one-line notice.
func FirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Advice says what to do about a failed turn, empty when the error says
// enough. A rejected credential gets one, since "auth: Unauthorized"
// alone does not say whose key or where it comes from.
func Advice(err error) string {
	var ae *agentapi.Error
	if errors.As(err, &ae) && ae != nil && ae.Kind == agentapi.ErrAuth {
		return "the provider rejected the credentials: check the api_key of the profile in use " +
			"(and the environment variable it expands, if any); `hint models` tries the same profile"
	}
	return ""
}

// Explain is the text a shell shows for a turn that failed: the error,
// then its Advice on the next line.
func Explain(err error) string {
	if advice := Advice(err); advice != "" {
		return err.Error() + "\n" + advice
	}
	return err.Error()
}
