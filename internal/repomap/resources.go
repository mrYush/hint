package repomap

import (
	"context"
	"fmt"
	"time"
)

// ResourceSampler returns a bounded observation; failure must never imply zero pressure.
type ResourceSampler func(context.Context) (ResourceSample, error)

// LocalResourceSampler selects the platform adapter for the catalog filesystem.
// Unsupported platforms return unknown samples and cannot admit background work.
func LocalResourceSampler(catalogPath string) ResourceSampler {
	return localResourceSampler(catalogPath)
}

func (m *Manager) sampleResources() {
	defer close(m.sampleDone)
	if m.sampler == nil {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		m.mu.Lock()
		needed := m.governor.inference == 0 && (m.governor.mode == ModeOn || (m.governor.mode == ModeAuto && m.governor.profile.Validated))
		m.mu.Unlock()
		if needed {
			ctx, cancel := context.WithTimeout(m.ctx, 3*time.Second)
			m.mu.Lock()
			m.sampleCancel = cancel
			if m.governor.inference != 0 || m.governor.mode == ModeOff {
				cancel()
			}
			m.mu.Unlock()
			sample, err := m.sampler(ctx)
			cancel()
			m.mu.Lock()
			m.sampleCancel = nil
			m.mu.Unlock()
			if err != nil {
				sample = ResourceSample{At: m.now()}
			}
			m.UpdateResources(sample)
		}
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// StatusText returns bounded diagnostics for an explicit user status request.
func (m *Manager) StatusText() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	allowed, reason := m.governor.admit(m.now())
	state := "paused"
	if allowed {
		state, reason = "eligible", "none"
	}
	profile := "unconfigured"
	if m.governor.profile.Validated {
		profile = "validated"
	} else if m.governor.profile.Experimental {
		profile = "experimental (manual opt-in; acceptance unmeasured)"
	}
	age := "unknown"
	if m.governor.sample.Known && !m.governor.sample.At.IsZero() {
		age = m.now().Sub(m.governor.sample.At).Round(time.Second).String()
	}
	budget := m.store.Options()
	return fmt.Sprintf("Index mode: %s (shared by all sessions)\nBackground: %s; reason: %s\nProfile: %s\nTelemetry: known=%t; age=%s\nRoots: %d; foreground requests: %d; inference requests: %d\nLimits: one worker, 32 directory entries/slice, 8 directory streams total and at most one per root, 1 MiB source, 64 MiB WASM memory\nStorage: %d bytes total allowance including journals; approximately one third admits database pages; SQLite cache=%d KiB\n", m.governor.mode, state, reason, profile, m.governor.sample.Known, age, len(m.roots), m.foreground, m.governor.inference, budget.MaxBytes, budget.CacheKiB)
}

func (m *Manager) closeDetachedStreams() {
	select {
	case m.slot <- struct{}{}:
		defer m.release()
	default:
		return
	}
	for key, stream := range m.streams {
		m.mu.Lock()
		work := m.roots[stream.lease.RootID]
		m.mu.Unlock()
		if work != nil && stream.lease.Valid() {
			continue
		}
		delete(m.streams, key)
		if err := stream.file.Close(); err != nil {
			m.setStatus(stream.lease.RootID, Status{State: "paused", Reason: "directory_close_failed"})
		}
	}
}

// policyRevision reports only changes observed by bounded background discovery.
// Every returned query candidate still requires the current exclusion policy check.
func (m *Manager) policyRevision(root string) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if work := m.roots[root]; work != nil {
		return work.policyEpoch
	}
	return 0
}
