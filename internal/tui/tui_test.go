package tui

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// fakeEngine is the core as the shell sees it: sessions whose turns run
// a script. The script writes events to out and returns; the fake sends
// turn_start before it and closes the channel after it.
type fakeEngine struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	script   func(ctx context.Context, text string, ask permission.Prompter, out chan<- agentapi.Event)
	infos    []rpc.Info
}

type fakeSession struct {
	id   string
	eng  *fakeEngine
	msgs []agentapi.Message
}

func (e *fakeEngine) List(context.Context) ([]rpc.Info, error) { return e.infos, nil }

func (e *fakeEngine) Open(context.Context, rpc.OpenOptions) (rpc.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := &fakeSession{id: fmt.Sprintf("s%d", len(e.sessions)+1), eng: e}
	e.sessions[s.id] = s
	return s, nil
}

func (e *fakeEngine) Attach(_ context.Context, id string) (rpc.Session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s := e.sessions[id]; s != nil {
		return s, nil
	}
	return nil, errors.New("no such session")
}

func (s *fakeSession) ID() string                   { return s.id }
func (s *fakeSession) Created() time.Time           { return time.Unix(0, 0) }
func (s *fakeSession) Messages() []agentapi.Message { return s.msgs }
func (s *fakeSession) Warnings() []string           { return nil }
func (s *fakeSession) Close() error                 { return nil }

func (s *fakeSession) RunTurn(ctx context.Context, text string, ask permission.Prompter) (<-chan agentapi.Event, error) {
	s.eng.mu.Lock()
	s.msgs = append(s.msgs, agentapi.UserMessage(text))
	s.eng.mu.Unlock()
	out := make(chan agentapi.Event)
	go func() {
		defer close(out)
		out <- agentapi.Event{Kind: agentapi.EventTurnStart}
		s.eng.script(ctx, text, ask, out)
	}()
	return out, nil
}

// echoScript answers "**hi** <text>" in two deltas.
func echoScript(_ context.Context, text string, _ permission.Prompter, out chan<- agentapi.Event) {
	out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: "**hi** "}
	out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: text}
	out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop}
}

// chanSender is the program's Send for a model driven by hand.
type chanSender chan tea.Msg

func (c chanSender) Send(msg tea.Msg) { c <- msg }

// harness is a model over a served fake engine, with the channel its
// turns report on.
type harness struct {
	t   *testing.T
	eng *fakeEngine
	cl  *rpc.Client
	ctx context.Context
	m   model
	ch  chanSender
}

func newHarness(t *testing.T, eng *fakeEngine, opts Options) *harness {
	t.Helper()
	if eng.sessions == nil {
		eng.sessions = map[string]*fakeSession{}
	}
	if eng.script == nil {
		eng.script = echoScript
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	shellConn, core := net.Pipe()
	srv := &rpc.Server{Engine: eng, Version: "test"}
	go func() { _ = srv.ServeConn(ctx, core) }()
	cl, err := rpc.Dial(ctx, shellConn, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close(); srv.Close() })
	opts.Client = cl
	opts.ServerVersion = "test"
	if opts.SessionID == "" && !opts.PickSession {
		view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opts.SessionID, opts.Recorded = view.ID, true
	}
	ch := make(chanSender, 64)
	h := &harness{t: t, eng: eng, cl: cl, ctx: ctx, ch: ch}
	h.m = newModel(ctx, opts, true, ch)
	h.update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return h
}

func (h *harness) update(msg tea.Msg) tea.Cmd {
	next, cmd := h.m.Update(msg)
	h.m = next.(model)
	return cmd
}

// run executes a command synchronously and feeds its message back, the
// way the program would.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if msg := cmd(); msg != nil {
		h.update(msg)
	}
}

func keyMsg(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	}
	r := []rune(s)
	return tea.KeyPressMsg{Code: r[0], Text: s}
}

func (h *harness) press(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.update(keyMsg(k))
	}
	return cmd
}

func (h *harness) typeText(s string) {
	for _, r := range s {
		h.update(keyMsg(string(r)))
	}
}

// pump feeds the turn's messages into the model until stop says so.
func (h *harness) pump(stop func(msg tea.Msg) bool) {
	h.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-h.ch:
			h.update(msg)
			if stop(msg) {
				return
			}
		case <-deadline:
			h.t.Fatalf("the turn did not reach the expected point; view:\n%s", h.view())
		}
	}
}

