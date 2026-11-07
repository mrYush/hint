package repomap

import (
	"errors"
	"sync"
	"time"
)

// Mode is user configuration, independent of effective work state.
type Mode string

// Capability modes never grant filesystem authority.
const (
	ModeOff  Mode = "off"
	ModeAuto Mode = "auto"
	ModeOn   Mode = "on"
)

// ResourceSample is a complete, timestamped observation supplied by a platform sampler.
// CPU and I/O pressure use normalized fractions in [0,1]; unknown is not zero.
type ResourceSample struct {
	At                        time.Time
	AvailableMemory, FreeDisk uint64
	CPU, IO                   float64
	Known                     bool
}

// ResourceProfile holds finite thresholds and their evaluation status.
// Experimental profiles require explicit on mode and are never an automatic recommendation.
type ResourceProfile struct {
	Validated                           bool
	Experimental                        bool
	PauseMemory, ResumeMemory           uint64
	PauseDisk, ResumeDisk               uint64
	PauseCPU, ResumeCPU                 float64
	PauseIO, ResumeIO                   float64
	MaxSampleAge, ResumeDwell, Cooldown time.Duration
}

func (p ResourceProfile) valid() bool {
	return (p.Validated || p.Experimental) && p.PauseMemory > 0 && p.PauseDisk > 0 && p.PauseMemory < p.ResumeMemory && p.PauseDisk < p.ResumeDisk &&
		p.PauseCPU > p.ResumeCPU && p.PauseCPU <= 1 && p.ResumeCPU >= 0 &&
		p.PauseIO > p.ResumeIO && p.PauseIO <= 1 && p.ResumeIO >= 0 &&
		p.MaxSampleAge > 0 && p.ResumeDwell > 0 && p.Cooldown >= 0
}

// ExperimentalResourceProfile is an explicit opt-in safety envelope, not measured acceptance.
// The serial worker, 1 MiB input, 64 MiB WASM memory and catalog quotas remain independent bounds.
func ExperimentalResourceProfile() ResourceProfile {
	return ResourceProfile{Experimental: true,
		PauseMemory: 512 << 20, ResumeMemory: 768 << 20,
		PauseDisk: 512 << 20, ResumeDisk: 768 << 20,
		PauseCPU: .75, ResumeCPU: .5, PauseIO: .5, ResumeIO: .2,
		MaxSampleAge: 15 * time.Second, ResumeDwell: 10 * time.Second, Cooldown: 15 * time.Second}
}

type resourceGovernor struct {
	mode      Mode
	profile   ResourceProfile
	sample    ResourceSample
	inference int
	goodSince time.Time
	pausedAt  time.Time
	paused    bool
	emergency bool
}

func (g *resourceGovernor) currentSample(now time.Time) bool {
	sample := g.sample
	return sample.Known && !sample.At.IsZero() && !now.Before(sample.At) && g.profile.MaxSampleAge > 0 && now.Sub(sample.At) <= g.profile.MaxSampleAge &&
		sample.CPU >= 0 && sample.CPU <= 1 && sample.IO >= 0 && sample.IO <= 1
}

func (g *resourceGovernor) admit(now time.Time) (bool, string) {
	reason := ""
	switch {
	case g.mode == ModeOff:
		reason = "configured_off"
	case g.inference > 0:
		reason = "inference_active"
	case g.emergency:
		reason = "resource_pressure"
	case !g.profile.valid() || (g.mode == ModeAuto && !g.profile.Validated):
		reason = "resource_profile_unvalidated"
	case !g.currentSample(now):
		reason = "telemetry_unavailable"
	case g.sample.AvailableMemory <= g.profile.PauseMemory || g.sample.FreeDisk <= g.profile.PauseDisk || g.sample.CPU >= g.profile.PauseCPU || g.sample.IO >= g.profile.PauseIO:
		reason = "resource_pressure"
	}
	if reason != "" {
		g.goodSince = time.Time{}
		if !g.paused {
			g.pausedAt = now
		}
		g.paused = true
		return false, reason
	}
	if !g.paused {
		return true, ""
	}
	if g.sample.AvailableMemory < g.profile.ResumeMemory || g.sample.FreeDisk < g.profile.ResumeDisk || g.sample.CPU > g.profile.ResumeCPU || g.sample.IO > g.profile.ResumeIO {
		g.goodSince = time.Time{}
		return false, "resource_recovery"
	}
	if g.goodSince.IsZero() {
		g.goodSince = now
	}
	if now.Sub(g.goodSince) < g.profile.ResumeDwell || now.Sub(g.pausedAt) < g.profile.Cooldown {
		return false, "resource_cooldown"
	}
	g.paused = false
	return true, ""
}

// Mode reports configured capability mode, not an admission or coverage claim.
func (m *Manager) Mode() Mode {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.governor.mode
}

// SetMode changes admission without granting roots or declaring a profile validated.
func (m *Manager) SetMode(mode Mode) error {
	if mode != ModeOff && mode != ModeAuto && mode != ModeOn {
		return errors.New("repo map: invalid indexing mode")
	}
	m.mu.Lock()
	m.governor.mode = mode
	m.closeWorker = mode == ModeOff || (mode == ModeAuto && !m.governor.profile.Validated)
	if m.closeWorker && m.sampleCancel != nil {
		m.sampleCancel()
	}
	m.interruptBackgroundLocked()
	m.mu.Unlock()
	m.signal()
	return nil
}

// UpdateResources publishes a complete platform sample and interrupts unsafe work.
func (m *Manager) UpdateResources(sample ResourceSample) {
	m.mu.Lock()
	m.governor.sample = sample
	// Unknown telemetry cannot clear an emergency already observed. Focused
	// catalog reads remain available, but source work waits for healthy data.
	now := m.now()
	low := sample.AvailableMemory <= m.governor.profile.PauseMemory || sample.FreeDisk <= m.governor.profile.PauseDisk
	if low && sample.Known && !sample.At.IsZero() && !now.Before(sample.At) {
		m.governor.emergency = true
	} else if m.governor.currentSample(now) {
		m.governor.emergency = false
	}
	if m.governor.emergency && m.extractionCancel != nil {
		m.extractionCancel()
	}
	m.interruptBackgroundLocked()
	m.mu.Unlock()
	m.signal()
}

func (m *Manager) interruptBackgroundLocked() {
	if ok, _ := m.governor.admit(m.now()); !ok && m.backgroundCancel != nil {
		m.backgroundCancel()
	}
}

// Inference counts an actual provider request until the returned idempotent
// release is called, including error/cancellation/retry paths.
func (m *Manager) Inference() func() {
	m.mu.Lock()
	m.governor.inference++
	if m.sampleCancel != nil {
		m.sampleCancel()
	}
	m.interruptBackgroundLocked()
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			m.governor.inference--
			m.mu.Unlock()
			m.signal()
		})
	}
}

func (m *Manager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}
