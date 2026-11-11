package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// wireVersion is written into the header so that a reader can recognise
// messages from a newer contract.
const wireVersion = agentapi.WireVersion

// Session is one conversation on disk, open for appending.
//
// It holds the conversation as loaded plus everything appended since, so
// [Session.Messages] is always the history a run should hand to the agent
// (after its own preamble). Records are appended one per line with a
// single write each; a complete record is never rewritten, and the only
// bytes ever removed are a torn last line left by a crash. An open
// Session holds the session's lock, so two runs cannot append to one
// file. Close when done.
type Session struct {
	path    string
	id      string
	cwd     string
	created time.Time
	// parent is set for a fork; profile is the last profile record, or
	// the zero value for "the configured default".
	parent           *Parent
	profile          ProfileRef
	workspace        WorkspaceRef
	workspaceHistory []WorkspaceRef
	totalUserTurns   int

	messages []agentapi.Message
	// warnings collects what load skipped or repaired, for the CLI to show.
	warnings []string
	// validLen is the byte length of the file up to and including the last
	// newline-terminated record; torn is set when bytes follow it — a crash
	// mid-write left an unterminated tail. Open truncates the file back to
	// validLen before appending, the way a journal discards a torn tail:
	// the tail is an interrupted append, and leaving it would make the next
	// record garble with it.
	validLen int64
	torn     bool

	f *os.File
	// lock is the open lock file whose exclusive lock this Session holds;
	// closing it lets the next run continue the session.
	lock *os.File
	now  func() time.Time
}

// ID returns the session id.
func (s *Session) ID() string { return s.id }

// Path returns the file.
func (s *Session) Path() string { return s.path }

// Cwd returns the working directory recorded in the header.
func (s *Session) Cwd() string { return s.cwd }

// Created returns when the session was started.
func (s *Session) Created() time.Time { return s.created }

// Parent returns where a fork's history came from, and false for a
// session that was started from nothing.
func (s *Session) Parent() (Parent, bool) {
	if s.parent == nil {
		return Parent{}, false
	}
	return *s.parent, true
}

// Profile returns the provider profile the session last switched to. The
// zero value means none was recorded: the run's default applies.
func (s *Session) Profile() ProfileRef { return s.profile }

// Warnings returns what loading the file skipped or repaired, one line
// each; empty for a clean file.
func (s *Session) Warnings() []string { return append([]string(nil), s.warnings...) }

// Messages returns a copy of the conversation: the last compaction
// checkpoint, if any, followed by every message since. An assistant tool
// call the file never answered — the run was interrupted mid-batch —
// gets a synthesized error result so the history stays sendable: every
// supported dialect rejects a tool call without a response.
//
// The leading system messages are marked as compaction summaries. A
// session never stores the run's prompt — the Recorder strips it before
// writing a checkpoint — so a system message at the head of a stored
// conversation is compaction output, including in files written before
// [agentapi.Message.Summary] existed. Marking it is what tells a caller
// that prepends a fresh preamble where the prompt ends and history begins.
func (s *Session) Messages() []agentapi.Message {
	out := repairDangling(s.messages)
	for i := range out {
		if out[i].Role != agentapi.RoleSystem {
			break
		}
		out[i].Summary = true
	}
	return out
}

// Len returns the number of conversation messages.
func (s *Session) Len() int { return len(s.messages) }

// AppendMessage records m.
func (s *Session) AppendMessage(m agentapi.Message) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("session: append: %w", err)
	}
	if err := s.write(Record{Kind: KindMessage, At: s.now().UTC(), Message: &m}); err != nil {
		return err
	}
	s.messages = append(s.messages, m)
	if m.Role == agentapi.RoleUser {
		s.totalUserTurns++
	}
	return nil
}

// AppendCompaction records a checkpoint. c.History must already be the
// conversation alone, without the run's preamble — [Recorder] does that
// stripping. A compaction without History (a transport that omitted it)
// is recorded for the record but changes nothing on load.
func (s *Session) AppendCompaction(c agentapi.Compaction) error {
	for i, m := range c.History {
		if err := m.Validate(); err != nil {
			return fmt.Errorf("session: append compaction history[%d]: %w", i, err)
		}
	}
	if err := s.write(Record{Kind: KindCompaction, At: s.now().UTC(), Compaction: &c}); err != nil {
		return err
	}
	if c.History != nil {
		s.messages = append([]agentapi.Message(nil), c.History...)
	}
	return nil
}

// AppendProfile records that ref answers from here on.
func (s *Session) AppendProfile(ref ProfileRef) error {
	if ref.Name == "" {
		return fmt.Errorf("session: append profile: no profile name")
	}
	if err := s.write(Record{Kind: KindProfile, At: s.now().UTC(), Profile: &ref}); err != nil {
		return err
	}
	s.profile = ref
	return nil
}

// write appends one record as a single line. One Write call per record,
// with the newline included, so a crash between records never leaves a
// half line that a later reader would have to guess about — and a
// reader that does find one knows it is the last, unfinished record.
func (s *Session) write(r Record) error {
	if s.f == nil {
		return fmt.Errorf("session: %s is closed", s.path)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("session: encode record: %w", err)
	}
	line = append(line, '\n')
	if _, err := s.f.Write(line); err != nil {
		return fmt.Errorf("session: write %s: %w", s.path, err)
	}
	return nil
}

