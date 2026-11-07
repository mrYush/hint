package repomap

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

type savedQuery struct {
	session   string
	snapshot  index.Snapshot
	request   Request
	lease     workspace.Lease
	directory bool
	coverage  Coverage
	policy    uint64
}

type mapTool struct {
	manager *Manager
	space   *workspace.Session
}

func (*mapTool) Name() string { return "repo_map" }
func (*mapTool) Description() string {
	return "Find code declarations and signatures in one attached repository. Use repo (ID/alias), path or entry_id, optional symbol and exact/prefix match. Results may be partial; continue using only cursor. Read source before editing; an empty incomplete page does not prove absence."
}
func (*mapTool) InputSchema() json.RawMessage { return mapSchema }
func (*mapTool) Class() agentapi.ActionClass  { return agentapi.ClassRead }
func (*mapTool) StructuredOutput() bool       { return true }

var mapSchema = tool.MustSchema(Request{})

var errRetentionCapacity = errors.New("repo map: retention capacity")

func (t *mapTool) Run(ctx context.Context, callID string, args json.RawMessage) (agentapi.ToolResult, error) {
	in, err := decodeRequest(args)
	if err != nil {
		return mapFailure(callID, "invalid_args", err.Error(), false), nil
	}
	budget := MaxOutputBytes
	if outer := tool.OutputBudget(ctx); outer > 0 {
		budget = min(budget, outer)
	}
	if budget < MinOutputBytes {
		return mapFailure(callID, "index_unavailable", "repo_map requires an output budget of at least 1024 bytes", false), nil
	}
	result, err := t.manager.query(ctx, t.space, in, budget)
	if err != nil {
		if ctx.Err() != nil {
			return agentapi.ToolResult{}, ctx.Err()
		}
		code, reason := "index_unavailable", ""
		switch {
		case errors.Is(err, errRetentionCapacity):
			reason = "retention_capacity"
		case errors.Is(err, workspace.ErrUnknownRoot):
			code = "unknown_root"
		case errors.Is(err, workspace.ErrChanged), errors.Is(err, index.ErrStale):
			code = "stale_cursor"
		case errors.Is(err, workspace.ErrStaleEntry), errors.Is(err, os.ErrNotExist), errors.Is(err, index.ErrNotFound):
			code = "scope_not_found"
		case errors.Is(err, tool.ErrOutsideRoot), errors.Is(err, tool.ErrDenied), errors.Is(err, workspace.ErrBoundary), errors.Is(err, os.ErrPermission):
			code = "access_denied"
		case errors.Is(err, context.DeadlineExceeded):
			code = "query_timeout"
		}
		return mapFailure(callID, code, err.Error(), code == "index_unavailable" || code == "query_timeout", reason), nil
	}
	return agentapi.TextResult(callID, "repo_map", string(result)), nil
}

func mapFailure(id, code, message string, retryable bool, reason ...string) agentapi.ToolResult {
	return agentapi.ErrorResult(id, "repo_map", errorPayload(code, message, retryable, reason...))
}

