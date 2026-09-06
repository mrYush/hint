// Package session persists conversations so that a later run can continue
// them (WP0.7). A session is one append-only JSONL file; a [Store] locates
// the files of a working directory, a [Session] appends to one and hands
// back the conversation it holds, and a [Recorder] adapts the agent's
// event stream onto a Session.
//
// # What a session holds
//
// Only the dialogue: user, assistant and tool messages, plus the summaries
// compaction produced — every one an [agentapi.Message] written verbatim.
// The system prompt is not stored. It is the run's, not the
// conversation's: WP0.8 derives it from HINT.md and the project, both of
// which change between runs, and a frozen copy would go stale. The CLI
// prepends a fresh preamble on every run and tells the Recorder how many
// leading messages that preamble is, so that a compaction checkpoint can
// leave it out.
//
// # Compaction checkpoints
//
// A compaction is recorded as a checkpoint: the whole post-compaction
// conversation, taken from [agentapi.Compaction.History]. Loading a file
// therefore means "the last checkpoint, then every message after it" — no
// reader has to know how the agent chose what to summarize. The cost is
// that the retained tail is written twice; compaction is rare, and what it
// retains is by construction small enough to fit a context window.
//
// # File format
//
// One JSON object per line, [Record] shaped, with a Kind discriminator in
// the style of pkg/agentapi. The first line is the header (Kind
// [KindSession]) carrying [FormatVersion] and [agentapi.WireVersion];
// later lines are messages and compactions. A reader skips a record kind
// it does not know and refuses a file whose format version it does not
// support. The last line may be cut short by a crash; a reader ignores it
// with a warning, since every record before it is complete on its own,
// and opening the file for appending truncates it away first, the way a
// journal discards a torn tail, so the next record starts a clean line.
//
// The layout follows the session format of Pi (0BSD) — header line, typed
// entries, a retained tail on compaction — reduced to what a linear
// conversation needs: no entry ids or branching.
package session