func untilDone(msg tea.Msg) bool  { _, ok := msg.(turnDoneMsg); return ok }
func untilAsked(msg tea.Msg) bool { _, ok := msg.(permissionMsg); return ok }

// view is the drawn screen without its styling, so that words a style
// change splits can still be found.
func (h *harness) view() string { return ansiSeq.ReplaceAllString(h.m.View().Content, "") }

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")

func TestStreamsAndRendersTheAnswer(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{})

	h.typeText("hello")
	h.press("enter")
	if !h.m.busy {
		t.Fatal("enter did not start a turn")
	}
	if v := h.view(); !strings.Contains(v, "you") || !strings.Contains(v, "hello") || !strings.Contains(v, "thinking") {
		t.Errorf("view after enter lacks the question or the spinner:\n%s", v)
	}
	h.pump(untilDone)
	v := h.view()
	if h.m.busy || !strings.Contains(v, "hi") || !strings.Contains(v, "hello") || strings.Contains(v, "**") {
		t.Errorf("answer not rendered as markdown:\n%s", v)
	}
	if !strings.Contains(v, "enter sends") {
		t.Errorf("status line did not return to the help:\n%s", v)
	}
	sess := eng.sessions[h.m.sessionID]
	if sess == nil || len(sess.msgs) != 1 || sess.msgs[0].Text() != "hello" {
		t.Errorf("the core did not get the question: %+v", sess)
	}
	if h.m.ta.Value() != "" {
		t.Errorf("input not cleared: %q", h.m.ta.Value())
	}
}

func TestPermissionDialog(t *testing.T) {
	var decision permission.Decision
	eng := &fakeEngine{script: func(ctx context.Context, _ string, ask permission.Prompter, out chan<- agentapi.Event) {
		req := agentapi.PermissionRequest{
			CallID: "c1", Tool: "edit_file", Class: agentapi.ClassWrite, Summary: "edit main.go", Path: "main.go",
			Detail: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old line\n+new line",
			Always: `edits under "."`,
		}
		out <- agentapi.Event{Kind: agentapi.EventPermission, Permission: &req}
		decision, _ = ask.Prompt(ctx, permission.Ask{Request: req})
		if decision != permission.Deny {
			out <- agentapi.Event{Kind: agentapi.EventToolStart, Call: &agentapi.ToolCall{ID: "c1", Name: "edit_file", Arguments: []byte(`{"path":"main.go"}`)}}
		}
		out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: "done"}
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop}
	}}
	h := newHarness(t, eng, Options{})

	h.typeText("fix it")
	h.press("enter")
	h.pump(untilAsked)
	if h.m.dialog == nil {
		t.Fatal("no dialog after the permission event")
	}
	v := h.view()
	for _, want := range []string{"permission: edit main.go", "main.go", "+new line", "-old line", "allow", "always"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q:\n%s", want, v)
		}
	}
	// Typing goes nowhere while the question is open; "a" answers it.
	h.press("x")
	if h.m.dialog == nil || h.m.ta.Value() != "" {
		t.Fatalf("a stray key reached the input or closed the dialog")
	}
	h.press("a")
	if h.m.dialog != nil {
		t.Fatal("the dialog stayed open after the answer")
	}
	h.pump(untilDone)
	if decision != permission.AllowAlways {
		t.Errorf("core saw %v, want AllowAlways", decision)
	}
	if v := h.view(); !strings.Contains(v, "⚙ edit_file") || !strings.Contains(v, "done") {
		t.Errorf("tool activity or answer missing:\n%s", v)
	}
}

func TestCtrlCCancelsTheTurn(t *testing.T) {
	eng := &fakeEngine{script: func(ctx context.Context, _ string, _ permission.Prompter, out chan<- agentapi.Event) {
		<-ctx.Done()
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishCanceled}
	}}
	h := newHarness(t, eng, Options{})
	h.typeText("wait")
	h.press("enter")
	h.press("ctrl+c")
	if h.m.quitting {
		t.Fatal("ctrl+c during a turn must cancel it, not quit")
	}
	h.pump(untilDone)
	if h.m.busy || !strings.Contains(h.view(), "interrupted") {
		t.Errorf("turn not reported as interrupted:\n%s", h.view())
	}
	// Idle, ctrl+c leaves.
	h.press("ctrl+c")
	if !h.m.quitting {
		t.Error("ctrl+c at rest did not quit")
	}
}