func (m *Manager) openQuery(ctx context.Context, space *workspace.Session, in Request) (*savedQuery, error) {
	var lease workspace.Lease
	var err error
	if in.EntryID != "" {
		lease, err = m.ResolveEntry(ctx, space, in.Repo, in.EntryID)
	} else {
		lease, err = space.Resolve(in.Repo, in.Path)
	}
	if err != nil {
		return nil, err
	}
	info, err := lease.Stat()
	if err != nil {
		return nil, err
	}
	if err := m.excluded(ctx, lease, info.IsDir()); err != nil {
		return nil, err
	}
	m.Track(space)
	if !info.IsDir() {
		// A focused file request can make progress without waiting behind a
		// large root. The worker still shares the manager's finite lease.
		if err := m.refresh(ctx, lease); err != nil && ctx.Err() != nil {
			return nil, err
		}
	}
	// Every abbreviated scope has a durable mapping, even when discovery
	// has not reached this directory yet. Never replace existing metadata.
	if info.IsDir() && lease.RelativePath != "." {
		_, lookupErr := m.store.EntryLookup(ctx, lease.RootID, workspace.EntryID(lease.RootID, lease.RelativePath))
		if errors.Is(lookupErr, index.ErrNotFound) {
			lookupErr = lease.Publish(func() error {
				_, putErr := m.store.PutSource(ctx, index.File{RootID: lease.RootID, Path: []byte(filepath.ToSlash(lease.RelativePath)), Kind: index.KindDirectory, State: index.StatePending, ModTime: info.ModTime(), ObservedAt: time.Now()}, nil)
				return putErr
			})
		}
		if lookupErr != nil {
			return nil, lookupErr
		}
	}
	discoveryComplete := m.status(lease.RootID).State == "ready"
	snap, err := m.store.Pin(ctx, lease.RootID, 10*time.Minute)
	if err != nil {
		if errors.Is(err, index.ErrCapacity) {
			return nil, errors.Join(errRetentionCapacity, err)
		}
		return nil, err
	}
	q := &savedQuery{session: space.ID(), snapshot: snap, request: in, lease: lease, directory: info.IsDir(), policy: m.policyRevision(lease.RootID), coverage: Coverage{DiscoveryComplete: discoveryComplete}}
	if lease.RelativePath == "." {
		if counts, e := m.store.Status(ctx, lease.RootID); e == nil && counts.Generation == snap.Generation {
			q.coverage.KnownFiles, q.coverage.PendingFiles = &counts.Files, &counts.Pending
			q.coverage.IndexedFiles, q.coverage.OmittedFiles = &counts.Indexed, &counts.Omitted
		}
	}
	m.mu.Lock()
	for id, old := range m.queries {
		if !time.Now().Before(old.snapshot.ExpiresAt) {
			delete(m.queries, id)
		}
	}
	m.queries[snap.ID] = q
	m.mu.Unlock()
	return q, nil
}

