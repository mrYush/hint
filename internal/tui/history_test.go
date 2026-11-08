package tui

import "testing"

// TestInputHistoryRecall walks the lines sent this run with shift+↑/↓:
// commands and questions are kept, the walk stops at both ends, and the
// unsent draft comes back.
func TestInputHistoryRecall(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.submit("hello")
	h.pump(until[turnDoneMsg])
	h.submit("/help")

	h.typeText("draft")
	h.press("shift+up")
	if got := h.m.ta.Value(); got != "/help" {
		t.Fatalf("first shift+↑ = %q, want /help", got)
	}
	h.press("shift+up")
	if got := h.m.ta.Value(); got != "hello" {
		t.Fatalf("second shift+↑ = %q, want hello", got)
	}
	h.press("shift+up")
	if got := h.m.ta.Value(); got != "hello" {
		t.Errorf("walk past the oldest line changed it to %q", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "/help" {
		t.Fatalf("shift+↓ = %q, want /help", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "draft" {
		t.Fatalf("shift+↓ past the newest line = %q, want the draft", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "draft" {
		t.Errorf("shift+↓ below the draft changed it to %q", got)
	}
}

// TestInputHistoryEditingStartsANewWalk checks that a recalled line the
// user edits becomes the draft of the next walk instead of pinning the
// position in the old one.
func TestInputHistoryEditingStartsANewWalk(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.submit("/help")

	h.typeText("mine")
	h.press("shift+up")
	h.typeText("!")
	if got := h.m.ta.Value(); got != "/help!" {
		t.Fatalf("edited recall = %q, want /help!", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "/help!" {
		t.Errorf("shift+↓ after editing = %q, want the edited line", got)
	}
	h.press("shift+up")
	if got := h.m.ta.Value(); got != "/help" {
		t.Errorf("shift+↑ after editing = %q, want /help", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "/help!" {
		t.Errorf("shift+↓ = %q, want the edited draft back", got)
	}
}

// TestInputHistoryWithoutSentLines checks that shift+↑/↓ does nothing
// before the first send, and does not start a selection in the input.
func TestInputHistoryWithoutSentLines(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.typeText("typed")
	h.press("shift+up")
	if got := h.m.ta.Value(); got != "typed" {
		t.Errorf("shift+↑ without history = %q, want typed", got)
	}
	h.press("shift+down")
	if got := h.m.ta.Value(); got != "typed" {
		t.Errorf("shift+↓ without history = %q, want typed", got)
	}
}