func TestHistoryIsShown(t *testing.T) {
	history := []agentapi.Message{
		agentapi.UserMessage("earlier question"),
		{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{agentapi.Text("earlier answer")}, ToolCalls: []agentapi.ToolCall{{ID: "c", Name: "grep", Arguments: []byte(`{"pattern":"x"}`)}}},
	}
	h := newHarness(t, &fakeEngine{}, Options{History: history})
	v := h.view()
	for _, want := range []string{"earlier question", "earlier answer", "⚙ grep", "session s1", "core test"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestSessionPickerAttaches(t *testing.T) {
	eng := &fakeEngine{sessions: map[string]*fakeSession{}}
	h := newHarness(t, eng, Options{})
	// A second session with history, recorded earlier.
	old := &fakeSession{id: "old-session", eng: eng, msgs: []agentapi.Message{agentapi.UserMessage("from before")}}
	eng.sessions[old.id] = old
	eng.infos = []rpc.Info{{ID: old.id, Messages: 1, FirstPrompt: "from before"}, {ID: h.m.sessionID}}

	h.typeText("/sessions")
	cmd := h.press("enter")
	if h.m.picker == nil {
		t.Fatal("/sessions did not open the list")
	}
	h.run(cmd)
	if v := h.view(); !strings.Contains(v, "sessions") || !strings.Contains(v, "old-sess") || !strings.Contains(v, "from before") {
		t.Fatalf("list not shown:\n%s", v)
	}
	// Keys go to the list, not the input.
	h.press("x")
	if h.m.ta.Value() != "" {
		t.Fatal("a key reached the input while the list was open")
	}
	h.run(h.press("enter"))
	if h.m.picker != nil || h.m.sessionID != old.id {
		t.Fatalf("picker = %v, session = %q; want attached to %q", h.m.picker, h.m.sessionID, old.id)
	}
	if v := h.view(); !strings.Contains(v, "from before") {
		t.Errorf("history of the attached session not shown:\n%s", v)
	}

	// Esc in the list goes back to the current session.
	h.run(h.press("/", "s", "e", "s", "s", "i", "o", "n", "s", "enter"))
	h.press("esc")
	if h.m.picker != nil || h.m.sessionID != old.id {
		t.Error("esc did not return to the session")
	}
}

func TestStartsInThePickerWithoutASession(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{PickSession: true})
	if h.m.picker == nil || h.m.sessionID != "" {
		t.Fatal("PickSession did not open the list")
	}
	h.update(sessionsMsg{})
	if v := h.view(); !strings.Contains(v, "no recorded sessions") || !strings.Contains(v, "no session") {
		t.Errorf("empty list not explained:\n%s", v)
	}
	// Esc with nothing to pick opens a fresh session.
	h.run(h.press("esc"))
	if h.m.picker != nil || h.m.sessionID == "" || !h.m.recorded {
		t.Errorf("esc did not open a new session: picker=%v id=%q", h.m.picker, h.m.sessionID)
	}
}

func TestPasteGoesToTheInput(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.update(tea.PasteMsg{Content: "line one\nline two"})
	if got := h.m.ta.Value(); got != "line one\nline two" {
		t.Fatalf("input after paste = %q", got)
	}
	// Pasted newlines do not send; enter does, as one question.
	if h.m.busy {
		t.Fatal("a paste started a turn")
	}
	h.press("enter")
	h.pump(untilDone)
	sess := h.eng.sessions[h.m.sessionID]
	if len(sess.msgs) != 1 || sess.msgs[0].Text() != "line one\nline two" {
		t.Errorf("the core got %+v", sess.msgs)
	}

	// Nothing to paste into while a list is open.
	h.run(h.press("/", "s", "e", "s", "s", "i", "o", "n", "s", "enter"))
	h.update(tea.PasteMsg{Content: "stray"})
	if h.m.ta.Value() != "" {
		t.Errorf("a paste reached the input behind the list: %q", h.m.ta.Value())
	}
}

func TestResizeRedraws(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{History: []agentapi.Message{agentapi.UserMessage(strings.Repeat("word ", 40))}})
	before := h.m.vp.TotalLineCount()
	h.update(tea.WindowSizeMsg{Width: 40, Height: 20})
	if after := h.m.vp.TotalLineCount(); after <= before {
		t.Errorf("narrower window did not rewrap: %d lines before, %d after", before, after)
	}
	if h.m.vp.Height() != 20-headerLines-statusLines-inputLines-1 {
		t.Errorf("viewport height = %d", h.m.vp.Height())
	}
}