// Close flushes the file to disk, closes it and releases the session's
// lock. The Session can still be read afterwards; appending fails.
func (s *Session) Close() error {
	if s.f == nil && s.lock == nil {
		return nil
	}
	var errs []error
	if f := s.f; f != nil {
		s.f = nil
		if err := f.Sync(); err != nil {
			_ = f.Close()
			errs = append(errs, fmt.Errorf("session: sync %s: %w", s.path, err))
		} else if err := f.Close(); err != nil {
			errs = append(errs, fmt.Errorf("session: close %s: %w", s.path, err))
		}
	}
	// The lock goes last: no other run may append between this one's last
	// write and its sync.
	if l := s.lock; l != nil {
		s.lock = nil
		if err := l.Close(); err != nil {
			errs = append(errs, fmt.Errorf("session: unlock %s: %w", s.path, err))
		}
	}
	return errors.Join(errs...)
}

// load reads a session file into a Session without opening it for
// appending. See the package doc for the rules: unknown kinds are skipped,
// an unsupported format version is refused, an unterminated last line is
// dropped with a warning, and a compaction record resets the conversation
// to its checkpoint.
func load(path string) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("session: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// A loaded snapshot is never appended to (f is nil), but it keeps a
	// clock so an append fails as closed instead of dereferencing nil.
	sess := &Session{path: path, now: time.Now}
	r := bufio.NewReader(f)
	for n := 1; ; n++ {
		line, err := r.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("session: read %s: %w", path, err)
		}
		if len(line) == 0 && errors.Is(err, io.EOF) {
			break
		}
		if line[len(line)-1] != '\n' {
			// The writer puts the record and its newline in one write, so a
			// line without one is an append a crash interrupted — never a
			// committed record, whether or not its bytes happen to parse.
			// Every record before it is intact on its own.
			sess.warnings = append(sess.warnings, fmt.Sprintf("line %d is unterminated and was ignored", n))
			sess.torn = true
			break
		}

		var rec Record
		if uerr := json.Unmarshal(line, &rec); uerr != nil {
			return nil, fmt.Errorf("session: %s line %d: %w", path, n, uerr)
		}
		if verr := rec.Validate(); verr != nil {
			if errors.Is(verr, ErrUnknownKind) || errors.Is(verr, agentapi.ErrUnknownKind) {
				// Written by a newer hint: skip rather than fail, per the
				// forward-compatibility rule the wire types promise.
				sess.warnings = append(sess.warnings, fmt.Sprintf("line %d was written by a newer version and was skipped (%v)", n, verr))
				sess.validLen += int64(len(line))
				if err != nil {
					break
				}
				continue
			}
			return nil, fmt.Errorf("session: %s line %d: %w", path, n, verr)
		}

		sess.validLen += int64(len(line))
		if n == 1 {
			if rec.Kind != KindSession {
				return nil, fmt.Errorf("session: %s does not start with a session header", path)
			}
			if rec.Version > FormatVersion {
				return nil, fmt.Errorf("session: %s uses format version %d, this build reads up to %d", path, rec.Version, FormatVersion)
			}
			sess.id, sess.cwd, sess.created, sess.parent = rec.ID, rec.Cwd, rec.At, rec.Parent
		} else {
			sess.apply(rec)
		}
		if err != nil {
			break // EOF after a complete final line
		}
	}
	if sess.id == "" {
		return nil, fmt.Errorf("session: %s is empty", path)
	}
	return sess, nil
}

// apply folds one body record into the loaded conversation.
func (s *Session) apply(rec Record) {
	switch rec.Kind {
	case KindMessage:
		s.messages = append(s.messages, *rec.Message)
		if rec.Message.Role == agentapi.RoleUser {
			s.totalUserTurns++
		}
	case KindCompaction:
		if rec.Compaction.History != nil {
			s.messages = append([]agentapi.Message(nil), rec.Compaction.History...)
		}
	case KindWorkspace:
		s.workspace = *rec.Workspace
		s.workspaceHistory = append(s.workspaceHistory, *rec.Workspace)
	case KindProfile:
		s.profile = *rec.Profile
	case KindSession:
		// A second header is a file that was concatenated by hand; the
		// conversation simply continues.
	}
}

// repairDangling returns messages with an error result synthesized for
// every assistant tool call that has no RoleTool answer before the next
// non-tool message. It never mutates its argument.
func repairDangling(messages []agentapi.Message) []agentapi.Message {
	out := make([]agentapi.Message, 0, len(messages))
	for i := 0; i < len(messages); i++ {
		m := messages[i]
		out = append(out, m)
		if m.Role != agentapi.RoleAssistant || len(m.ToolCalls) == 0 {
			continue
		}
		answered := map[string]bool{}
		j := i + 1
		for ; j < len(messages) && messages[j].Role == agentapi.RoleTool; j++ {
			answered[messages[j].ToolCallID] = true
			out = append(out, messages[j])
		}
		for _, c := range m.ToolCalls {
			if !answered[c.ID] {
				out = append(out, agentapi.ErrorResult(c.ID, c.Name,
					"no result was recorded: the run ended before this call finished").Message())
			}
		}
		i = j - 1
	}
	return out
}
