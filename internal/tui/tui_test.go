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

// testVersion is the core version the harness serves; the header shows it.
const testVersion = "test"

// The terminal every model starts at. Wide enough that no assertion
// depends on wrapping; TestResizeRedraws shrinks it on purpose.
const (
	termWidth  = 80
	termHeight = 24
)

// turnTimeout bounds how long pump waits for a turn to reach the message
// a test expects.
const turnTimeout = 5 * time.Second

// script is what one turn of a fakeSession does: it writes events to out
// and returns. The session sends turn_start before it and closes out
// after it, so a script ends with the turn_end it wants reported.
type script func(ctx context.Context, text string, ask permission.Prompter, out chan<- agentapi.Event)

// fakeEngine is the core as the shell sees it: sessions whose turns run a
// script. The zero value is usable through newHarness, which fills in the
// session map and echoScript. infos is what List reports; a test sets it
// before opening the picker. mu guards sessions and every session's msgs.
type fakeEngine struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
	script   script
	infos    []rpc.Info
}

// fakeSession is one session of a fakeEngine: its id and the questions
// its turns were given, in order.
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

// add records a session the engine did not open itself, the way a
// session recorded by an earlier run would already be on disk.
func (e *fakeEngine) add(s *fakeSession) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sessions[s.id] = s
}

func (s *fakeSession) ID() string         { return s.id }
func (s *fakeSession) Created() time.Time { return time.Unix(0, 0) }
func (s *fakeSession) Warnings() []string { return nil }
func (s *fakeSession) Close() error       { return nil }

// Messages is the session's history: the questions RunTurn recorded.
func (s *fakeSession) Messages() []agentapi.Message {
	s.eng.mu.Lock()
	defer s.eng.mu.Unlock()
	return s.msgs
}

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

// echoScript answers "**hi** <text>" in two deltas: enough to see
// streaming and the markdown rendering of the finished answer.
func echoScript(_ context.Context, text string, _ permission.Prompter, out chan<- agentapi.Event) {
	out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: "**hi** "}
	out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: text}
	out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop}
}

// chanSender is the program's Send for a model driven by hand: the turn
// goroutine writes here and pump reads. The buffer must hold whatever a
// turn produces before the test starts pumping.
type chanSender chan tea.Msg

func (c chanSender) Send(msg tea.Msg) { c <- msg }

// harness is a model over a fake engine served on an in-memory pipe, and
// the channel its turns report on. Update is driven by hand; a command
// a test cares about is executed through run.
type harness struct {
	t   *testing.T
	eng *fakeEngine
	m   model
	ch  chanSender
}

// newHarness serves eng on a pipe, dials it and builds a model over the
// client at termWidth×termHeight. Nil fields of eng get their defaults:
// an empty session map and echoScript. Unless opts names a session or
// asks for the picker, a recorded session is opened first, as cmd/hint
// does before it starts the shell.
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
	srv := &rpc.Server{Engine: eng, Version: testVersion}
	go func() { _ = srv.ServeConn(ctx, core) }()
	cl, err := rpc.Dial(ctx, shellConn, "")
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	// Cleanups run last-in first-out: the connection closes before the
	// context that serves it is cancelled.
	t.Cleanup(func() { _ = cl.Close(); srv.Close() })
	opts.Client = cl
	opts.ServerVersion = testVersion
	if opts.SessionID == "" && !opts.PickSession {
		view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
		if err != nil {
			t.Fatalf("OpenSession: %v", err)
		}
		opts.SessionID, opts.Recorded = view.ID, true
	}
	ch := make(chanSender, 64)
	h := &harness{t: t, eng: eng, ch: ch}
	h.m = newModel(ctx, opts, true, ch)
	h.update(tea.WindowSizeMsg{Width: termWidth, Height: termHeight})
	return h
}

// update feeds one message to the model and keeps the model it returns.
func (h *harness) update(msg tea.Msg) tea.Cmd {
	next, cmd := h.m.Update(msg)
	h.m = next.(model)
	return cmd
}

// run executes a command synchronously and feeds its message back, the
// way the program would; a batch is run command by command. Commands
// the resulting updates return are dropped: none of them matter here,
// and a cursor blink would sleep.
func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			h.run(c)
		}
	default:
		h.update(msg)
	}
}

