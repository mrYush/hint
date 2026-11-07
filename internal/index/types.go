// Package index stores bounded, versioned local navigation metadata.
package index

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"time"

	"github.com/mrYush/hint/internal/pathkey"
)

// Errors distinguish resource admission, stale reservations and absent entries.
var (
	ErrBusy           = errors.New("index: catalog is busy; retry later")
	ErrInitialization = errors.New("index: catalog initialization failed")
	ErrConfigConflict = errors.New("index: shared catalog resource limits conflict")
	ErrTooLarge       = errors.New("index: record or operation exceeds its finite budget")
	ErrCapacity       = errors.New("index: storage or reservation capacity reached")
	ErrStale          = errors.New("index: snapshot expired or was released")
	ErrNotFound       = errors.New("index: entry not found")
)

// Options bounds storage, SQLite cache and individual operations. Zero uses defaults.
type Options struct {
	MaxBytes         int64
	CacheKiB         int
	MaxRecordBytes   int
	MaxSourceBytes   int
	MaxSourceSymbols int
	MaxPins          int
	MaxPinTTL        time.Duration
	OperationTimeout time.Duration
}

// EntryKind describes an observed filesystem entry.
type EntryKind string

// Entry kinds include separately authorized nested-root boundaries.
const (
	KindFile      EntryKind = "file"
	KindDirectory EntryKind = "directory"
	KindBoundary  EntryKind = "boundary"
)

// SourceState distinguishes discovery from successful or deliberately omitted extraction.
type SourceState string

// Source states never imply live filesystem authority or freshness.
const (
	StatePending SourceState = "pending"
	StateIndexed SourceState = "indexed"
	StateOmitted SourceState = "omitted"
)

// File describes one observed root-relative source version. Path preserves raw bytes.
type File struct {
	RootID     string
	ID         string
	Path       []byte
	Hash       string
	Policy     string
	Size       int64
	ModTime    time.Time
	ObservedAt time.Time
	Kind       EntryKind
	State      SourceState
	Reason     string
}

// Symbol describes a declaration in the file's observed source bytes.
type Symbol struct {
	ID        string
	FileID    string
	Kind      string
	Name      string
	Signature string
	Enclosing string
	StartByte int64
	EndByte   int64
	StartLine int
	EndLine   int
}

// Snapshot is a durable retention reservation for one observed root generation.
type Snapshot struct {
	ID         string
	RootID     string
	Generation uint64
	ExpiresAt  time.Time
}

// Order chooses the physical index used by a bounded raw scan.
type Order string

// Orders follow the deterministic repo_map contract.
const (
	PathOrder Order = "path"
	NameOrder Order = "name"
)

// Match restricts the leading indexed name or path, without a global sort.
type Match string

// MatchAny leaves a leading key unrestricted.
const (
	MatchAny    Match = ""
	MatchExact  Match = "exact"
	MatchPrefix Match = "prefix"
)

// Query selects an index range. Name prefix requires NameOrder. Path ranges
// require PathOrder without a name filter; subtree filtering after a name
// search belongs to the caller and still consumes the examined budget.
type Query struct {
	Order     Order
	Name      string
	NameMatch Match
	Path      []byte
	PathMatch Match
}

// Key identifies an immutable raw record's total ordering position.
type Key struct {
	Path      []byte
	Name      string
	StartByte int64
	Kind      string
	SymbolID  string
	Version   uint64
}

// Record includes visibility bounds so callers filter only after bounded reads.
type Record struct {
	File       File
	Symbol     Symbol
	Key        Key
	ValidFrom  uint64
	ValidUntil uint64
}

// Visible reports whether this version belongs to generation; zero end means current.
func (r Record) Visible(generation uint64) bool {
	return r.ValidFrom <= generation && (r.ValidUntil == 0 || generation < r.ValidUntil)
}

// Page reports raw work, independently of how many records a caller accepts.
// Next advances only over returned records, including obsolete versions.
// ReadBytes counts retrieved record and header bytes, not SQLite page-cache I/O.
type Page struct {
	Records   []Record
	Next      *Key
	Examined  int
	ReadBytes int64
	Exhausted bool
}

// Status holds incrementally maintained current-entry counts for one root.
type Status struct {
	Generation uint64
	Files      int64
	Pending    int64
	Indexed    int64
	Omitted    int64
}

// EntryID identifies a root-relative path without requiring UTF-8 or storing it in a cursor.
func EntryID(rootID, path string) string {
	return pathkey.EntryID(rootID, path)
}

// SymbolID identifies a declaration's location and name within a stable file handle.
func SymbolID(fileID string, startByte int64, kind, name string) string {
	sum := sha256.Sum256([]byte(fileID + "\x00" + strconv.FormatInt(startByte, 10) + "\x00" + kind + "\x00" + name))
	return "s_" + base64.RawURLEncoding.EncodeToString(sum[:])
}
