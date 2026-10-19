package tui

import (
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// twoProfiles is the core's profile list in these tests: one a session
// can switch to, one that cannot be used.
func twoProfiles() []rpc.ProfileInfo {
	return []rpc.ProfileInfo{
		{Name: "local", Kind: "ollama", Model: "qwen", Default: true, Usable: true},
		{Name: "cloud", Kind: "openai", Model: "gpt", Usable: false, Problem: "selected profile \"cloud\" needs an API key"},
	}
}

func TestProfileCommandSwitchesAndCompletes(t *testing.T) {
	eng := &fakeEngine{profiles: twoProfiles()}
	h := newHarness(t, eng, Options{Profile: rpc.ProfileRef{Profile: "local", Model: "qwen"}})
	if v := h.view(); !strings.Contains(v, "local/qwen") {
		t.Fatalf("header does not name the profile:\n%s", v)
	}
	// Init asks the core for its profiles; Tab then offers them.
	h.run(h.m.Init())
	if len(h.m.profiles) != 2 {
		t.Fatalf("profiles after Init = %+v", h.m.profiles)
	}
	h.typeText("/profile ")
	h.press("tab")
	if st := h.m.statusLine(); !strings.Contains(st, "local") || !strings.Contains(st, "cloud") {
		t.Errorf("profile names not offered: %q", st)
	}
	h.m.ta.Reset()
	h.typeText("/profile c")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/profile cloud" {
		t.Errorf("/profile c + tab = %q", got)
	}

	// A name switches the session; the header and the transcript say so.
	h.m.ta.Reset()
	h.run(h.submit("/profile cloud"))
	if !strings.Contains(h.m.statusLine(), "needs an API key") || h.m.profile.Profile != "local" {
		t.Errorf("an unusable profile was not refused with its reason: %q, profile %+v", h.m.statusLine(), h.m.profile)
	}
	eng.profiles[1].Usable, eng.profiles[1].Problem = true, ""
	h.run(h.submit("/profile cloud"))
	if h.m.profile != (rpc.ProfileRef{Profile: "cloud", Model: "gpt"}) {
		t.Fatalf("profile after the switch = %+v", h.m.profile)
	}
	if v := h.view(); !strings.Contains(v, "cloud/gpt") || !strings.Contains(v, "answering with cloud/gpt") {
		t.Errorf("switch not shown:\n%s", v)
	}
	if got := eng.sessions[h.m.sessionID].Profile(); got.Profile != "cloud" {
		t.Errorf("the core's session answers as %+v", got)
	}
	// A model after the name overrides the profile's.
	h.run(h.submit("/profile local tiny"))
	if h.m.profile != (rpc.ProfileRef{Profile: "local", Model: "tiny"}) {
		t.Errorf("profile with a model = %+v", h.m.profile)
	}

	// Alone, /profile opens the list with the profile in use marked.
	h.run(h.submit("/profile"))
	if h.m.ppicker == nil {
		t.Fatal("/profile did not open the list")
	}
	if v := h.view(); !strings.Contains(v, "profiles") || !strings.Contains(v, "in use") || !strings.Contains(v, "default") {
		t.Fatalf("list not shown:\n%s", v)
	}
	h.press("x")
	if h.m.ta.Value() != "" {
		t.Error("a key reached the input while the list was open")
	}
	h.press("down")
	h.run(h.press("enter"))
	if h.m.ppicker != nil || h.m.profile.Profile != "cloud" {
		t.Errorf("enter did not switch to the row under the cursor: picker %v, profile %+v", h.m.ppicker, h.m.profile)
	}
	h.run(h.submit("/profile"))
	h.press("esc")
	if h.m.ppicker != nil {
		t.Error("esc did not close the list")
	}
}