// specialKeys are the keys without a character of their own that the
// tests press, under the names tea.KeyPressMsg.String gives them.
var specialKeys = map[string]tea.KeyPressMsg{
	"enter":       {Code: tea.KeyEnter},
	"esc":         {Code: tea.KeyEscape},
	"ctrl+c":      {Code: 'c', Mod: tea.ModCtrl},
	"ctrl+d":      {Code: 'd', Mod: tea.ModCtrl},
	"ctrl+y":      {Code: 'y', Mod: tea.ModCtrl},
	"tab":         {Code: tea.KeyTab},
	"ctrl+s":      {Code: 's', Mod: tea.ModCtrl},
	"backspace":   {Code: tea.KeyBackspace},
	"shift+enter": {Code: tea.KeyEnter, Mod: tea.ModShift},
	"alt+enter":   {Code: tea.KeyEnter, Mod: tea.ModAlt},
	"up":          {Code: tea.KeyUp},
	"down":        {Code: tea.KeyDown},
	"shift+up":    {Code: tea.KeyUp, Mod: tea.ModShift},
	"shift+down":  {Code: tea.KeyDown, Mod: tea.ModShift},
	"ctrl+up":     {Code: tea.KeyUp, Mod: tea.ModCtrl},
	"pgup":        {Code: tea.KeyPgUp},
	"pgdown":      {Code: tea.KeyPgDown},
	"ctrl+home":   {Code: tea.KeyHome, Mod: tea.ModCtrl},
	"ctrl+end":    {Code: tea.KeyEnd, Mod: tea.ModCtrl},
}

// keyMsg is the key press for a name: one of specialKeys, or a single
// character typed as text.
func keyMsg(name string) tea.KeyPressMsg {
	if k, ok := specialKeys[name]; ok {
		return k
	}
	r := []rune(name)
	if len(r) != 1 {
		panic(fmt.Sprintf("keyMsg: %q is neither a special key nor one character", name))
	}
	return tea.KeyPressMsg{Code: r[0], Text: name}
}

// press sends the named keys in order and returns the command of the
// last one.
func (h *harness) press(keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.update(keyMsg(k))
	}
	return cmd
}

// typeText presses each character of s, the way a user types it.
func (h *harness) typeText(s string) {
	for _, r := range s {
		h.update(keyMsg(string(r)))
	}
}

// submit types a line and presses enter, returning enter's command: the
// list request of /sessions, or the spinner tick of a question whose turn
// runs on its own goroutine.
func (h *harness) submit(line string) tea.Cmd {
	h.typeText(line)
	return h.press("enter")
}

// pump feeds the turn's messages into the model until stop says so, and
// fails the test when turnTimeout passes first.
func (h *harness) pump(stop func(msg tea.Msg) bool) {
	h.t.Helper()
	deadline := time.After(turnTimeout)
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

// until is pump's stop condition: the message is a T.
func until[T tea.Msg](msg tea.Msg) bool {
	_, ok := msg.(T)
	return ok
}

// view is the drawn screen without its styling, so that words a style
// change splits can still be found.
func (h *harness) view() string { return ansiSeq.ReplaceAllString(h.m.View().Content, "") }

// ansiSeq matches one CSI sequence, which is all lipgloss and glamour
// emit for what these tests draw.
var ansiSeq = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// TestStreamsAndRendersTheAnswer walks one question through the shell:
// enter starts a turn and shows the question with the spinner; the
// deltas stream in; the finished answer is rendered as markdown, the
// status line is back at the help, the input is empty and the core has
// the question on record.
func TestStreamsAndRendersTheAnswer(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{})

	h.submit("hello")
	if !h.m.busy {
		t.Fatal("enter did not start a turn")
	}
	v := h.view()
	for _, want := range []string{"you", "hello", "thinking"} {
		if !strings.Contains(v, want) {
			t.Errorf("view after enter lacks %q:\n%s", want, v)
		}
	}

	h.pump(until[turnDoneMsg])
	if h.m.busy {
		t.Error("still busy after the turn ended")
	}
	v = h.view()
	// "**hi**" is bold in markdown: the word stays, the markers go.
	if !strings.Contains(v, "hi") || !strings.Contains(v, "hello") || strings.Contains(v, "**") {
		t.Errorf("answer not rendered as markdown:\n%s", v)
	}
	if !strings.Contains(v, "enter sends") {
		t.Errorf("status line did not return to the help:\n%s", v)
	}
	sess := eng.sessions[h.m.sessionID]
	if sess == nil {
		t.Fatalf("no session %q in the core", h.m.sessionID)
	}
	if msgs := sess.Messages(); len(msgs) != 1 || msgs[0].Text() != "hello" {
		t.Errorf("the core got %+v, want the one question", msgs)
	}
	if got := h.m.ta.Value(); got != "" {
		t.Errorf("input not cleared: %q", got)
	}
}

