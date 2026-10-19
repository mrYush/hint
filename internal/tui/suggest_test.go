package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
)

// completionEngine is a fake engine with completion in the given mode and
// short timers, so the ticks a test runs by hand do not sleep.
func completionEngine(mode string) *fakeEngine {
	return &fakeEngine{settings: rpc.CompletionSettings{Mode: mode, IdleAfterMs: 1, WarnAfterMs: 1}}
}

func (h *harness) completes() int {
	h.eng.mu.Lock()
	defer h.eng.mu.Unlock()
	return len(h.eng.completes)
}

func TestTabAsksForAndAcceptsACompletion(t *testing.T) {
	h := newHarness(t, completionEngine("tab"), Options{})
	h.run(h.m.Init())
	if h.m.cmpl.mode() != "tab" {
		t.Fatalf("mode after Init = %q", h.m.cmpl.mode())
	}
	h.typeText("add a test for")
	cmd := h.press("tab")
	if h.m.cmpl.inflight == nil || !strings.Contains(h.m.statusLine(), "completing") {
		t.Fatalf("tab did not ask: inflight %v, status %q", h.m.cmpl.inflight, h.m.statusLine())
	}
	h.run(cmd)
	if h.m.cmpl.sugg == nil || !strings.Contains(h.m.statusLine(), "tab accepts: and tests") {
		t.Fatalf("no suggestion shown: %q", h.m.statusLine())
	}
	if n := h.completes(); n != 1 {
		t.Errorf("the core saw %d requests, want 1", n)
	}
	// Tab takes it; the input is the text plus the suggestion.
	h.press("tab")
	if got := h.m.ta.Value(); got != "add a test for and tests" || h.m.cmpl.sugg != nil {
		t.Fatalf("after accepting: %q, sugg %v", got, h.m.cmpl.sugg)
	}
	if n := h.completes(); n != 1 {
		t.Errorf("accepting asked the core again: %d requests", n)
	}

	// Any other key withdraws a suggestion.
	h.run(h.press("tab"))
	if h.m.cmpl.sugg == nil {
		t.Fatal("a second request got no suggestion")
	}
	h.typeText("x")
	if h.m.cmpl.sugg != nil || strings.Contains(h.m.statusLine(), "tab accepts") {
		t.Errorf("typing kept the suggestion: %q", h.m.statusLine())
	}
	// A command or an empty input never asks.
	before := h.completes()
	h.m.ta.Reset()
	h.press("tab")
	h.typeText("/he")
	h.press("tab")
	if h.completes() != before || h.m.ta.Value() != "/help" {
		t.Errorf("tab on a command or nothing asked the core: %d requests, input %q", h.completes()-before, h.m.ta.Value())
	}
}

func TestCompletionOffSaysSoOnTab(t *testing.T) {
	h := newHarness(t, completionEngine("off"), Options{})
	h.run(h.m.Init())
	h.typeText("add a test for")
	h.run(h.press("tab"))
	if h.completes() != 0 || !strings.Contains(h.m.statusLine(), "completion is off") {
		t.Errorf("off mode: %d requests, status %q", h.completes(), h.m.statusLine())
	}
	// The core's own refusal switches the shell off until /completion.
	eng := completionEngine("tab")
	eng.completionOff = true
	h = newHarness(t, eng, Options{})
	h.run(h.m.Init())
	h.typeText("add a test for")
	h.run(h.press("tab"))
	if h.m.cmpl.disabled == "" || !strings.Contains(h.m.statusLine(), "completion.mode is off") {
		t.Fatalf("the core's refusal was not taken: %q / %q", h.m.cmpl.disabled, h.m.statusLine())
	}
	h.run(h.press("tab"))
	if h.completes() != 1 {
		t.Errorf("asked again after the core refused: %d requests", h.completes())
	}
}

func TestIdleCompletionObeysTheThresholds(t *testing.T) {
	h := newHarness(t, completionEngine("idle"), Options{})
	h.run(h.m.Init())
	// Short text: the tick after the edit asks nothing.
	h.typeText("short one")
	h.run(h.update(idleTickMsg{seq: h.m.cmpl.seq}))
	if h.completes() != 0 {
		t.Fatalf("a short input asked the core")
	}
	// Long enough: the newest tick asks, a stale one does not.
	h.typeText(" and a longer one")
	h.run(h.update(idleTickMsg{seq: h.m.cmpl.seq - 1}))
	if h.completes() != 0 {
		t.Fatalf("a stale tick asked the core")
	}
	h.run(h.update(idleTickMsg{seq: h.m.cmpl.seq}))
	if h.completes() != 1 || h.m.cmpl.sugg == nil {
		t.Fatalf("the idle tick did not ask: %d requests, sugg %v", h.completes(), h.m.cmpl.sugg)
	}
	if strings.Contains(h.m.statusLine(), "completing") {
		t.Error("the idle trigger announced itself in the status line")
	}
	// Right after, three more characters are inside the gap: no request.
	h.typeText("abc")
	h.run(h.update(idleTickMsg{seq: h.m.cmpl.seq}))
	if h.completes() != 1 {
		t.Errorf("a request inside the gap: %d requests", h.completes())
	}
	// Tab still works in idle mode? No: tab is not in the idle mode.
	h.run(h.press("tab"))
	if h.completes() != 1 {
		t.Errorf("tab asked in idle mode: %d requests", h.completes())
	}
}