func (m *Manager) query(parent context.Context, space *workspace.Session, in Request, budget int) ([]byte, error) {
	var q *savedQuery
	var after *index.Key
	var err error
	if in.Cursor == "" {
		q, err = m.openQuery(parent, space, in)
	} else {
		q, after, err = m.resume(parent, space, in.Cursor)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(parent, 250*time.Millisecond)
	defer cancel()
	ctx, release, err := m.acquireFocused(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	// A cursor retains data, never a grant to reveal a scope that has since
	// disappeared or become excluded. Check before constructing its envelope.
	info, err := q.lease.Stat()
	if err != nil {
		if in.Cursor != "" && definitiveAccess(err) {
			return nil, index.ErrStale
		}
		return nil, err
	}
	if info.IsDir() != q.directory {
		return nil, index.ErrStale
	}
	if err := m.excluded(ctx, q.lease, q.directory); err != nil {
		if in.Cursor != "" && definitiveAccess(err) {
			return nil, index.ErrStale
		}
		return nil, err
	}
	// Reserve the entire final envelope before admitting any declaration.
	// Verification counters may grow and false is one byte longer than true.
	token, err := m.cursor(q, nil)
	if err != nil {
		return nil, err
	}
	result := Response{RootID: q.lease.RootID, MembershipRevision: q.lease.MembershipRevision,
		Generation: q.snapshot.Generation, Scope: Scope{PathDisplay: q.lease.RelativePath}, Order: "path",
		Status: m.status(q.lease.RootID), Coverage: q.coverage, Files: []File{}, Entries: []Declaration{},
		Verification: &Verification{FilesChecked: 4, BytesRead: 1 << 20},
		Page:         Page{Truncated: true, StopReason: "byte_budget", NextCursor: token, ExpiresAt: q.snapshot.ExpiresAt.UTC().Format(time.RFC3339)}}
	if q.lease.RelativePath != "." {
		result.Scope.EntryID = workspace.EntryID(q.lease.RootID, q.lease.RelativePath)
		result.Scope.PathDisplay = abbreviate(q.lease.RelativePath, 64)
		result.Scope.PathTruncated = result.Scope.PathDisplay != q.lease.RelativePath
	}
	if budget < 2048 {
		if result.Scope.EntryID != "" {
			result.Scope = Scope{EntryID: result.Scope.EntryID}
		}
		result.Coverage = Coverage{DiscoveryComplete: result.Coverage.DiscoveryComplete}
		result.Verification = &Verification{}
	}
	query := index.Query{Order: index.PathOrder}
	if q.request.Symbol != "" {
		query.Name, query.NameMatch = q.request.Symbol, index.MatchExact
		if q.request.Match == "prefix" {
			query.Order, query.NameMatch, result.Order = index.NameOrder, index.MatchPrefix, "name"
		}
	} else if q.lease.RelativePath != "." {
		query.Path, query.PathMatch = []byte(filepath.ToSlash(q.lease.RelativePath)), index.MatchExact
		if q.directory {
			query.Path = append(query.Path, '/')
			query.PathMatch = index.MatchPrefix
		}
	}
	remaining, readBudget := 512, int64(1<<20)
	verified := make(map[string]validation)
	invalidated := make(map[string]bool)
	work := Verification{}
	hashUntil := time.Now().Add(100 * time.Millisecond)
	complete, stop := false, "work_budget"
scan:
	for remaining > 0 && readBudget > 0 && ctx.Err() == nil {
		page, scanErr := m.store.Scan(ctx, q.snapshot, query, after, min(32, remaining), readBudget)
		if scanErr != nil {
			if errors.Is(scanErr, context.DeadlineExceeded) && parent.Err() == nil {
				stop = "time_budget"
				break
			}
			if errors.Is(scanErr, index.ErrTooLarge) && readBudget < 144 {
				break
			}
			return nil, scanErr
		}
		remaining -= page.Examined
		readBudget -= page.ReadBytes
		for _, record := range page.Records {
			if ctx.Err() != nil {
				stop = "time_budget"
				break scan
			}
			if !record.Visible(q.snapshot.Generation) || !withinScope(q, string(record.File.Path)) {
				key := record.Key
				after = &key
				continue
			}
			v, exists := verified[record.File.ID]
			if !exists {
				v, err = m.validate(ctx, space, record.File, &work, hashUntil)
				if ctx.Err() != nil {
					stop = "time_budget"
					break scan
				}
				// Unknown/transient failures never consume a potentially eligible
				// record. A fresh retry resumes from the same authenticated token.
				if err != nil {
					return nil, err
				}
				verified[record.File.ID] = v
			}
			if !v.usable {
				if !invalidated[record.File.ID] {
					if !appendInvalidation(&result, v.reason, budget) {
						stop = "byte_budget"
						break scan
					}
					invalidated[record.File.ID] = true
				}
				key := record.Key
				after = &key
				continue
			}
			s := record.Symbol
			d := Declaration{ID: s.ID, FileID: s.FileID, Kind: s.Kind, Name: s.Name,
				Signature: s.Signature, Enclosing: s.Enclosing, StartByte: s.StartByte,
				EndByte: s.EndByte, StartLine: s.StartLine, EndLine: s.EndLine}
			if !appendEntry(&result, v.file, d, budget) {
				stop = "byte_budget"
				break scan
			}
			key := record.Key
			after = &key
			if len(result.Entries) >= q.request.Limit {
				stop = "limit"
				break scan
			}
		}
		if page.Exhausted {
			complete = true
			break
		}
		if len(page.Records) == 0 {
			break
		}
	}
	if parent.Err() != nil {
		return nil, parent.Err()
	}
	if ctx.Err() != nil && !complete {
		stop = "time_budget"
	}
	*result.Verification = work
	if budget < 2048 {
		result.Verification = &Verification{BudgetExhausted: work.BudgetExhausted}
	}
	result.Page = Page{Truncated: !complete, QueryComplete: complete}
	if !complete {
		result.Page.NextCursor, err = m.cursor(q, after)
		if err != nil {
			return nil, err
		}
		result.Page.StopReason, result.Page.ExpiresAt = stop, q.snapshot.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if q.policy != m.policyRevision(q.lease.RootID) {
		return nil, index.ErrStale
	}
	var payload []byte
	err = space.Publish(q.lease, func() error {
		var encodeErr error
		payload, encodeErr = encode(result, budget)
		return encodeErr
	})
	if err == nil && complete {
		if releaseErr := m.store.Release(parent, q.snapshot.ID); releaseErr != nil {
			return nil, releaseErr
		}
		m.mu.Lock()
		delete(m.queries, q.snapshot.ID)
		m.mu.Unlock()
	}
	return payload, err
}

func withinScope(q *savedQuery, path string) bool {
	scope := filepath.ToSlash(q.lease.RelativePath)
	if scope == "." {
		return true
	}
	return path == scope || q.directory && strings.HasPrefix(path, scope+"/")
}

type validation struct {
	usable bool
	file   File
	reason string
}

func (m *Manager) validate(ctx context.Context, space *workspace.Session, source index.File, work *Verification, until time.Time) (result validation, err error) {
	invalidate := func(reason string) (validation, error) {
		if err := m.store.Enqueue(ctx, source.RootID, source.Path); err != nil {
			return validation{}, err
		}
		return validation{reason: reason}, nil
	}
	lease, err := space.Resolve(source.RootID, string(source.Path))
	if err != nil {
		if definitiveAccess(err) {
			return validation{reason: accessReason(err)}, nil
		}
		return validation{}, err
	}
	f, err := lease.OpenRegular()
	if errors.Is(err, workspace.ErrNotRegular) {
		return invalidate("source_changed")
	}
	if err != nil {
		if definitiveAccess(err) {
			return invalidate(accessReason(err))
		}
		return validation{}, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return validation{}, err
	}
	if !info.Mode().IsRegular() || info.Size() != source.Size || !info.ModTime().Equal(source.ModTime) {
		return invalidate("source_changed")
	}
	if source.Policy != extract.PolicyVersion(extract.Language(string(source.Path))) {
		return invalidate("extractor_changed")
	}
	if err := m.excluded(ctx, lease, false); err != nil {
		if definitiveAccess(err) {
			return validation{reason: "excluded"}, nil
		}
		return validation{}, err
	}
	digest, err := hex.DecodeString(strings.TrimPrefix(source.Hash, "sha256:"))
	if err != nil || len(digest) != sha256.Size || !strings.HasPrefix(source.Hash, "sha256:") {
		return invalidate("source_changed")
	}
	file := File{ID: source.ID, PathDisplay: string(source.Path), SourceVersion: "sha256:" + base64.RawURLEncoding.EncodeToString(digest), Freshness: "observed", ObservedAt: source.ObservedAt.UTC().Format(time.RFC3339)}
	remaining := int64(1<<20) - work.BytesRead
	if work.FilesChecked < 4 && info.Size() <= remaining && time.Now().Before(until) && ctx.Err() == nil {
		hash := sha256.New()
		var buf [32 << 10]byte
		read := int64(0)
		for read < info.Size() && time.Now().Before(until) && ctx.Err() == nil {
			n, readErr := f.Read(buf[:min(int64(len(buf)), info.Size()-read)])
			work.BytesRead += int64(n)
			read += int64(n)
			if _, err := hash.Write(buf[:n]); err != nil {
				return validation{}, err
			}
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return validation{}, readErr
			}
			if errors.Is(readErr, io.EOF) || n == 0 {
				break
			}
		}
		after, statErr := f.Stat()
		if statErr != nil {
			return validation{}, statErr
		}
		if !sameObservation(info, after) {
			return invalidate("source_changed")
		}
		if read == info.Size() {
			work.FilesChecked++
			if !hmac.Equal(hash.Sum(nil), digest) {
				return invalidate("source_changed")
			}
			file.Freshness, file.ObservedAt, file.CheckedAt = "content_checked", "", time.Now().UTC().Format(time.RFC3339)
		} else if ctx.Err() == nil && time.Now().Before(until) {
			return invalidate("source_changed")
		} else {
			work.BudgetExhausted = true
		}
	} else {
		work.BudgetExhausted = true
	}
	if ctx.Err() != nil {
		return validation{}, ctx.Err()
	}
	// Stat the authorized name as well as checking the original
	// descriptor: a rename replacement must not inherit the old AST.
	current, err := lease.Stat()
	if err != nil {
		if definitiveAccess(err) {
			return invalidate(accessReason(err))
		}
		return validation{}, err
	}
	if !sameObservation(info, current) {
		return invalidate("source_changed")
	}
	if err := lease.Verify(); err != nil {
		return validation{}, err
	}
	return validation{usable: true, file: file}, nil
}

func definitiveAccess(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) || errors.Is(err, tool.ErrDenied) || errors.Is(err, tool.ErrOutsideRoot) || errors.Is(err, workspace.ErrBoundary)
}

func accessReason(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	return "access_denied"
}

func sameObservation(a, b os.FileInfo) bool {
	return a != nil && b != nil && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func (m *Manager) cursor(q *savedQuery, key *index.Key) (string, error) {
	// Nonces and source IDs are encoded once, as binary. Keeping their text
	// representation inside another base64 token wastes the small-page budget.
	nonce, err := base64.RawURLEncoding.DecodeString(q.snapshot.ID)
	if err != nil || len(nonce) != 16 {
		return "", fmt.Errorf("repo map: invalid reservation identity")
	}
	data := make([]byte, 16+32+8, 16+32+8+16)
	copy(data, nonce)
	if key != nil {
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key.SymbolID, "s_"))
		if decodeErr != nil || len(decoded) != sha256.Size || !strings.HasPrefix(key.SymbolID, "s_") {
			return "", fmt.Errorf("repo map: invalid catalog symbol identity")
		}
		copy(data[16:], decoded)
		binary.BigEndian.PutUint64(data[48:], key.Version)
	}
	mac := hmac.New(sha256.New, m.secret[:])
	_, _ = mac.Write(data)
	data = append(data, mac.Sum(nil)[:16]...)
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (m *Manager) resume(ctx context.Context, space *workspace.Session, token string) (*savedQuery, *index.Key, error) {
	if len(token) != 96 {
		return nil, nil, index.ErrStale
	}
	data, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(data) != 72 {
		return nil, nil, index.ErrStale
	}
	mac := hmac.New(sha256.New, m.secret[:])
	_, _ = mac.Write(data[:56])
	if !hmac.Equal(mac.Sum(nil)[:16], data[56:]) {
		return nil, nil, index.ErrStale
	}
	m.mu.Lock()
	q := m.queries[base64.RawURLEncoding.EncodeToString(data[:16])]
	m.mu.Unlock()
	if q == nil || q.session != space.ID() || !time.Now().Before(q.snapshot.ExpiresAt) || q.lease.Verify() != nil || q.policy != m.policyRevision(q.lease.RootID) {
		return nil, nil, index.ErrStale
	}
	version := binary.BigEndian.Uint64(data[48:56])
	if version == 0 {
		return q, nil, nil
	}
	key, err := m.store.LookupKey(ctx, q.snapshot, "s_"+base64.RawURLEncoding.EncodeToString(data[16:48]), version)
	if err != nil {
		return nil, nil, fmt.Errorf("resume map: %w", err)
	}
	return q, &key, nil
}