// TestPermissionDialog pins the permission dialog: the event opens it
// with the summary, the path, the diff and the options; a key that is
// not an answer neither closes it nor reaches the input; "a" answers
// AllowAlways, the core sees that decision, and the turn goes on to its
// tool call and answer.
func TestPermissionDialog(t *testing.T) {
	req := agentapi.PermissionRequest{
		CallID:  "c1",
		Tool:    "edit_file",
		Class:   agentapi.ClassWrite,
		Summary: "edit main.go",
		Path:    "main.go",
		Detail:  "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old line\n+new line",
		Always:  `edits under "."`,
	}
	// decided carries the core's view of the answer back to the test.
	decided := make(chan permission.Decision, 1)
	eng := &fakeEngine{script: func(ctx context.Context, _ string, ask permission.Prompter, out chan<- agentapi.Event) {
		out <- agentapi.Event{Kind: agentapi.EventPermission, Permission: &req}
		decision, _ := ask.Prompt(ctx, permission.Ask{Request: req})
		decided <- decision
		if decision != permission.Deny {
			out <- agentapi.Event{
				Kind: agentapi.EventToolStart,
				Call: &agentapi.ToolCall{ID: "c1", Name: "edit_file", Arguments: []byte(`{"path":"main.go"}`)},
			}
		}
		out <- agentapi.Event{Kind: agentapi.EventTextDelta, Text: "done"}
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop}
	}}
	h := newHarness(t, eng, Options{})

	h.submit("fix it")
	h.pump(until[permissionMsg])
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
		t.Fatal("a stray key reached the input or closed the dialog")
	}
	h.press("a")
	if h.m.dialog != nil {
		t.Fatal("the dialog stayed open after the answer")
	}

	h.pump(until[turnDoneMsg])
	if got := <-decided; got != permission.AllowAlways {
		t.Errorf("core saw %v, want AllowAlways", got)
	}
	if v := h.view(); !strings.Contains(v, "⚙ edit_file") || !strings.Contains(v, "done") {
		t.Errorf("tool activity or answer missing:\n%s", v)
	}
}

// TestCtrlCCancelsTheTurn pins the two meanings of ctrl+c: during a turn
// it cancels the turn, which the transcript reports as interrupted; at
// rest it quits the shell.
func TestCtrlCCancelsTheTurn(t *testing.T) {
	// The turn hangs until it is cancelled, as a slow model would.
	eng := &fakeEngine{script: func(ctx context.Context, _ string, _ permission.Prompter, out chan<- agentapi.Event) {
		<-ctx.Done()
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishCanceled}
	}}
	h := newHarness(t, eng, Options{})

	h.submit("wait")
	h.press("ctrl+c")
	if h.m.quitting {
		t.Fatal("ctrl+c during a turn must cancel it, not quit")
	}
	h.pump(until[turnDoneMsg])
	if v := h.view(); h.m.busy || !strings.Contains(v, "interrupted") {
		t.Errorf("turn not reported as interrupted:\n%s", v)
	}

	// Idle, ctrl+c leaves.
	h.press("ctrl+c")
	if !h.m.quitting {
		t.Error("ctrl+c at rest did not quit")
	}
}

// TestHistoryIsShown pins the first screen of a continued session: the
// recorded questions, answers and tool calls are in the transcript, and
// the header names the session and the core's version.
func TestHistoryIsShown(t *testing.T) {
	history := []agentapi.Message{
		agentapi.UserMessage("earlier question"),
		{
			Role:      agentapi.RoleAssistant,
			Content:   []agentapi.ContentPart{agentapi.Text("earlier answer")},
			ToolCalls: []agentapi.ToolCall{{ID: "c", Name: "grep", Arguments: []byte(`{"pattern":"x"}`)}},
		},
	}
	h := newHarness(t, &fakeEngine{}, Options{History: history})

	v := h.view()
	for _, want := range []string{"earlier question", "earlier answer", "⚙ grep", "session s1", "core " + testVersion} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

// TestSessionPickerAttaches walks /sessions: the list opens and fills
// from the server, keys go to the list and not the input, enter attaches
// the row under the cursor and shows its history, and esc from the list
// returns to the session the shell is on.
func TestSessionPickerAttaches(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{})
	// A second session with history, recorded earlier. The list shows
	// ids cut to eight characters.
	old := &fakeSession{id: "old-session", eng: eng, msgs: []agentapi.Message{agentapi.UserMessage("from before")}}
	eng.add(old)
	eng.infos = []rpc.Info{{ID: old.id, Messages: 1, FirstPrompt: "from before"}, {ID: h.m.sessionID}}

	cmd := h.submit("/sessions")
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
	h.run(h.submit("/sessions"))
	h.press("esc")
	if h.m.picker != nil || h.m.sessionID != old.id {
		t.Error("esc did not return to the session")
	}
}

