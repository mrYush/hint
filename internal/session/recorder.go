package session

import "github.com/mrYush/hint/pkg/agentapi"

// Recorder is the conversation of one run: it seeds itself from the
// [Session] being continued, follows the agent's event stream — every
// [agentapi.EventMessage] is appended, every [agentapi.EventCompaction]
// replaces the history with its checkpoint — and journals each change
// into the session. [Recorder.Messages] is therefore the history the next
// turn should send, whether or not a file backs the run: the REPL asks it
// before every turn, and --no-session simply has no file behind it.
//
// It is the one place that knows the run's preamble — the leading system
// messages the CLI prepends to the stored conversation before each turn —
// so that a checkpoint's History is kept and stored without them,
// matching what the session holds.
//
// The first write failure sticks and later events are no longer written:
// recording is best effort and must not interrupt an answer that is
// streaming to the user, so the CLI reports [Recorder.Err] once at the
// end instead. The in-memory conversation keeps growing regardless, so a
// REPL whose disk filled up still remembers the turn it just had.
type Recorder struct {
	session  *Session
	preamble int
	messages []agentapi.Message
	err      error
}

// NewRecorder returns a Recorder writing into s, starting from the
// conversation s holds. preamble is how many leading messages of every
// turn's history belong to the run rather than to the conversation. A nil
// s records nothing (the --no-session run) and starts empty.
func NewRecorder(s *Session, preamble int) *Recorder {
	r := &Recorder{session: s, preamble: preamble}
	if s != nil {
		r.messages = s.Messages()
	}
	return r
}

// Messages returns a copy of the conversation as it stands: what the
// session held when the run started plus everything observed since.
func (r *Recorder) Messages() []agentapi.Message {
	return append([]agentapi.Message(nil), r.messages...)
}

// Append adds a message the client itself produced — the user's prompt —
// and writes it to the session. Unlike [Recorder.Observe], a write
// failure is returned: the CLI fails a turn before the first request
// rather than after the answer when the file cannot be written at all.
// The message is kept in memory either way.
func (r *Recorder) Append(m agentapi.Message) error {
	r.messages = append(r.messages, m)
	if r.session == nil {
		return nil
	}
	if err := r.session.AppendMessage(m); err != nil {
		r.err = err
		return err
	}
	return nil
}

// Observe follows ev when it is a message or a compaction and ignores
// every other kind.
func (r *Recorder) Observe(ev agentapi.Event) {
	switch ev.Kind {
	case agentapi.EventMessage:
		if ev.Message == nil {
			return
		}
		r.messages = append(r.messages, *ev.Message)
		if r.session != nil && r.err == nil {
			r.err = r.session.AppendMessage(*ev.Message)
		}
	case agentapi.EventCompaction:
		if ev.Compaction == nil {
			return
		}
		c := r.stripPreamble(*ev.Compaction)
		if c.History != nil {
			r.messages = append([]agentapi.Message(nil), c.History...)
		}
		if r.session != nil && r.err == nil {
			r.err = r.session.AppendCompaction(c)
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
