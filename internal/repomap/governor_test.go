package repomap

import (
	"math"
	"testing"
	"time"
)

func testResourceProfile() ResourceProfile {
	// Fixture units are deliberately synthetic, not a deployable device profile.
	return ResourceProfile{Validated: true, PauseMemory: 10, ResumeMemory: 20, PauseDisk: 10, ResumeDisk: 20,
		PauseCPU: .8, ResumeCPU: .4, PauseIO: .8, ResumeIO: .4, MaxSampleAge: time.Minute, ResumeDwell: 5 * time.Second, Cooldown: 10 * time.Second}
}

func TestGovernorUnknownPressureAndHysteresis(t *testing.T) {
	now := time.Unix(1000, 0)
	g := resourceGovernor{mode: ModeAuto, profile: testResourceProfile()}
	if ok, why := g.admit(now); ok || why != "telemetry_unavailable" {
		t.Fatalf("unknown = %v,%s", ok, why)
	}
	g.sample = ResourceSample{At: now, Known: true, AvailableMemory: 30, FreeDisk: 30, CPU: .1, IO: .1}
	if ok, _ := g.admit(now); ok {
		t.Fatal("resumed without dwell/cooldown")
	}
	if ok, _ := g.admit(now.Add(9 * time.Second)); ok {
		t.Fatal("resumed before cooldown")
	}
	if ok, why := g.admit(now.Add(10 * time.Second)); !ok {
		t.Fatalf("recovery=%s", why)
	}
	g.sample.CPU = .9
	if ok, why := g.admit(now.Add(11 * time.Second)); ok || why != "resource_pressure" {
		t.Fatalf("pressure=%v,%s", ok, why)
	}
	g.sample.CPU = .5
	if ok, why := g.admit(now.Add(30 * time.Second)); ok || why != "resource_recovery" {
		t.Fatalf("deadband=%v,%s", ok, why)
	}
	g.sample.CPU = .1
	if ok, _ := g.admit(now.Add(31 * time.Second)); ok {
		t.Fatal("deadband accumulated dwell")
	}
	if ok, why := g.admit(now.Add(36 * time.Second)); !ok {
		t.Fatalf("second recovery=%s", why)
	}
	if ok, why := g.admit(now.Add(61 * time.Second)); ok || why != "telemetry_unavailable" {
		t.Fatalf("stale=%v,%s", ok, why)
	}
	g.sample.At = now.Add(62 * time.Second)
	g.sample.CPU = math.NaN()
	if ok, why := g.admit(now.Add(62 * time.Second)); ok || why != "telemetry_unavailable" {
		t.Fatalf("nan=%v,%s", ok, why)
	}
}

func TestGovernorUnvalidatedAndInference(t *testing.T) {
	now := time.Now()
	g := resourceGovernor{mode: ModeAuto, sample: ResourceSample{At: now, Known: true, AvailableMemory: 100, FreeDisk: 100}}
	if ok, why := g.admit(now); ok || why != "resource_profile_unvalidated" {
		t.Fatalf("unvalidated=%v,%s", ok, why)
	}
	g.profile = testResourceProfile()
	g.inference = 2
	if ok, why := g.admit(now); ok || why != "inference_active" {
		t.Fatalf("inference=%v,%s", ok, why)
	}
	g.mode = ModeOff
	if ok, why := g.admit(now); ok || why != "configured_off" {
		t.Fatalf("off=%v,%s", ok, why)
	}
}