// TestStartsInThePickerWithoutASession pins hint -r: the shell starts in
// the list with no session, Init asks the server for the rows, an empty
// list says so, and esc then opens a fresh recorded session rather than
// leaving the user nowhere.
func TestStartsInThePickerWithoutASession(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{PickSession: true})
	if h.m.picker == nil || h.m.sessionID != "" {
		t.Fatal("PickSession did not open the list")
	}
	h.run(h.m.Init())
	if v := h.view(); !strings.Contains(v, "no recorded sessions") || !strings.Contains(v, "no session") {
		t.Errorf("empty list not explained:\n%s", v)
	}

	// Esc with nothing to pick opens a fresh session.
	h.run(h.press("esc"))
	if h.m.picker != nil || h.m.sessionID == "" || !h.m.recorded {
		t.Errorf("esc did not open a new session: picker=%v id=%q", h.m.picker, h.m.sessionID)
	}
}

// TestPasteGoesToTheInput pins bracketed paste: pasted text lands in the
// input with its newlines, is not sent until enter, and goes to the core
// as one question; while the list is open a paste is dropped.
func TestPasteGoesToTheInput(t *testing.T) {
	const pasted = "line one\nline two"
	h := newHarness(t, &fakeEngine{}, Options{})

	h.update(tea.PasteMsg{Content: pasted})
	if got := h.m.ta.Value(); got != pasted {
		t.Fatalf("input after paste = %q", got)
	}
	if h.m.busy {
		t.Fatal("a paste started a turn")
	}
	h.press("enter")
	h.pump(until[turnDoneMsg])
	if msgs := h.eng.sessions[h.m.sessionID].Messages(); len(msgs) != 1 || msgs[0].Text() != pasted {
		t.Errorf("the core got %+v, want the pasted text as one question", msgs)
	}

	// Nothing to paste into while a list is open.
	h.run(h.submit("/sessions"))
	h.update(tea.PasteMsg{Content: "stray"})
	if got := h.m.ta.Value(); got != "" {
		t.Errorf("a paste reached the input behind the list: %q", got)
	}
}

// TestResizeRedraws pins the layout on a window change: the transcript
// is rewrapped to the new width, the viewport takes the height the
// layout leaves it, and the whole screen still fits the terminal.
func TestResizeRedraws(t *testing.T) {
	const width, height = 40, 20
	long := agentapi.UserMessage(strings.Repeat("word ", 40))
	h := newHarness(t, &fakeEngine{}, Options{History: []agentapi.Message{long}})

	before := h.m.vp.TotalLineCount()
	h.update(tea.WindowSizeMsg{Width: width, Height: height})
	if after := h.m.vp.TotalLineCount(); after <= before {
		t.Errorf("narrower window did not rewrap: %d lines before, %d after", before, after)
	}
	if got, want := h.m.vp.Height(), h.m.bodyHeight(); got != want {
		t.Errorf("viewport height = %d, want %d", got, want)
	}
	if lines := strings.Count(h.view(), "\n") + 1; lines > height {
		t.Errorf("screen is %d lines for a %d-line terminal:\n%s", lines, height, h.view())
	}
}

// clip records what the shell copied instead of touching the system clipboard.
func clip(t *testing.T) *[]string {
	t.Helper()
	var got []string
	old := writeClipboard
	writeClipboard = func(s string) error { got = append(got, s); return nil }
	t.Cleanup(func() { writeClipboard = old })
	return &got
}

// runBatch executes a command and the commands inside a batch.
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			runBatch(c)
		}
	}
}

func TestTabCompletesCommands(t *testing.T) {
	// A transcript to copy from, so that /copy is a command that exists.
	h := newHarness(t, &fakeEngine{}, Options{History: []agentapi.Message{agentapi.UserMessage("q")}})

	// A unique prefix completes to the command.
	h.typeText("/se")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/sessions" {
		t.Fatalf("/se + tab = %q, want /sessions", got)
	}

	// Several candidates: the common prefix, the list in the status line, then a cycle.
	h.m.ta.Reset()
	h.typeText("/")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/" {
		t.Fatalf("/ + tab = %q; the common prefix of every command is just /", got)
	}
	if st := h.m.statusLine(); !strings.Contains(st, "/copy") || !strings.Contains(st, "/sessions") {
		t.Errorf("candidates not listed: %q", st)
	}
	first := ""
	for i := 0; i < len(h.m.available()); i++ {
		h.press("tab")
		if i == 0 {
			first = h.m.ta.Value()
		}
	}
	if h.m.ta.Value() != first && !strings.HasPrefix(h.m.ta.Value(), "/") {
		t.Errorf("cycle left %q", h.m.ta.Value())
	}

	// Arguments are the command's closed set.
	h.m.ta.Reset()
	h.typeText("/copy ")
	h.press("tab")
	if st := h.m.statusLine(); !strings.Contains(st, "/copy answer") || !strings.Contains(st, "/copy code") {
		t.Errorf("arguments not offered: %q", st)
	}
	h.m.ta.Reset()
	h.typeText("/copy co")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/copy code" {
		t.Errorf("/copy co + tab = %q", got)
	}

	// A question has no deterministic completion: tab inserts nothing.
	h.m.ta.Reset()
	h.typeText("what")
	h.press("tab")
	if got := h.m.ta.Value(); got != "what" {
		t.Errorf("tab changed a question: %q", got)
	}
}

