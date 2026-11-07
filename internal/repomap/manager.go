package repomap

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mrYush/hint/internal/index"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/repomap/extract"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Extractor processes complete authorized observations under a finite lease.
type Extractor interface {
	Extract(context.Context, extract.Source, extract.Limits) (extract.Result, error)
	Close() error
}

// Options selects local storage and an isolated extractor; it grants no roots.
type Options struct {
	Path    string
	Store   index.Options
	Runner  Extractor
	Environ func() []string
	Mode    Mode
	Profile ResourceProfile
	Now     func() time.Time
	Sampler ResourceSampler
}

type rootWork struct {
	space       *workspace.Session
	info        workspace.RootInfo
	status      Status
	members     map[string]*workspace.Session
	gitRoot     bool
	reconcileAt time.Time
	policyEpoch uint64
	policyFiles map[string]string
	sweepAfter  []byte
	sweepAt     time.Time
	gcAt        time.Time
}

type directoryStream struct {
	item    index.QueueItem
	lease   workspace.Lease
	file    *os.File
	pending []os.DirEntry
	ignored map[string]bool
	readErr error
}

// Manager shares the catalog, worker and bounded frontier across sessions.
type Manager struct {
	store            *index.Store
	runner           Extractor
	git              string
	env              func() []string
	secret           [32]byte
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
	wake             chan struct{}
	mu               sync.Mutex
	queries          map[string]*savedQuery
	roots            map[string]*rootWork
	streams          map[string]*directoryStream
	closed           bool
	closeOnce        sync.Once
	closeErr         error
	slot             chan struct{}
	now              func() time.Time
	governor         resourceGovernor
	backgroundCancel context.CancelFunc
	extractionCancel context.CancelFunc
	foreground       int
	sampler          ResourceSampler
	sampleDone       chan struct{}
	sampleCancel     context.CancelFunc
	closeWorker      bool
}