func TestCompletionFailuresSwitchItOff(t *testing.T) {
	eng := completionEngine("tab")
	eng.completeErr = errors.New("boom")
	h := newHarness(t, eng, Options{})
	h.run(h.m.Init())
	h.typeText("add a test for")
	for i := 1; i <= 3; i++ {
		h.run(h.press("tab"))
		if i < 3 && (h.m.cmpl.disabled != "" || !strings.Contains(h.m.statusLine(), "completion failed")) {
			t.Fatalf("failure %d: disabled %q, status %q", i, h.m.cmpl.disabled, h.m.statusLine())
		}
	}
	if !strings.Contains(h.m.cmpl.disabled, "3 failures") || !strings.Contains(h.view(), "switched off after 3 failures") {
		t.Fatalf("three failures did not switch completion off: %q\n%s", h.m.cmpl.disabled, h.view())
	}
	h.run(h.press("tab"))
	if h.completes() != 3 {
		t.Errorf("asked after being switched off: %d requests", h.completes())
	}
	// /completion on tries again.
	eng.mu.Lock()
	eng.completeErr = nil
	eng.mu.Unlock()
	h.m.ta.Reset()
	h.run(h.submit("/completion on"))
	h.typeText("add a test for")
	h.run(h.press("tab"))
	if h.m.cmpl.sugg == nil {
		t.Errorf("/completion on did not bring it back: %q", h.m.statusLine())
	}
}

func TestCompletionCommandReportsModeAndCost(t *testing.T) {
	h := newHarness(t, completionEngine("both"), Options{})
	h.run(h.m.Init())
	h.typeText("add a test for")
	h.run(h.press("tab"))
	h.m.ta.Reset()
	h.run(h.submit("/completion"))
	v := h.view()
	for _, want := range []string{"completion: both (config)", "the session's profile", "requests: 1 (0 failed)", "tokens: 10 in, 2 out"} {
		if !strings.Contains(v, want) {
			t.Errorf("report lacks %q:\n%s", want, v)
		}
	}
	h.run(h.submit("/completion off"))
	if h.m.cmpl.mode() != "off" || !strings.Contains(h.view(), "completion: off (this session)") {
		t.Errorf("/completion off: mode %q", h.m.cmpl.mode())
	}
	h.typeText("add a test for")
	h.run(h.press("tab"))
	if h.completes() != 1 {
		t.Errorf("asked while off for the session: %d requests", h.completes())
	}
	h.m.ta.Reset()
	h.run(h.submit("/completion sometimes"))
	if !strings.Contains(h.m.statusLine(), "usage") {
		t.Errorf("a bad mode was not refused: %q", h.m.statusLine())
	}
}

func TestLateCompletionIsDropped(t *testing.T) {
	h := newHarness(t, completionEngine("tab"), Options{})
	h.run(h.m.Init())
	h.typeText("add a test for")
	ask := h.press("tab")
	// The text changes before the answer arrives: the answer is for the
	// old text and is dropped, the request is no longer waited for.
	h.typeText("x")
	if h.m.cmpl.inflight != nil {
		t.Fatal("an edit left the request in flight")
	}
	h.run(ask)
	if h.m.cmpl.sugg != nil || strings.Contains(h.m.statusLine(), "tab accepts") {
		t.Errorf("a late answer was shown: %q", h.m.statusLine())
	}
}

func TestTabFetchesTheModelsOfAProfile(t *testing.T) {
	eng := &fakeEngine{profiles: twoProfiles(), models: map[string][]string{"local": {"qwen", "llama"}}}
	setup := &Setup{Describe: "d", Profile: "local", Set: func(string, string, bool) (string, error) { return "", nil }}
	h := newHarness(t, eng, Options{Setup: setup})
	h.run(h.m.Init())
	h.typeText("/profile local ")
	cmd := h.press("tab")
	if !strings.Contains(h.m.statusLine(), "loading the models of local") {
		t.Fatalf("the first tab did not fetch: %q", h.m.statusLine())
	}
	h.run(cmd)
	if !strings.Contains(h.m.statusLine(), "2 models of local") {
		t.Fatalf("models not reported: %q", h.m.statusLine())
	}
	h.press("tab")
	if st := h.m.statusLine(); !strings.Contains(st, "qwen") || !strings.Contains(st, "llama") {
		t.Errorf("models not offered: %q", st)
	}
	h.typeText("q")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/profile local qwen" {
		t.Errorf("/profile local q + tab = %q", got)
	}
	// The cache serves /config set too; an unknown profile is reported once.
	h.m.ta.Reset()
	h.typeText("/config set providers.local.model ")
	h.press("tab")
	if st := h.m.statusLine(); !strings.Contains(st, "qwen") {
		t.Errorf("cached models not offered for /config: %q", st)
	}
	h.m.ta.Reset()
	h.typeText("/profile cloud ")
	h.run(h.press("tab"))
	if !strings.Contains(h.m.statusLine(), "no model list for cloud") {
		t.Errorf("unknown profile not reported: %q", h.m.statusLine())
	}
}