func TestTabOffersOnlyWhatIsAvailable(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	// Nothing in the transcript yet: /copy has nothing to copy.
	for _, c := range h.m.complete("/") {
		if c == "/copy" {
			t.Fatalf("/copy offered with an empty transcript: %v", h.m.complete("/"))
		}
	}
	h.m.busy = true
	for _, c := range h.m.complete("/") {
		if c == "/sessions" {
			t.Fatalf("/sessions offered during a turn: %v", h.m.complete("/"))
		}
	}
}

func TestCopyAnswerQuestionCodeAll(t *testing.T) {
	got := clip(t)
	history := []agentapi.Message{
		agentapi.UserMessage("show me"),
		agentapi.AssistantMessage("Here:\n```go\nfunc a() {}\n```\nand\n```sh\ngo test\n```\n"),
	}
	h := newHarness(t, &fakeEngine{}, Options{History: history})

	h.press("ctrl+y")
	cmd := h.press("ctrl+y")
	runBatch(cmd)
	if len(*got) == 0 || !strings.Contains((*got)[0], "Here:") {
		t.Fatalf("ctrl+y copied %q", *got)
	}
	if !strings.Contains(h.m.statusLine(), "copied answer") {
		t.Errorf("no confirmation: %q", h.m.statusLine())
	}

	for _, c := range []struct{ line, want string }{
		{"/copy question", "show me"},
		{"/copy code", "go test"},
		{"/copy all", "you:\nshow me"},
	} {
		*got = nil
		h.typeText(c.line)
		cmd := h.press("enter")
		runBatch(cmd)
		if len(*got) == 0 || !strings.Contains((*got)[0], c.want) {
			t.Errorf("%s copied %q, want it to contain %q", c.line, *got, c.want)
		}
	}
	h.typeText("/copy bogus")
	h.press("enter")
	if !strings.Contains(h.m.statusLine(), "nothing to copy") {
		t.Errorf("bad argument not reported: %q", h.m.statusLine())
	}
}

func TestMouseModeCopiesTheBlockUnderTheClick(t *testing.T) {
	got := clip(t)
	history := []agentapi.Message{agentapi.UserMessage("first question"), agentapi.AssistantMessage("first answer")}
	h := newHarness(t, &fakeEngine{}, Options{History: history})

	if h.m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("mouse reporting is on by default; the terminal could not select text")
	}
	h.typeText("/mouse")
	h.press("enter")
	if !h.m.mouse || h.m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("/mouse did not turn reporting on")
	}
	// Row 0 is the header; the question is drawn from row 1 (label), 2 (text).
	cmd := h.update(tea.MouseClickMsg{X: 3, Y: headerLines + 1, Button: tea.MouseLeft})
	runBatch(cmd)
	if len(*got) != 1 || (*got)[0] != "first question" {
		t.Errorf("click on the question copied %q", *got)
	}
	// A click in the blank gap or on the rule copies nothing.
	*got = nil
	runBatch(h.update(tea.MouseClickMsg{X: 3, Y: 0, Button: tea.MouseLeft}))
	if len(*got) != 0 {
		t.Errorf("click on the header copied %q", *got)
	}
	h.typeText("/mouse")
	h.press("enter")
	if h.m.mouse {
		t.Error("/mouse again did not turn it off")
	}
}

func TestFailedTurnExplainsAuth(t *testing.T) {
	eng := &fakeEngine{script: func(_ context.Context, _ string, _ permission.Prompter, out chan<- agentapi.Event) {
		out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.NewError(agentapi.ErrAuth, "Unauthorized")}
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
	}}
	h := newHarness(t, eng, Options{})
	h.submit("hi")
	h.pump(until[turnDoneMsg])
	if v := h.view(); !strings.Contains(v, "Unauthorized") || !strings.Contains(v, "api_key") {
		t.Errorf("a rejected key is not explained:\n%s", v)
	}
}