// history is n user turns with an answer each, as the core's session
// holds them.
func history(n int) []agentapi.Message {
	var msgs []agentapi.Message
	for i := 1; i <= n; i++ {
		msgs = append(msgs, agentapi.UserMessage("question "+itoa(i)), agentapi.AssistantMessage("answer "+itoa(i)))
	}
	return msgs
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestForkFromTheBrowser(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{History: history(3)})
	src := eng.sessions[h.m.sessionID]
	src.msgs = history(3)
	src.profile = rpc.ProfileRef{Profile: "local", Model: "qwen"}

	// esc selects the newest block, g the first: turn 1. f forks there.
	h.press("esc", "g")
	if h.m.entries[h.m.sel].turn != 1 {
		t.Fatalf("selected turn %d", h.m.entries[h.m.sel].turn)
	}
	h.run(h.press("f"))
	if h.m.sessionID != src.id+"-fork" || h.m.sel != noSelection {
		t.Fatalf("after f: session %q, sel %d", h.m.sessionID, h.m.sel)
	}
	if h.m.turns != 1 {
		t.Errorf("the fork shows %d turns, want 1", h.m.turns)
	}
	v := h.view()
	for _, want := range []string{"question 1", "answer 1", "forked from session " + src.id + " after turn 1", "local/qwen"} {
		if !strings.Contains(v, want) {
			t.Errorf("fork view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "question 2") {
		t.Errorf("the fork shows a turn past the cut:\n%s", v)
	}
	// The original is untouched, the fork grows on its own.
	h.submit("question 4")
	h.pump(until[turnDoneMsg])
	if len(src.Messages()) != 6 || len(eng.sessions[h.m.sessionID].Messages()) != 3 {
		t.Errorf("source has %d messages, fork %d", len(src.Messages()), len(eng.sessions[h.m.sessionID].Messages()))
	}
}

func TestSessionTreeAndForkFromTheList(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{})
	old := &fakeSession{id: "old-session", eng: eng, msgs: history(2)}
	eng.add(old)
	// The list: the current session, an older one, and a fork of the
	// older one that the tree puts under it.
	eng.infos = []rpc.Info{
		{ID: h.m.sessionID},
		{ID: "old-fork-1", Messages: 2, FirstPrompt: "question 1", Parent: &rpc.Parent{ID: old.id, Turn: 1}},
		{ID: old.id, Messages: 4, FirstPrompt: "question 1"},
		{ID: "orphan", Messages: 1, FirstPrompt: "lost", Parent: &rpc.Parent{ID: "gone", Turn: 3}},
	}
	h.run(h.submit("/sessions"))
	rows := h.m.picker.rows
	if len(rows) != 4 || rows[1].info.ID != old.id || rows[2].info.ID != "old-fork-1" || rows[2].depth != 1 || rows[3].depth != 0 {
		t.Fatalf("tree = %+v", rows)
	}
	if v := h.view(); !strings.Contains(v, "└ old-fork") || !strings.Contains(v, "↳ old-sess@1") || !strings.Contains(v, "↳ gone@3") {
		t.Errorf("tree not drawn:\n%s", v)
	}

	// f on the older session forks the whole of it and attaches.
	h.press("down")
	h.run(h.press("f"))
	if h.m.picker != nil || h.m.sessionID != old.id+"-fork" || h.m.turns != 2 {
		t.Fatalf("after f: picker %v, session %q, turns %d", h.m.picker, h.m.sessionID, h.m.turns)
	}
	if v := h.view(); !strings.Contains(v, "forked from session old-sess after turn 2") {
		t.Errorf("fork not announced:\n%s", v)
	}
}

func TestSetupAppliesTheSavedProfile(t *testing.T) {
	eng := &fakeEngine{profiles: []rpc.ProfileInfo{{Name: "api-bar", Kind: "openai", Model: "m1", Usable: true}}}
	setup := &Setup{Describe: "d", Profile: "api-bar", Values: map[string]string{"model": "m1"}, Save: func(string, map[string]string) (string, error) {
		return "saved to /home/u/.config/hint/config.yaml", nil
	}}
	h := newHarness(t, eng, Options{Setup: setup})
	h.submit("/setup")
	// The save's answer carries the switch and the list refresh as the
	// next command; the harness runs it by hand.
	save := h.press("ctrl+s")
	h.run(h.update(save()))
	if h.m.setupForm != nil {
		t.Fatalf("form still open: %q", h.m.setupForm.err)
	}
	if h.m.profile != (rpc.ProfileRef{Profile: "api-bar", Model: "m1"}) {
		t.Errorf("the saved profile is not in use: %+v", h.m.profile)
	}
	if got := eng.sessions[h.m.sessionID].Profile(); got.Profile != "api-bar" {
		t.Errorf("the core's session answers as %+v", got)
	}
	if v := h.view(); !strings.Contains(v, "saved to") || !strings.Contains(v, "answering with api-bar/m1") {
		t.Errorf("save and switch not both shown:\n%s", v)
	}
	if len(h.m.profiles) != 1 {
		t.Errorf("the profile list was not refreshed: %+v", h.m.profiles)
	}
}
