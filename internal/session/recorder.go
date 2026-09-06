package session

import "github.com/mrYush/hint/pkg/agentapi"

// Recorder adapts the agent's event stream onto a [Session]: every
// [agentapi.EventMessage] becomes a message record and every
// [agentapi.EventCompaction] a checkpoint. It is the one place that knows
// the run's preamble — the leading system messages the CLI prepends to
// the stored conversation before each turn — so that a checkpoint's
// History is stored without them, matching what the session holds.
//
// The first write failure sticks and later events are dropped: recording
// is best effort and must not interrupt an answer that is streaming to
// the user, so the CLI reports [Recorder.Err] once at the end instead.
type Recorder struct {
	session  *Session
	preamble int
	err      error
}

// NewRecorder returns a Recorder writing into s. preamble is how many
// leading messages of every turn's history belong to the run rather than
// to the conversation. A nil s records nothing (the --no-session run).
func NewRecorder(s *Session, preamble int) *Recorder {
	return &Recorder{session: s, preamble: preamble}
}

// Observe records ev when it is a message or a compaction and ignores
// every other kind.
func (r *Recorder) Observe(ev agentapi.Event) {
	if r.session == nil || r.err != nil {
		return
	}
	switch ev.Kind {
	case agentapi.EventMessage:
		if ev.Message != nil {
			r.err = r.session.AppendMessage(*ev.Message)
		}
	case agentapi.EventCompaction:
		if ev.Compaction != nil {
			r.err = r.session.AppendCompaction(r.stripPreamble(*ev.Compaction))
		}
	case agentapi.EventTurnStart, agentapi.EventTextDelta, agentapi.EventThinkingDelta,
		agentapi.EventPermission, agentapi.EventToolStart, agentapi.EventToolEnd,
		agentapi.EventUsage, agentapi.EventTurnEnd, agentapi.EventError:
		// Not part of the conversation.
	}
}

// stripPreamble returns c with the run's preamble removed from History.
// The preamble is exactly the first messages of the post-compaction
// history: compaction keeps the leading system run verbatim and inserts
// its summary after it.
func (r *Recorder) stripPreamble(c agentapi.Compaction) agentapi.Compaction {
	if c.History == nil {
		return c
	}
	n := min(r.preamble, len(c.History))
	c.History = append([]agentapi.Message(nil), c.History[n:]...)
	return c
}

// Err returns the first write failure, or nil.
func (r *Recorder) Err() error { return r.err }
