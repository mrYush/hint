package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// errInterrupted is the outcome of a turn cancelled by Ctrl-C.
var errInterrupted = errors.New("interrupted")

// turnResult is what one turn produced, in the shape the one-shot
// outputs print.
type turnResult struct {
	// answer is every text fragment the model wrote this turn, joined:
	// what the text output streamed, what the JSON output reports.
	answer string
	finish agentapi.FinishReason
	usage  agentapi.Usage
	// err is the failure that ended the turn, when finish is FinishError.
	err *agentapi.Error
}

// turn runs one user turn: it records the question, hands the whole
// conversation to the agent, follows the event stream into the session
// recorder, the debug trace and the terminal, and returns what came back.
// stream receives the answer text as it arrives; nil collects it silently
// for an output that prints once at the end.
//
// The returned error is the turn's outcome — nil for a normal answer,
// "interrupted" for a cancel, the classified failure otherwise — and is
// what a one-shot exits with and what the REPL prints before the next
// prompt.
func (a *app) turn(ctx context.Context, question string, stream io.Writer) (turnResult, error) {
	user := agentapi.UserMessage(question)
	// Fail before the first request if the session cannot be written at
	// all; a read-only disk is better learned about now than after the
	// answer.
	if err := a.rec.Append(user); err != nil {
		return turnResult{}, err
	}
	history := append(append([]agentapi.Message(nil), a.preamble...), a.rec.Messages()...)
	a.tracef("turn: %q (%d messages)", question, len(history))

	var res turnResult
	var answer strings.Builder
	// lastErr remembers the most recent EventError. Per the contract,
	// EventError does not necessarily end the turn — a non-terminal
	// compaction failure is reported this way too, and the turn carries on
	// — so it only becomes the turn's result if EventTurnEnd actually
	// closes with FinishError.
	var lastErr *agentapi.Error
	var runErr error
	for ev := range a.agent.RunTurn(ctx, history) {
		a.rec.Observe(ev)
		a.traceEvent(ev)
		switch ev.Kind {
		case agentapi.EventTextDelta:
			answer.WriteString(ev.Text)
			if stream != nil {
				fmt.Fprint(stream, ev.Text)
			}
		case agentapi.EventPermission:
			// The prompt itself is drawn by the gate's ReaderPrompter,
			// which also reads the answer; rendering it here too would
			// print it twice. The event still travels the stream for a
			// Phase 1 client that answers over RPC.
		case agentapi.EventToolStart:
			// Tool activity goes to stderr so the answer on stdout stays
			// clean for pipes and scripts.
			fmt.Fprintf(a.io.err, "hint: %s %s\n", ev.Call.Name, summarizeArgs(ev.Call.Arguments))
		case agentapi.EventToolEnd:
			switch {
			case ev.Result.IsError:
				fmt.Fprintf(a.io.err, "hint: %s failed: %s\n", ev.Result.Name, firstLine(ev.Result.Text()))
			case ev.Result.Name == "todo":
				// The plan is for the user as much as for the model.
				fmt.Fprintln(a.io.err, ev.Result.Text())
			}
		case agentapi.EventCompaction:
			fmt.Fprintf(a.io.err, "hint: compacted %d messages into a summary\n", ev.Compaction.MessagesReplaced)
		case agentapi.EventUsage:
			res.usage = *ev.Usage
		case agentapi.EventError:
			lastErr = ev.Err
		case agentapi.EventTurnEnd:
			res.finish = ev.FinishReason
			switch ev.FinishReason {
			case agentapi.FinishStop, agentapi.FinishToolCalls, agentapi.FinishContentFilter:
				// A normal stop. FinishToolCalls never actually reaches
				// EventTurnEnd (a tool round always loops back into the
				// agent, never ends the turn directly).
			case agentapi.FinishCanceled:
				runErr = errInterrupted
			case agentapi.FinishLength:
				fmt.Fprintln(a.io.err, "hint: response was truncated by the provider's token limit")
			case agentapi.FinishError:
				res.err = lastErr
				runErr = lastErr
			}
		case agentapi.EventTurnStart, agentapi.EventThinkingDelta, agentapi.EventMessage:
			// Nothing to show: thinking is not rendered, and a message is
			// the sum of deltas already printed.
		}
	}
	if err := a.rec.Err(); err != nil && !a.warnedSave {
		// Recording is best effort once the answer is streaming: say so,
		// but the exit status stays the turn's.
		a.warnedSave = true
		fmt.Fprintf(a.io.err, "hint: warning: the session is not being saved: %v\n", err)
	}
	res.answer = answer.String()
	return res, runErr
}