// New opens the bounded catalog and starts one cooperative discovery worker.
func New(parent context.Context, options Options) (*Manager, error) {
	if options.Mode == "" {
		options.Mode = ModeAuto
	}
	if options.Mode != ModeOff && options.Mode != ModeAuto && options.Mode != ModeOn {
		return nil, errors.New("repo map: invalid indexing mode")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Profile == (ResourceProfile{}) {
		options.Profile = ExperimentalResourceProfile()
	}
	store, err := index.Open(options.Path, options.Store)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{store: store, runner: options.Runner, env: options.Environ,
		ctx: ctx, cancel: cancel, done: make(chan struct{}), wake: make(chan struct{}, 1),
		queries: make(map[string]*savedQuery), roots: make(map[string]*rootWork), streams: make(map[string]*directoryStream)}
	m.slot = make(chan struct{}, 1)
	m.now = options.Now
	m.governor = resourceGovernor{mode: options.Mode, profile: options.Profile}
	m.sampler = options.Sampler
	m.sampleDone = make(chan struct{})
	if _, err := rand.Read(m.secret[:]); err != nil {
		cancel()
		return nil, errors.Join(fmt.Errorf("repo map: cursor key: %w", err), store.Close())
	}
	m.git, _ = exec.LookPath("git")
	go m.sampleResources()
	go m.run()
	return m, nil
}

// Close cancels and joins owned work before releasing descriptors and storage.
func (m *Manager) Close() error {
	m.closeOnce.Do(func() {
		m.mu.Lock()
		m.closed = true
		m.mu.Unlock()
		m.cancel()
		<-m.done
		<-m.sampleDone
		m.slot <- struct{}{}
		defer func() { <-m.slot }()
		for _, stream := range m.streams {
			m.closeErr = errors.Join(m.closeErr, stream.file.Close())
		}
		if m.runner != nil {
			m.closeErr = errors.Join(m.closeErr, m.runner.Close())
		}
		m.closeErr = errors.Join(m.closeErr, m.store.Close())
	})
	return m.closeErr
}

// Tools returns session-bound tools; grants are checked again on each request.
func (m *Manager) Tools(space *workspace.Session) []agentapi.Tool {
	m.mu.Lock()
	enabled := m.governor.mode == ModeOn || (m.governor.mode == ModeAuto && m.governor.profile.Validated)
	m.mu.Unlock()
	tools := []agentapi.Tool{tool.WithLimits(&listTool{manager: m, space: space}, tool.DefaultLimits())}
	if enabled {
		tools = append(tools, tool.WithLimits(&mapTool{manager: m, space: space}, tool.DefaultLimits()))
	}
	return tools
}

// Track makes explicitly attached roots eligible for incremental discovery.
func (m *Manager) Track(space *workspace.Session) {
	roots, _ := space.Snapshot()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	var added []workspace.RootInfo
	wanted := make(map[string]bool, len(roots))
	for _, root := range roots {
		wanted[root.ID] = true
	}
	for id, current := range m.roots {
		if !wanted[id] {
			delete(current.members, space.ID())
			if len(current.members) == 0 {
				delete(m.roots, id)
			} else if current.space == space {
				for _, remaining := range current.members {
					current.space = remaining
					break
				}
			}
		}
	}
	for _, root := range roots {
		if current := m.roots[root.ID]; current != nil {
			if len(current.members) < 128 || current.members[space.ID()] != nil {
				current.members[space.ID()] = space
				current.space = space
			}
		} else if len(m.roots) < 256 {
			m.roots[root.ID] = &rootWork{space: space, info: root, members: map[string]*workspace.Session{space.ID(): space}, status: Status{State: "building"}, reconcileAt: m.now(), policyFiles: make(map[string]string)}
			added = append(added, root)
		}
	}
	m.mu.Unlock()
	for _, root := range added {
		if err := m.store.Enqueue(m.ctx, root.ID, []byte(".")); err != nil {
			m.setStatus(root.ID, Status{State: "paused", Reason: "storage_unavailable"})
		}
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) status(root string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if work := m.roots[root]; work != nil {
		if ok, reason := m.governor.admit(m.now()); !ok {
			return Status{State: "paused", Reason: reason}
		}
		return work.status
	}
	return Status{State: "unavailable", Reason: "root_not_tracked"}
}

func (m *Manager) setStatus(root string, status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if work := m.roots[root]; work != nil {
		work.status = status
	}
}

func (m *Manager) run() {
	defer close(m.done)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	turn := 0
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
		m.closeDetachedStreams()
		m.mu.Lock()
		allowed, reason := m.governor.admit(m.now())
		if !allowed {
			if m.backgroundCancel != nil {
				m.backgroundCancel()
			}
			for _, work := range m.roots {
				work.status = Status{State: "paused", Reason: reason}
			}
			closeWorker := m.closeWorker
			m.mu.Unlock()
			if closeWorker || reason == "resource_pressure" || reason == "configured_off" || reason == "inference_active" || reason == "telemetry_unavailable" {
				select {
				case m.slot <- struct{}{}:
					if m.runner != nil {
						if err := m.runner.Close(); err != nil {
							m.mu.Lock()
							for _, work := range m.roots {
								work.status = Status{State: "paused", Reason: "worker_close_failed"}
							}
							m.mu.Unlock()
						}
					}
					m.mu.Lock()
					m.closeWorker = false
					m.mu.Unlock()
					m.release()
				default:
				}
			}
			continue
		}
		if m.foreground > 0 {
			m.mu.Unlock()
			continue
		}
		ids := make([]string, 0, len(m.roots))
		for id := range m.roots {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		if len(ids) == 0 {
			m.mu.Unlock()
			continue
		}
		root := ids[turn%len(ids)]
		turn++
		work := *m.roots[root]
		m.mu.Unlock()
		m.mu.Lock()
		jobLimit := min(3*time.Second, m.governor.sample.At.Add(m.governor.profile.MaxSampleAge).Sub(m.now()))
		ctx, cancel := context.WithTimeout(m.ctx, jobLimit)
		m.backgroundCancel = cancel
		m.interruptBackgroundLocked()
		m.mu.Unlock()
		err := m.acquire(ctx)
		if err == nil {
			err = m.maintenance(ctx, work)
			if err == nil {
				err = m.step(ctx, work)
			}
			m.release()
		}
		cancel()
		m.mu.Lock()
		m.backgroundCancel = nil
		m.mu.Unlock()
		if err != nil && m.ctx.Err() == nil {
			reason := "index_work_failed"
			switch {
			case errors.Is(err, index.ErrCapacity):
				reason = "storage_capacity"
				m.mu.Lock()
				if current := m.roots[root]; current != nil {
					current.gcAt = time.Time{}
				}
				m.mu.Unlock()
			case errors.Is(err, context.DeadlineExceeded):
				reason = "job_deadline"
			case errors.Is(err, context.Canceled):
				reason = "scheduler_pause"
			}
			m.setStatus(root, Status{State: "paused", Reason: reason})
		}
	}
}

func (m *Manager) step(ctx context.Context, work rootWork) error {
	if err := m.reconcileSlice(ctx, work); err != nil {
		if sourceAccessFailure(err) {
			return m.deferReconciliation(ctx, work, err)
		}
		return err
	}
	items, err := m.store.TakeReady(ctx, work.info.ID, 1, m.now())
	if err != nil {
		return err
	}
	if len(items) == 0 {
		pending, err := m.store.HasPendingWork(ctx, work.info.ID)
		if err != nil {
			return err
		}
		if pending {
			m.setStatus(work.info.ID, Status{State: "partial", Reason: "retry_wait"})
			return nil
		}
		m.setStatus(work.info.ID, Status{State: "ready"})
		// Reconcile at finite intervals; a live directory is never a watcher-only
		// source of truth. The frontier and streams bound each subsequent slice.
		if m.now().Sub(work.reconcileAt) >= time.Minute {
			if err := m.store.Enqueue(ctx, work.info.ID, []byte(".")); err != nil {
				return err
			}
			m.mu.Lock()
			if current := m.roots[work.info.ID]; current != nil {
				current.reconcileAt = m.now()
			}
			m.mu.Unlock()
		}
		return nil
	}
	item := items[0]
	var blocked *index.QueueItem
	m.setStatus(work.info.ID, Status{State: "building"})
	// Rotation is transactional: interruption never drops unfinished work.
	defer func() {
		rotateCtx, cancel := context.WithTimeout(m.ctx, time.Second)
		defer cancel()
		var rotateErr error
		if blocked != nil {
			rotateErr = m.store.Rotate(rotateCtx, *blocked)
		}
		rotateErr = errors.Join(rotateErr, m.store.Rotate(rotateCtx, item))
		if rotateErr != nil && m.ctx.Err() == nil {
			m.setStatus(work.info.ID, Status{State: "paused", Reason: "storage_unavailable"})
		}
	}()
	lease, err := work.space.Resolve(work.info.ID, string(item.Path))
	if err != nil {
		return m.sourceFailure(ctx, work, item, err)
	}
	info, err := lease.Stat()
	if err != nil {
		return m.sourceFailure(ctx, work, item, err)
	}
	if !info.IsDir() {
		if err := m.refreshHeld(ctx, lease); err != nil {
			if sourceAccessFailure(err) {
				return m.sourceFailure(ctx, work, item, err)
			}
			return err
		}
		return m.store.Ack(ctx, item)
	}
	for activeKey, active := range m.streams {
		if active.lease.RootID != work.info.ID || bytes.Equal(active.item.Path, item.Path) {
			continue
		}
		if !active.lease.Valid() {
			delete(m.streams, activeKey)
			if err := active.file.Close(); err != nil {
				return err
			}
			continue
		}
		// Advance the retained parent whenever a child cannot open yet, then
		// rotate just that child so files behind it retain their opportunity.
		// Each blocked visit advances a parent page rather than churning the
		// whole child queue between pages of a wide directory.
		current, err := m.store.QueueLookup(ctx, work.info.ID, active.item.Path)
		if err != nil {
			return err
		}
		previous := item
		blocked = &previous
		item, lease = current, active.lease
		break
	}
	key := work.info.ID + "\x00" + string(item.Path)
	stream := m.streams[key]
	if stream != nil && !stream.lease.Valid() {
		delete(m.streams, key)
		if err := stream.file.Close(); err != nil {
			return err
		}
		stream = nil
	}
	if stream == nil {
		if len(m.streams) >= 8 {
			m.setStatus(work.info.ID, Status{State: "paused", Reason: "directory_stream_capacity"})
			return nil
		}
		f, err := lease.OpenDirectory()
		if err != nil {
			return m.sourceFailure(ctx, work, item, err)
		}
		stream = &directoryStream{item: item, lease: lease, file: f}
		m.streams[key] = stream
	}
	stream.item = item
	if err := m.observePolicy(ctx, work, lease.RelativePath); err != nil {
		return m.retrySource(ctx, work, item, err)
	}
	if len(stream.pending) == 0 && stream.readErr == nil {
		stream.pending, stream.readErr = stream.file.ReadDir(32)
		stream.ignored = nil
	}
	// A retained batch may outlive an ignore edit; recheck its remaining paths
	// on every slice rather than carrying an obsolete allow decision forward.
	if len(stream.pending) > 0 {
		paths := make([]string, 0, len(stream.pending))
		directories := make([]string, 0, len(stream.pending))
		for _, entry := range stream.pending {
			rel := filepath.Join(lease.RelativePath, entry.Name())
			paths = append(paths, rel)
			if entry.IsDir() {
				directories = append(directories, rel)
			}
		}
		stream.ignored, err = m.gitIgnored(ctx, lease, paths, directories...)
		if err != nil {
			return m.retrySource(ctx, work, item, err)
		}
	}
	for len(stream.pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := stream.pending[0]
		rel := filepath.Join(lease.RelativePath, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 || project.Basic.Ignored(filepath.ToSlash(rel), entry.IsDir()) || stream.ignored[filepath.ToSlash(rel)] {
			stream.pending = stream.pending[1:]
			continue
		}
		child, resolveErr := work.space.Resolve(work.info.ID, rel)
		if errors.Is(resolveErr, workspace.ErrBoundary) {
			file := index.File{RootID: work.info.ID, Path: []byte(filepath.ToSlash(rel)), Kind: index.KindBoundary,
				State: index.StateOmitted, Reason: "nested_root", ObservedAt: time.Now()}
			if err := lease.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
				return err
			}
			stream.pending = stream.pending[1:]
			continue
		}
		if resolveErr != nil {
			if errors.Is(resolveErr, workspace.ErrChanged) || errors.Is(resolveErr, workspace.ErrClosed) || errors.Is(resolveErr, workspace.ErrUnknownRoot) {
				return resolveErr
			}
			if err := m.deferUnavailable(ctx, work, lease, rel, entry.IsDir()); err != nil {
				return err
			}
			stream.pending = stream.pending[1:]
			continue
		}
		childInfo, statErr := child.Stat()
		if statErr != nil {
			if errors.Is(statErr, workspace.ErrChanged) || ctx.Err() != nil {
				return errors.Join(statErr, ctx.Err())
			}
			if err := m.deferUnavailable(ctx, work, lease, rel, entry.IsDir()); err != nil {
				return err
			}
			stream.pending = stream.pending[1:]
			continue
		}
		if !childInfo.IsDir() && !childInfo.Mode().IsRegular() {
			stream.pending = stream.pending[1:]
			continue
		}
		file := index.File{RootID: work.info.ID, Path: []byte(filepath.ToSlash(child.RelativePath)), Size: childInfo.Size(),
			ModTime: childInfo.ModTime(), ObservedAt: time.Now(), Kind: index.KindFile, State: index.StatePending}
		if childInfo.IsDir() {
			file.Kind, file.State = index.KindDirectory, index.StateIndexed
		}
		id := index.EntryID(file.RootID, string(file.Path))
		old, oldErr := m.store.EntryLookup(ctx, file.RootID, id)
		if oldErr != nil && !errors.Is(oldErr, index.ErrNotFound) {
			return oldErr
		}
		if oldErr != nil || old.Size != file.Size || !old.ModTime.Equal(file.ModTime) {
			if err := work.space.Publish(lease, func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
				return err
			}
		}
		if err := m.store.Enqueue(ctx, work.info.ID, file.Path); err != nil {
			return err
		}
		stream.pending = stream.pending[1:]
	}
	if errors.Is(stream.readErr, io.EOF) {
		delete(m.streams, key)
		observed, statErr := stream.file.Stat()
		if err := errors.Join(statErr, stream.file.Close()); err != nil {
			return m.retrySource(ctx, work, item, err)
		}
		current, err := lease.Stat()
		if err != nil {
			return m.sourceFailure(ctx, work, item, err)
		}
		if !os.SameFile(observed, current) {
			return workspace.ErrChanged
		}
		if lease.RelativePath != "." {
			file := index.File{RootID: lease.RootID, Path: item.Path, Kind: index.KindDirectory,
				State: index.StateIndexed, Size: current.Size(), ModTime: current.ModTime(), ObservedAt: m.now()}
			old, err := m.store.EntryLookup(ctx, item.RootID, index.EntryID(item.RootID, string(item.Path)))
			if err != nil && !errors.Is(err, index.ErrNotFound) {
				return err
			}
			if err != nil || old.Kind != file.Kind || old.State != file.State || old.Size != file.Size || !old.ModTime.Equal(file.ModTime) {
				if err := lease.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
					return err
				}
			}
		}
		return m.store.Ack(ctx, stream.item)
	}
	if stream.readErr != nil {
		return m.retrySource(ctx, work, item, stream.readErr)
	}
	return nil
}

func (m *Manager) refresh(ctx context.Context, lease workspace.Lease) (resultErr error) {
	m.mu.Lock()
	off := m.governor.mode == ModeOff
	m.mu.Unlock()
	if off {
		return errors.New("repo map: indexing is configured off")
	}
	ctx, release, err := m.acquireFocused(ctx)
	if err != nil {
		return err
	}
	defer release()
	return m.refreshHeld(ctx, lease)
}

func (m *Manager) acquireFocused(ctx context.Context) (context.Context, func(), error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, nil, errors.New("repo map: index closed")
	}
	m.foreground++
	if m.backgroundCancel != nil {
		m.backgroundCancel()
	}
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(m.ctx, cancel)
	cleanup := func() {
		stop()
		cancel()
		m.mu.Lock()
		m.foreground--
		m.mu.Unlock()
		m.signal()
	}
	if err := m.acquire(ctx); err != nil {
		cleanup()
		return nil, nil, err
	}
	var once sync.Once
	return ctx, func() { once.Do(func() { m.release(); cleanup() }) }, nil
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.slot <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.release()
			return err
		}
		if err := m.ctx.Err(); err != nil {
			m.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-m.ctx.Done():
		return m.ctx.Err()
	}
}
func (m *Manager) release() { <-m.slot }

