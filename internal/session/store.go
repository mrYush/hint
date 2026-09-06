package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// ErrNoSessions reports that a working directory has no session to
// continue. Callers test for it with errors.Is and start a new one.
var ErrNoSessions = errors.New("session: no sessions for this directory")

// DefaultDir returns where sessions live for the given home directory:
// $XDG_DATA_HOME/hint/sessions when the variable is set, otherwise
// ~/.local/share/hint/sessions. lookupEnv is injected, as in
// internal/config, so tests never touch the process environment.
func DefaultDir(home string, lookupEnv func(string) (string, bool)) string {
	if lookupEnv != nil {
		if xdg, ok := lookupEnv("XDG_DATA_HOME"); ok && xdg != "" {
			return filepath.Join(xdg, "hint", "sessions")
		}
	}
	return filepath.Join(home, ".local", "share", "hint", "sessions")
}

// Store locates session files under one base directory. Each working
// directory gets its own subdirectory, named by a hash of the absolute
// path, so that `hint -c` in a project only ever sees that project's
// sessions.
type Store struct {
	dir   string
	now   func() time.Time
	newID func() (string, error)
}

// Option configures a [Store].
type Option func(*Store)

// WithClock replaces the clock records are stamped with. Tests use it for
// a deterministic file.
func WithClock(now func() time.Time) Option {
	return func(s *Store) { s.now = now }
}

// WithIDs replaces the session id generator. Tests use it for a
// deterministic file name and header.
func WithIDs(newID func() (string, error)) Option {
	return func(s *Store) { s.newID = newID }
}

// NewStore returns a Store over dir, creating nothing until a session is.
func NewStore(dir string, opts ...Option) *Store {
	s := &Store{dir: dir, now: time.Now, newID: randomID}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Dir returns the store's base directory.
func (s *Store) Dir() string { return s.dir }

// ProjectDir returns the directory holding cwd's sessions.
func (s *Store) ProjectDir(cwd string) string {
	return filepath.Join(s.dir, projectHash(cwd))
}

// projectHash names a working directory's subdirectory: the first 16 hex
// digits of a SHA-256 over the cleaned absolute path. A hash rather than
// the path itself keeps the name short, portable across filesystems and
// free of separators; the header records the path in clear for listings.
func projectHash(cwd string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(cwd)))
	return hex.EncodeToString(sum[:8])
}

// randomID returns 16 hex digits from crypto/rand: unique enough for a
// per-user directory of session files, with no module for UUIDs.
func randomID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("session: generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// fileName is `<UTC timestamp>_<id>.jsonl`: a plain directory listing
// sorts by creation time, and the id is recoverable from the name.
func fileName(created time.Time, id string) string {
	return created.UTC().Format("20060102T150405Z") + "_" + id + ".jsonl"
}

// Create starts a new session for cwd: it creates the project directory
// and writes the header. The returned Session is open for appending.
func (s *Store) Create(cwd string) (*Session, error) {
	id, err := s.newID()
	if err != nil {
		return nil, err
	}
	dir := s.ProjectDir(cwd)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("session: create %s: %w", dir, err)
	}
	created := s.now().UTC()
	path := filepath.Join(dir, fileName(created, id))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: create %s: %w", path, err)
	}
	sess := &Session{path: path, id: id, cwd: filepath.Clean(cwd), created: created, f: f, now: s.now}
	header := Record{
		Kind:        KindSession,
		At:          created,
		Version:     FormatVersion,
		WireVersion: wireVersion,
		ID:          id,
		Cwd:         sess.cwd,
	}
	if err := sess.write(header); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return sess, nil
}

// Open loads the session file at path and opens it for appending. The
// conversation it holds is available through [Session.Messages].
func (s *Store) Open(path string) (*Session, error) {
	sess, err := load(path)
	if err != nil {
		return nil, err
	}
	if sess.torn {
		// Discard the partial record a crash left, so the next append
		// starts a clean line instead of garbling with it.
		if err := os.Truncate(path, sess.validLen); err != nil {
			return nil, fmt.Errorf("session: discard torn tail of %s: %w", path, err)
		}
		sess.torn = false
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("session: open %s for appending: %w", path, err)
	}
	sess.f = f
	sess.now = s.now
	return sess, nil
}

// Latest opens cwd's most recently modified session, or returns
// [ErrNoSessions].
func (s *Store) Latest(cwd string) (*Session, error) {
	infos, err := s.List(cwd)
	if err != nil {
		return nil, err
	}
	if len(infos) == 0 {
		return nil, ErrNoSessions
	}
	return s.Open(infos[0].Path)
}

// Info summarizes one session file for a listing.
type Info struct {
	// Path is the file.
	Path string
	// ID is the session id from the header.
	ID string
	// Cwd is the working directory from the header.
	Cwd string
	// Created is the header timestamp; Modified is the file's mtime, i.e.
	// when the conversation last grew.
	Created, Modified time.Time
	// Messages counts the conversation messages currently in the file.
	Messages int
	// FirstPrompt is the text of the first user message, for display.
	FirstPrompt string
}

// List returns cwd's sessions, most recently modified first. A file that
// cannot be read as a session is skipped: one damaged file must not hide
// the others.
func (s *Store) List(cwd string) ([]Info, error) {
	dir := s.ProjectDir(cwd)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("session: list %s: %w", dir, err)
	}
	var infos []Info
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := summarize(path)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}
	sort.SliceStable(infos, func(i, j int) bool {
		if !infos[i].Modified.Equal(infos[j].Modified) {
			return infos[i].Modified.After(infos[j].Modified)
		}
		return infos[i].Path > infos[j].Path
	})
	return infos, nil
}

// summarize reads one file into an [Info].
func summarize(path string) (Info, error) {
	sess, err := load(path)
	if err != nil {
		return Info{}, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return Info{}, err
	}
	info := Info{
		Path:     path,
		ID:       sess.id,
		Cwd:      sess.cwd,
		Created:  sess.created,
		Modified: st.ModTime(),
		Messages: len(sess.messages),
	}
	for _, m := range sess.messages {
		if m.Role == agentapi.RoleUser {
			info.FirstPrompt = m.Text()
			break
		}
	}
	return info, nil
}