// oneShot answers one question and returns the turn's outcome as the
// process's, in the configured output.
func (a *app) oneShot(ctx context.Context, question string) error {
	if a.opts.output == outputJSON {
		return a.oneShotJSON(ctx, question)
	}
	res, err := a.turn(ctx, question, a.io.out)
	if res.answer != "" {
		fmt.Fprintln(a.io.out)
	}
	return err
}

// jsonAnswer is the one object `--output json` prints: enough for a
// script to take the answer, tell a failure from a success, and continue
// the conversation later with --session.
type jsonAnswer struct {
	Answer       string                `json:"answer"`
	FinishReason agentapi.FinishReason `json:"finish_reason"`
	SessionID    string                `json:"session_id,omitempty"`
	Usage        *agentapi.Usage       `json:"usage,omitempty"`
	Error        *agentapi.Error       `json:"error,omitempty"`
}

// oneShotJSON runs the turn silently and prints one object afterwards.
// A failure is part of the object as well as the exit status, so a
// script that parses stdout still learns what went wrong.
func (a *app) oneShotJSON(ctx context.Context, question string) error {
	res, err := a.turn(ctx, question, nil)
	out := jsonAnswer{Answer: res.answer, FinishReason: res.finish, Error: res.err}
	if a.sess != nil {
		out.SessionID = a.sess.ID()
	}
	if res.usage != (agentapi.Usage{}) {
		out.Usage = &res.usage
	}
	if err != nil && out.Error == nil {
		out.Error = asAgentError(err)
	}
	if out.FinishReason == "" {
		out.FinishReason = agentapi.FinishError
	}
	enc := json.NewEncoder(a.io.out)
	if encErr := enc.Encode(out); encErr != nil {
		return fmt.Errorf("writing the answer: %w", encErr)
	}
	return err
}

// asAgentError gives a plain error the wire shape, keeping a classified
// one as it is.
func asAgentError(err error) *agentapi.Error {
	var e *agentapi.Error
	if errors.As(err, &e) {
		return e
	}
	return agentapi.WrapError(agentapi.KindOf(err), err, "%v", err)
}

// traceEvent writes an event to the debug log: the complete messages (the
// responses, in the sense the requirement means — the request bodies are
// logged by the provider client), the tool calls with their arguments,
// and everything that went wrong.
func (a *app) traceEvent(ev agentapi.Event) {
	if a.trace == nil {
		return
	}
	switch ev.Kind {
	case agentapi.EventMessage:
		body, err := json.Marshal(ev.Message)
		if err != nil {
			a.trace.Printf("message %s: (unmarshalable: %v)", ev.Message.Role, err)
			return
		}
		a.trace.Printf("message %s: %s", ev.Message.Role, body)
	case agentapi.EventToolStart:
		a.trace.Printf("tool %s start %s: %s", ev.Call.Name, ev.Call.ID, ev.Call.Arguments)
	case agentapi.EventToolEnd:
		a.trace.Printf("tool %s end %s: error=%v %d bytes: %s", ev.Result.Name, ev.Result.CallID, ev.Result.IsError, len(ev.Result.Text()), firstLine(ev.Result.Text()))
	case agentapi.EventPermission:
		a.trace.Printf("permission asked: %s", ev.Permission.Summary)
	case agentapi.EventCompaction:
		a.trace.Printf("compaction: %d messages replaced, ~%d -> ~%d tokens", ev.Compaction.MessagesReplaced, ev.Compaction.TokensBefore, ev.Compaction.TokensAfter)
	case agentapi.EventUsage:
		a.trace.Printf("usage: %+v", *ev.Usage)
	case agentapi.EventError:
		a.trace.Printf("error %s: %s", ev.Err.Kind, ev.Err.Message)
	case agentapi.EventTurnEnd:
		a.trace.Printf("turn end: %s", ev.FinishReason)
	case agentapi.EventTurnStart, agentapi.EventTextDelta, agentapi.EventThinkingDelta:
		// The deltas add up to the message logged above.
	}
}

// summarizeArgs renders a tool call's arguments on one short line.
func summarizeArgs(raw []byte) string {
	const max = 120
	s := strings.Join(strings.Fields(string(raw)), " ")
	if len(s) > max {
		s = s[:max] + "..."
	}
	return s
}

// firstLine returns the first line of s, for a one-line stderr notice.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