func (m *Manager) refreshHeld(ctx context.Context, lease workspace.Lease) (resultErr error) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.extractionCancel = cancel
	if m.governor.emergency {
		cancel()
	}
	m.mu.Unlock()
	defer func() {
		cancel()
		m.mu.Lock()
		m.extractionCancel = nil
		emergency := m.governor.emergency
		m.mu.Unlock()
		if emergency {
			m.setStatus(lease.RootID, Status{State: "paused", Reason: "resource_pressure"})
			resultErr = errors.Join(resultErr, errors.New("repo map: source work paused by known memory or disk pressure"))
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := lease.OpenRegular()
	if errors.Is(err, workspace.ErrNotRegular) {
		return m.omitNonRegular(ctx, lease)
	}
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if err := m.excluded(ctx, lease, false); err != nil {
		return err
	}
	file := index.File{RootID: lease.RootID, ID: index.EntryID(lease.RootID, filepath.ToSlash(lease.RelativePath)),
		Path: []byte(filepath.ToSlash(lease.RelativePath)), Size: info.Size(), ModTime: info.ModTime(), ObservedAt: time.Now(),
		Kind: index.KindFile, State: index.StateOmitted}
	defer func() {
		if errors.Is(resultErr, workspace.ErrChanged) && lease.Valid() {
			file.State, file.Reason, file.Hash = index.StateOmitted, "source_changed", ""
			resultErr = errors.Join(resultErr, lease.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err }))
		}
	}()
	language := extract.Language(lease.RelativePath)
	file.Policy = extract.PolicyVersion(language)
	limits := extract.DefaultLimits()
	var symbols []index.Symbol
	switch {
	case language == "":
		file.Reason = "unsupported"
	case info.Size() > int64(limits.MaxInputBytes):
		file.Reason = "oversized"
	case m.runner == nil:
		file.Reason = "extractor_unavailable"
	default:
		data, err := readObservedSource(ctx, f, info, limits.MaxInputBytes)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		file.Hash = "sha256:" + hex.EncodeToString(digest[:])
		old, err := m.store.EntryLookup(ctx, file.RootID, file.ID)
		cacheable := old.State == index.StateIndexed || (old.State == index.StateOmitted && (old.Reason == "work_budget_exceeded" || old.Reason == "output_budget_exceeded" || old.Reason == "parse_failed"))
		if err == nil && old.Hash == file.Hash && old.Policy == file.Policy && old.ModTime.Equal(file.ModTime) && cacheable {
			return errors.Join(ctx.Err(), lease.Verify())
		}
		result, err := m.runner.Extract(ctx, extract.Source{Path: filepath.Base(lease.RelativePath), Content: data}, limits)
		if err != nil {
			return err
		}
		file.Reason = result.Omission
		if result.Complete {
			file.State = index.StateIndexed
			for _, decl := range result.Declarations {
				symbols = append(symbols, index.Symbol{FileID: file.ID, Kind: decl.Kind, Name: decl.Name,
					Signature: decl.Signature, Enclosing: decl.Enclosing, StartByte: int64(decl.StartByte),
					EndByte: int64(decl.EndByte), StartLine: decl.StartLine, EndLine: decl.EndLine})
			}
		}
		current, err := f.Stat()
		if err != nil || !sameObservation(info, current) {
			return workspace.ErrChanged
		}
		// Rehash the authorized current path, not only the old open descriptor:
		// replacement and same-size/restored-mtime edits during parsing invalidate it.
		verify, err := lease.OpenRegular()
		if errors.Is(err, workspace.ErrNotRegular) {
			return errors.Join(workspace.ErrChanged, err)
		}
		if err != nil {
			return err
		}
		verifyInfo, statErr := verify.Stat()
		hash := sha256.New()
		n, readErr := io.Copy(hash, io.LimitReader(contextReader{ctx: ctx, reader: verify}, int64(limits.MaxInputBytes)+1))
		verifyAfter, afterErr := verify.Stat()
		closeErr := verify.Close()
		if err := errors.Join(statErr, readErr, afterErr, closeErr); err != nil {
			return err
		}
		if !sameObservation(info, verifyInfo) || !sameObservation(verifyInfo, verifyAfter) || n != int64(len(data)) || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != file.Hash {
			return workspace.ErrChanged
		}
	}
	if err := lease.Verify(); err != nil {
		return err
	}
	if err := m.excluded(ctx, lease, false); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if file.State == index.StateOmitted && file.Hash == "" {
		old, err := m.store.EntryLookup(ctx, file.RootID, file.ID)
		if err != nil && !errors.Is(err, index.ErrNotFound) {
			return err
		}
		if err == nil && old.State == file.State && old.Reason == file.Reason && old.Policy == file.Policy && old.Size == file.Size && old.ModTime.Equal(file.ModTime) {
			return lease.Verify()
		}
	}
	// Store writes are bounded by its operation timeout; hold publication authority
	// through that write so revocation cannot race a successful publication.
	return lease.Publish(func() error { _, err := m.store.PutSource(ctx, file, symbols); return err })
}

