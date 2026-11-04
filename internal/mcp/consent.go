package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

// Consent decides whether a project-added MCP server may start. A global
// server is enabled by its presence; a project server runs a command or
// opens a connection that a cloned repository chose, so it waits for an
// explicit approval recorded outside the repository.
type Consent interface {
	// Check reports whether cfg may start in root. A false answer carries
	// a reason naming what is missing or which field changed.
	Check(root string, cfg ServerConfig) (bool, string)
	// Enable records approval for cfg in root.
	Enable(root string, cfg ServerConfig) error
}

// ConsentStore is the file-backed [Consent]: one JSON record per project
// root and server name. It is safe for concurrent use.
type ConsentStore struct {
	path string

	mu      sync.Mutex
	loaded  bool
	loadErr error
	records map[string]consentRecord
}

type consentRecord struct {
	Root        string    `json:"root"`
	Name        string    `json:"name"`
	Transport   string    `json:"transport"`
	Command     string    `json:"command"`
	Args        []string  `json:"args,omitempty"`
	URL         string    `json:"url,omitempty"`
	Permission  string    `json:"permission"`
	Fingerprint string    `json:"fingerprint"`
	Approved    time.Time `json:"approved"`
}

type consentFile struct {
	Version int             `json:"version"`
	Servers []consentRecord `json:"servers"`
}

// NewConsentStore returns a store at path. An empty path is a store that
// approves nothing, which is what a run without a known config directory
// gets.
func NewConsentStore(path string) *ConsentStore {
	return &ConsentStore{path: path}
}

// Check implements [Consent].
func (s *ConsentStore) Check(root string, cfg ServerConfig) (bool, string) {
	if !cfg.Project {
		return true, ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return false, fmt.Sprintf("MCP server %q is disabled: consent record: %v", cfg.Name, err)
	}
	rec, ok := s.records[consentKey(root, cfg.Name)]
	if !ok {
		return false, fmt.Sprintf("MCP server %q comes from the project file and is disabled until consent is recorded outside the repository", cfg.Name)
	}
	if rec.Fingerprint != cfg.fingerprint() {
		return false, fmt.Sprintf("MCP server %q is disabled: consent changed (%s)", cfg.Name, changedField(rec, cfg))
	}
	return true, ""
}

// Enable implements [Consent]. It preserves every other record and writes
// the file atomically with owner-only permissions.
func (s *ConsentStore) Enable(root string, cfg ServerConfig) error {
	cfg.normalize()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	key := consentKey(root, cfg.Name)
	s.records[key] = consentRecord{
		Root:        root,
		Name:        cfg.Name,
		Transport:   string(cfg.Transport),
		Command:     cfg.Command,
		Args:        append([]string(nil), cfg.Args...),
		URL:         cfg.URL,
		Permission:  string(cfg.Permission),
		Fingerprint: cfg.fingerprint(),
		Approved:    time.Now().UTC(),
	}
	return s.writeLocked()
}

// changedField names the first consent field that differs, so a user can
// see why a server that worked yesterday is disabled today.
func changedField(rec consentRecord, cfg ServerConfig) string {
	switch {
	case rec.Transport != string(cfg.Transport):
		return "transport"
	case rec.Command != cfg.Command:
		return "command"
	case !slices.Equal(rec.Args, cfg.Args):
		return "args"
	case rec.URL != cfg.URL:
		return "url"
	case rec.Permission != string(cfg.Permission):
		return "permission"
	}
	return "server definition"
}

func consentKey(root, name string) string { return root + "\x00" + name }

func (s *ConsentStore) loadLocked() error {
	if s.loaded {
		return s.loadErr
	}
	s.loaded = true
	s.records = map[string]consentRecord{}
	if s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.loadErr = fmt.Errorf("reading %s: %w", s.path, err)
		return s.loadErr
	}
	var f consentFile
	if err := json.Unmarshal(data, &f); err != nil {
		s.loadErr = fmt.Errorf("parsing %s: %w", s.path, err)
		return s.loadErr
	}
	for _, rec := range f.Servers {
		s.records[consentKey(rec.Root, rec.Name)] = rec
	}
	return nil
}

func (s *ConsentStore) writeLocked() error {
	if s.path == "" {
		return errors.New("mcp: no consent store path")
	}
	f := consentFile{Version: 1, Servers: make([]consentRecord, 0, len(s.records))}
	for _, rec := range s.records {
		f.Servers = append(f.Servers, rec)
	}
	slices.SortFunc(f.Servers, func(a, b consentRecord) int {
		if a.Root != b.Root {
			if a.Root < b.Root {
				return -1
			}
			return 1
		}
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp: encoding consent record: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp: creating %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".mcp-consent-*")
	if err != nil {
		return fmt.Errorf("mcp: creating consent record: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("mcp: setting consent record permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("mcp: writing consent record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mcp: writing consent record: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("mcp: replacing consent record: %w", err)
	}
	return nil
}