func TestSetupFormSavesWhatWasTypedAndMasksTheKey(t *testing.T) {
	var savedName string
	var saved map[string]string
	setup := &Setup{
		Describe: "config in use: /home/u/.config/hint/config.yaml",
		Profile:  "api-bar",
		Values:   map[string]string{"kind": "openai", "base_url": "https://gw.test", "model": "m1"},
		Save: func(profile string, values map[string]string) (string, error) {
			savedName, saved = profile, values
			return "saved to /home/u/.config/hint/config.yaml", nil
		},
	}
	eng := &fakeEngine{script: func(_ context.Context, _ string, _ permission.Prompter, out chan<- agentapi.Event) {
		out <- agentapi.Event{Kind: agentapi.EventError, Err: agentapi.NewError(agentapi.ErrAuth, "Unauthorized")}
		out <- agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishError}
	}}
	h := newHarness(t, eng, Options{Setup: setup})

	// The failed turn says where the configuration is and how to fix it.
	h.submit("hi")
	h.pump(until[turnDoneMsg])
	if v := h.view(); !strings.Contains(v, "/home/u/.config/hint/config.yaml") || !strings.Contains(v, "/setup") {
		t.Fatalf("rejected key not explained with a way out:\n%s", v)
	}

	h.submit("/setup")
	if h.m.setupForm == nil {
		t.Fatal("/setup did not open the form")
	}
	// Every profile field of the schema is in the form, empty ones with
	// what leaving them empty means.
	v := h.view()
	for _, want := range []string{"gw.test", "m1", "kind", "api key", "context window", "header timeout", "default 60s"} {
		if !strings.Contains(v, want) {
			t.Errorf("form lacks %q:\n%s", want, v)
		}
	}
	// profile, kind, base url → the key: typed, masked.
	h.press("enter", "enter", "enter")
	h.typeText("sk-secret-123")
	if v := h.view(); strings.Contains(v, "sk-secret-123") {
		t.Errorf("the key is shown on screen:\n%s", v)
	}
	// model → context window: a bad number is refused, on its field.
	h.press("enter", "enter")
	h.typeText("lots")
	h.press("ctrl+s")
	if h.m.setupForm == nil || !strings.Contains(h.view(), "whole number") || h.m.setupForm.fields[h.m.setupForm.focus].label != "context window" {
		t.Fatalf("a bad context window was not refused on its field:\n%s", h.view())
	}
	for range "lots" {
		h.press("backspace")
	}
	h.typeText("128000")
	h.run(h.press("ctrl+s"))
	if h.m.setupForm != nil {
		t.Fatalf("form still open: %q", h.m.setupForm.err)
	}
	want := map[string]string{"kind": "openai", "base_url": "https://gw.test", "api_key": "sk-secret-123", "model": "m1", "context_window": "128000"}
	if savedName != "api-bar" || fmt.Sprint(saved) != fmt.Sprint(want) {
		t.Errorf("Save got %q %v, want api-bar %v", savedName, saved, want)
	}
	if v := h.view(); !strings.Contains(v, "saved to /home/u") || strings.Contains(v, "sk-secret-123") {
		t.Errorf("confirmation missing or leaking:\n%s", v)
	}
}

func TestSetupFormShowsAFailureAndEscCancels(t *testing.T) {
	setup := &Setup{Describe: "d", Profile: "p", Save: func(string, map[string]string) (string, error) {
		return "", errors.New("permission denied")
	}}
	h := newHarness(t, &fakeEngine{}, Options{Setup: setup})
	h.submit("/setup")
	h.run(h.press("ctrl+s"))
	if h.m.setupForm == nil || !strings.Contains(h.view(), "permission denied") {
		t.Fatalf("a failed save closed the form or hid the reason:\n%s", h.view())
	}
	h.press("esc")
	if h.m.setupForm != nil {
		t.Error("esc did not close the form")
	}
	// A core in another process offers neither the form nor /config.
	remote := newHarness(t, &fakeEngine{}, Options{Setup: &Setup{Describe: "elsewhere"}})
	for _, c := range remote.m.complete("/") {
		if c == "/setup" || c == "/config" {
			t.Errorf("%s offered without a way to save: %v", c, remote.m.complete("/"))
		}
	}
}