func (m *Manager) omitNonRegular(ctx context.Context, lease workspace.Lease) error {
	info, err := lease.Stat()
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		return workspace.ErrChanged
	}
	if err := m.excluded(ctx, lease, info.IsDir()); err != nil {
		return err
	}
	file := index.File{RootID: lease.RootID, Path: []byte(filepath.ToSlash(lease.RelativePath)),
		Kind: index.KindFile, State: index.StateOmitted, Reason: "non_regular", Size: info.Size(),
		ModTime: info.ModTime(), ObservedAt: m.now()}
	if info.IsDir() {
		file.Kind = index.KindDirectory
	}
	if err := lease.Verify(); err != nil {
		return err
	}
	return lease.Publish(func() error { _, err := m.store.PutSource(ctx, file, nil); return err })
}

func (m *Manager) deferUnavailable(ctx context.Context, work rootWork, parent workspace.Lease, path string, directory bool) error {
	file := index.File{RootID: work.info.ID, Path: []byte(filepath.ToSlash(path)), Kind: index.KindFile, State: index.StateOmitted, Reason: "unavailable", ObservedAt: m.now()}
	if directory {
		file.Kind = index.KindDirectory
	}
	if err := work.space.Publish(parent, func() error { _, err := m.store.PutSource(ctx, file, nil); return err }); err != nil {
		return err
	}
	return m.store.Enqueue(ctx, work.info.ID, file.Path)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type observedSource interface {
	io.Reader
	Stat() (os.FileInfo, error)
}

func readObservedSource(ctx context.Context, source observedSource, before os.FileInfo, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, reader: source}, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	after, err := source.Stat()
	if err != nil {
		return nil, errors.Join(workspace.ErrChanged, err)
	}
	if len(data) > limit || !sameObservation(before, after) || int64(len(data)) != before.Size() {
		return nil, workspace.ErrChanged
	}
	return data, nil
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (m *Manager) excluded(ctx context.Context, lease workspace.Lease, directory bool) error {
	if project.Basic.Ignored(filepath.ToSlash(lease.RelativePath), directory) {
		return tool.ErrDenied
	}
	var directories []string
	if directory {
		directories = []string{lease.RelativePath}
	}
	ignored, err := m.gitIgnored(ctx, lease, []string{lease.RelativePath}, directories...)
	if err != nil {
		return err
	}
	if ignored[filepath.ToSlash(lease.RelativePath)] {
		return tool.ErrDenied
	}
	return nil
}

func (m *Manager) gitIgnored(ctx context.Context, lease workspace.Lease, paths []string, directories ...string) (map[string]bool, error) {
	m.mu.Lock()
	work := m.roots[lease.RootID]
	var root string
	knownGit := false
	primary := false
	if work != nil {
		root = work.info.Path
		knownGit = work.gitRoot
		primary = work.info.Primary
	}
	m.mu.Unlock()
	if root == "" {
		root = lease.Path
		if lease.RelativePath != "." {
			for range strings.Split(filepath.Clean(lease.RelativePath), string(filepath.Separator)) {
				root = filepath.Dir(root)
			}
		}
	}
	policyRoot := root
	// Preserve the primary startup directory's established enclosing-Git
	// policy without granting access outside its workspace root. Explicit
	// secondary ordinary folders use only their own Git metadata.
	if primary {
		if enclosing, ok := project.FindGitRoot(root); ok {
			policyRoot = enclosing
		}
	}
	marker, markerErr := os.Lstat(filepath.Join(policyRoot, ".git"))
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("repo map: inspect git root: %w", markerErr)
	}
	if markerErr == nil {
		if marker.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("repo map: symlinked Git metadata is unavailable")
		}
		knownGit = true
		m.mu.Lock()
		if current := m.roots[lease.RootID]; current != nil {
			current.gitRoot = true
		}
		m.mu.Unlock()
	}
	if !knownGit {
		return map[string]bool{}, nil
	}
	if m.git == "" {
		return nil, errors.New("repo map: git ignore policy unavailable")
	}
	checkCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, m.git, "-C", root, "-c", "core.fsmonitor=false", "check-ignore", "-z", "--stdin")
	var input strings.Builder
	for _, path := range paths {
		input.WriteString(filepath.ToSlash(path))
		input.WriteByte(0)
	}
	if input.Len() > 1<<20 {
		return nil, index.ErrTooLarge
	}
	cmd.Stdin = strings.NewReader(input.String())
	output := &boundedGitOutput{}
	cmd.Stdout = output
	cmd.WaitDelay = 100 * time.Millisecond
	if m.env != nil {
		cmd.Env = m.env()
	}
	err := cmd.Run()
	if output.exceeded {
		return nil, index.ErrTooLarge
	}
	if checkCtx.Err() != nil {
		return nil, checkCtx.Err()
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		return nil, fmt.Errorf("repo map: ignore policy unavailable: %w", err)
	}
	ignored := make(map[string]bool)
	for _, path := range bytes.Split(output.data, []byte{0}) {
		if len(path) > 0 {
			ignored[string(path)] = true
		}
	}
	for _, path := range directories {
		path = filepath.ToSlash(path)
		if !ignored[path] {
			continue
		}
		tracked, err := m.gitTrackedDescendant(ctx, root, path)
		if err != nil {
			return nil, err
		}
		if tracked {
			delete(ignored, path)
		}
	}
	return ignored, nil
}

