package session

import (
	"errors"
	"fmt"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// FormatVersion is the revision of the JSONL layout described in this
// package's doc comment. It is written into every header and checked on
// load. Bump it when a record changes meaning; adding a record kind or an
// optional field does not need a bump, readers skip what they do not know.
const FormatVersion = 1

// Kind discriminates a [Record].
type Kind string

const (
	// KindSession is the header, always the first line of a file.
	KindSession Kind = "session"
	// KindMessage carries one conversation message in Record.Message.
	KindMessage Kind = "message"
	// KindCompaction is a checkpoint: Record.Compaction.History is the
	// whole conversation from here on, replacing everything before.
	KindCompaction Kind = "compaction"
)

// Record is one line of a session file. It is a tagged union — Kind says
// which of the payload fields is meaningful — for the same reason
// pkg/agentapi's types are: the value round-trips through encoding/json
// with no custom marshalling, so the on-disk format is exactly the Go
// struct.
type Record struct {
	// Kind selects the payload. Always set.
	Kind Kind `json:"kind"`
	// At is when the record was written, in UTC.
	At time.Time `json:"at"`

	// Header fields (KindSession only).

	// Version is the [FormatVersion] the file was written with.
	Version int `json:"version,omitempty"`
	// WireVersion is the [agentapi.WireVersion] of the messages inside.
	WireVersion int `json:"wire_version,omitempty"`
	// ID identifies the session; it is also part of the file name.
	ID string `json:"id,omitempty"`
	// Cwd is the working directory the session belongs to.
	Cwd string `json:"cwd,omitempty"`

	// Message is the payload of KindMessage.
	Message *agentapi.Message `json:"message,omitempty"`
	// Compaction is the payload of KindCompaction. Its History has the
	// run's preamble already removed — see [Recorder].
	Compaction *agentapi.Compaction `json:"compaction,omitempty"`
}

// ErrUnknownKind reports a record kind this build does not define. A
// loader treats it as "written by a newer version, skip", the same rule
// [agentapi.ErrUnknownKind] states for wire types.
var ErrUnknownKind = errors.New("session: unknown record kind")

// Validate reports whether the record's payload matches its Kind.
func (r *Record) Validate() error {
	switch r.Kind {
	case KindSession:
		if r.Version <= 0 {
			return fmt.Errorf("session: header has no format version")
		}
		if r.ID == "" {
			return fmt.Errorf("session: header has no id")
		}
	case KindMessage:
		if r.Message == nil {
			return fmt.Errorf("session: message record has no message")
		}
		if err := r.Message.Validate(); err != nil {
			return err
		}
	case KindCompaction:
		if r.Compaction == nil {
			return fmt.Errorf("session: compaction record has no compaction")
		}
		for i, m := range r.Compaction.History {
			if err := m.Validate(); err != nil {
				return fmt.Errorf("session: compaction history[%d]: %w", i, err)
			}
		}
	case "":
		return fmt.Errorf("session: record has no kind")
	default:
		return fmt.Errorf("%w: %q", ErrUnknownKind, r.Kind)
	}
	return nil
}