func TestConfigCommand(t *testing.T) {
	type call struct {
		key, value string
		project    bool
	}
	var sets []call
	var unsets []call
	setup := &Setup{
		Describe: "d", Profile: "api-bar",
		Save: func(string, map[string]string) (string, error) { return "", nil },
		Set: func(key, value string, project bool) (string, error) {
			sets = append(sets, call{key, value, project})
			return key + " set", nil
		},
		Unset: func(key string, project bool) (string, error) {
			unsets = append(unsets, call{key: key, project: project})
			return key + " removed", nil
		},
		Show: func() (string, error) { return "overview.depth = 2", nil },
	}
	h := newHarness(t, &fakeEngine{}, Options{Setup: setup})

	// Tab walks the grammar: subcommand, --project or a key, a value.
	h.typeText("/con")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/config " {
		t.Fatalf("/con + tab = %q", got)
	}
	h.typeText("se")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/config set " {
		t.Fatalf("/config se + tab = %q", got)
	}
	h.typeText("overview.d")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/config set overview.depth " {
		t.Fatalf("key completion = %q", got)
	}
	h.m.ta.Reset()
	h.typeText("/config set providers.api-bar.k")
	h.press("tab")
	if got := h.m.ta.Value(); got != "/config set providers.api-bar.kind " {
		t.Fatalf("profile field completion = %q", got)
	}
	h.press("tab")
	if st := h.m.statusLine(); !strings.Contains(st, "ollama") || !strings.Contains(st, "openai") {
		t.Errorf("enum values not offered: %q", st)
	}
	h.m.ta.Reset()
	for _, c := range h.m.complete("/config set ") {
		if strings.HasSuffix(c, ".api_key") {
			t.Errorf("the key is offered for set: %v", h.m.complete("/config set "))
		}
	}

	h.m.ta.Reset()
	h.submit("/config set --project overview.depth 3")
	h.submit("/config unset instructions.budget")
	if len(sets) != 1 || sets[0] != (call{"overview.depth", "3", true}) || len(unsets) != 1 || unsets[0].key != "instructions.budget" || unsets[0].project {
		t.Errorf("sets %v unsets %v", sets, unsets)
	}
	h.submit("/config set providers.api-bar.api_key sk-x")
	if len(sets) != 1 || !strings.Contains(h.m.statusLine(), "/setup") {
		t.Errorf("a key was accepted on the command line: %v, %q", sets, h.m.statusLine())
	}
	h.submit("/config")
	if !strings.Contains(h.view(), "overview.depth = 2") {
		t.Errorf("/config did not show the configuration:\n%s", h.view())
	}
	h.submit("/config keys")
	if !strings.Contains(h.view(), "providers.<profile>.header_timeout") {
		t.Errorf("/config keys did not list the keys:\n%s", h.view())
	}
}

func TestNewlineKeys(t *testing.T) {
	eng := &fakeEngine{}
	h := newHarness(t, eng, Options{})
	h.typeText("one")
	h.press("shift+enter")
	h.typeText("two")
	h.press("alt+enter")
	h.typeText("three\\")
	h.press("enter") // a trailing backslash: a newline, not a send
	h.typeText("four")
	if got := h.m.ta.Value(); got != "one\ntwo\nthree\nfour" {
		t.Fatalf("input = %q", got)
	}
	if h.m.busy {
		t.Fatal("a newline key sent the question")
	}
	h.press("enter")
	h.pump(until[turnDoneMsg])
	if msgs := eng.sessions[h.m.sessionID].Messages(); len(msgs) == 0 || msgs[0].Text() != "one\ntwo\nthree\nfour" {
		t.Errorf("the core got %+v", msgs)
	}
}

// longHistory is n turns of a question and a one-line answer.
func longHistory(n int) []agentapi.Message {
	var msgs []agentapi.Message
	for i := 1; i <= n; i++ {
		msgs = append(msgs, agentapi.UserMessage(fmt.Sprintf("question %d", i)), agentapi.AssistantMessage(fmt.Sprintf("answer %d", i)))
	}
	return msgs
}