// An ignored parent may contain force-added tracked files. Stop at the first
// output byte; never materialize the directory's potentially huge tracked list.
func (m *Manager) gitTrackedDescendant(ctx context.Context, root, path string) (bool, error) {
	checkCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	output := &firstTrackedOutput{cancel: cancel}
	cmd := exec.CommandContext(checkCtx, m.git, "-C", root, "-c", "core.fsmonitor=false", "--literal-pathspecs", "ls-files", "-z", "--cached", "--", path+"/")
	cmd.Stdout = output
	cmd.WaitDelay = 100 * time.Millisecond
	if m.env != nil {
		cmd.Env = m.env()
	}
	err := cmd.Run()
	if output.found && ctx.Err() == nil {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("repo map: tracked directory policy unavailable: %w", err)
	}
	return false, ctx.Err()
}

type firstTrackedOutput struct {
	cancel context.CancelFunc
	found  bool
}

func (o *firstTrackedOutput) Write(data []byte) (int, error) {
	if len(data) > 0 {
		o.found = true
		o.cancel()
	}
	return len(data), nil
}

type boundedGitOutput struct {
	data     []byte
	exceeded bool
}

func (b *boundedGitOutput) Write(data []byte) (int, error) {
	if len(data) > (1<<20)-len(b.data) {
		b.exceeded = true
		return 0, index.ErrTooLarge
	}
	b.data = append(b.data, data...)
	return len(data), nil
}