func TestScrollKeys(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{History: longHistory(20)})
	if !h.m.vp.AtBottom() || strings.Contains(h.view(), "more lines") {
		t.Fatalf("a continued session does not open at its end:\n%s", h.view())
	}
	bottom := h.m.vp.YOffset()

	// ↑ on the input's only line scrolls the transcript, and the header
	// says how far from the end the view is.
	h.press("up", "up", "up")
	if got := h.m.vp.YOffset(); got != bottom-3 {
		t.Fatalf("three ↑ moved the view to %d, want %d", got, bottom-3)
	}
	if !strings.Contains(h.view(), "↓ 3 more lines") {
		t.Errorf("no word on the lines below:\n%s", h.view())
	}
	h.press("down")
	if got := h.m.vp.YOffset(); got != bottom-2 {
		t.Errorf("↓ moved the view to %d, want %d", got, bottom-2)
	}
	h.press("pgup")
	if got := h.m.vp.YOffset(); got != bottom-2-h.m.vp.Height() {
		t.Errorf("pgup moved the view to %d", got)
	}
	h.press("ctrl+home")
	if h.m.vp.YOffset() != 0 {
		t.Errorf("ctrl+home left the view at %d", h.m.vp.YOffset())
	}
	h.press("ctrl+end")
	if !h.m.vp.AtBottom() || strings.Contains(h.view(), "more lines") {
		t.Errorf("ctrl+end did not return to the end:\n%s", h.view())
	}

	// In a two-line question the arrows first move inside it: ↑ on its
	// second line is the input's, ↑ on its first the transcript's.
	h.typeText("first")
	h.press("shift+enter")
	h.typeText("second")
	h.press("up")
	if h.m.vp.YOffset() != bottom || h.m.ta.Line() != 0 {
		t.Fatalf("↑ on the second line scrolled (offset %d, line %d)", h.m.vp.YOffset(), h.m.ta.Line())
	}
	h.press("up")
	if h.m.vp.YOffset() != bottom-1 {
		t.Errorf("↑ on the first line did not scroll")
	}
	// shift+↑↓ scroll whatever line the cursor is on.
	h.press("shift+down")
	if h.m.vp.YOffset() != bottom {
		t.Errorf("shift+↓ did not scroll")
	}
	if h.m.ta.Value() != "first\nsecond" {
		t.Errorf("scrolling changed the question: %q", h.m.ta.Value())
	}
}

func TestMessageBrowser(t *testing.T) {
	got := clip(t)
	h := newHarness(t, &fakeEngine{}, Options{History: longHistory(12)})

	// esc in an empty input selects the newest block.
	h.press("esc")
	n := len(h.m.entries)
	if h.m.sel != n-1 {
		t.Fatalf("esc selected %d, want %d", h.m.sel, n-1)
	}
	if v := h.view(); !strings.Contains(v, "┃") || !strings.Contains(v, "turn 12") {
		t.Errorf("the selection is not marked or not described:\n%s", v)
	}
	if raw := h.m.View().Content; regexp.MustCompile(`\x1b\[(4[0-7]|48;)`).MatchString(raw) {
		t.Error("the selection paints a background")
	}
	if h.m.View().Cursor != nil {
		t.Error("the input's cursor is shown while a block is selected")
	}

	// ↑ and k move up a block; keys that are not the browser's do not
	// reach the input.
	h.press("up", "k", "x")
	if h.m.sel != n-3 || h.m.ta.Value() != "" {
		t.Fatalf("sel = %d, input %q", h.m.sel, h.m.ta.Value())
	}
	runBatch(h.press("y"))
	if len(*got) != 1 || (*got)[0] != "answer 11" {
		t.Errorf("y copied %q", *got)
	}

	// g goes to the first block and the view follows it.
	h.press("g")
	if h.m.sel != 0 || h.m.vp.YOffset() != 0 || !strings.Contains(h.view(), "question 1") {
		t.Errorf("g: sel %d, offset %d", h.m.sel, h.m.vp.YOffset())
	}
	h.press("f")
	if st := h.m.statusLine(); !strings.Contains(st, "turn 1") || !strings.Contains(st, "WP1.5") {
		t.Errorf("fork does not say what it needs: %q", st)
	}

	// esc gives the focus back to the input, at the end of the conversation.
	h.press("esc")
	if h.m.sel != noSelection || !h.m.vp.AtBottom() || strings.Contains(h.view(), "┃") {
		t.Errorf("esc did not leave the browser: sel %d, at bottom %v", h.m.sel, h.m.vp.AtBottom())
	}
	// With text in the input, esc keeps it and ctrl+↑ enters the browser.
	h.typeText("draft")
	h.press("esc")
	if h.m.sel != noSelection {
		t.Error("esc with a draft entered the browser")
	}
	h.press("ctrl+up")
	if h.m.sel != n-1 || h.m.ta.Value() != "draft" {
		t.Errorf("ctrl+↑: sel %d, draft %q", h.m.sel, h.m.ta.Value())
	}
}

func TestEntriesKnowTheirTurn(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{History: longHistory(2)})
	h.submit("third")
	h.pump(until[turnDoneMsg])
	var turns []int
	for _, e := range h.m.entries {
		turns = append(turns, e.turn)
	}
	if fmt.Sprint(turns) != "[1 1 2 2 3 3]" {
		t.Errorf("turns = %v", turns)
	}
}
