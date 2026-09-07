package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/debuglog"
	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/internal/project"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestToolRegistry_AllBehindPermissions replaces WP0.5's read-only pin: the
// binary now offers write_file, edit_file and bash, and it may only do so
// because assemble() builds the agent with a permission.Gate. The registry
// half is checked here; the gate half is a compile-time fact of assemble()
// plus TestRunMode below, since there is no way to build the agent in
// app.go without it.
func TestToolRegistry_AllBehindPermissions(t *testing.T) {
	root, err := tool.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reg, err := toolRegistry(root, nil)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"bash", "edit_file", "glob", "grep", "instructions", "list_dir", "read_file", "todo", "write_file"}
	if got := reg.Names(); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}
	classes := map[agentapi.ActionClass]int{}
	for _, tl := range reg.Tools() {
		classes[tl.Class()]++
	}
	if classes[agentapi.ClassWrite] != 2 || classes[agentapi.ClassExecute] != 1 {
		t.Fatalf("classes = %v, want 2 write + 1 execute", classes)
	}
}

func TestRunMode(t *testing.T) {
	cases := []struct {
		ask, autoEdit, yolo bool
		want                permission.Mode
	}{
		{false, false, false, permission.ModeAsk},
		{true, false, false, permission.ModeAsk},
		{false, true, false, permission.ModeAutoEdit},
		{false, false, true, permission.ModeYolo},
	}
	for _, c := range cases {
		if got := runMode(c.ask, c.autoEdit, c.yolo); got != c.want {
			t.Errorf("runMode(%v,%v,%v) = %s, want %s", c.ask, c.autoEdit, c.yolo, got, c.want)
		}
	}
}

func TestWarnMode(t *testing.T) {
	var out bytes.Buffer
	warnMode(&out, permission.ModeYolo, false)
	if !bytes.Contains(out.Bytes(), []byte("WARNING")) || !bytes.Contains(out.Bytes(), []byte("--yolo")) {
		t.Fatalf("yolo warning missing: %q", out.String())
	}

	out.Reset()
	warnMode(&out, permission.ModeAsk, false)
	if !bytes.Contains(out.Bytes(), []byte("not a terminal")) || !bytes.Contains(out.Bytes(), []byte("file edits and shell commands")) {
		t.Fatalf("non-tty notice missing: %q", out.String())
	}

	out.Reset()
	warnMode(&out, permission.ModeAutoEdit, false)
	if !bytes.Contains(out.Bytes(), []byte("shell commands will be denied")) || bytes.Contains(out.Bytes(), []byte("file edits")) {
		t.Fatalf("auto-edit notice wrong: %q", out.String())
	}

	out.Reset()
	warnMode(&out, permission.ModeAsk, true)
	if out.Len() != 0 {
		t.Fatalf("a terminal in ask mode needs no notice: %q", out.String())
	}
}

func TestSessionModeOf(t *testing.T) {
	cases := []struct {
		cont, resume, off bool
		id                string
		want              sessionMode
	}{
		{false, false, false, "", sessionNew},
		{true, false, false, "", sessionContinue},
		{false, true, false, "", sessionResume},
		{false, false, true, "", sessionOff},
		{false, false, false, "abc", sessionByID},
	}
	for _, c := range cases {
		if got := sessionModeOf(c.cont, c.resume, c.off, c.id); got != c.want {
			t.Errorf("sessionModeOf(%v,%v,%v,%q) = %d, want %d", c.cont, c.resume, c.off, c.id, got, c.want)
		}
	}
}

// TestDispatch walks the run modes: -p and plain arguments are one-shots
// (the latter with the migration note), both at once is refused, and
// nothing at all is the REPL — only with a terminal, and never with a
// JSON output that has no one answer to print.
func TestDispatch(t *testing.T) {
	// A regular file stands in for a non-terminal stdin (/dev/null would
	// not do: it is a character device, exactly like a tty).
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	tty, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer func() { _ = tty.Close() }()

	inv, err := dispatch("what?", nil, outputText, f)
	if err != nil || inv.interactive || inv.question != "what?" || inv.note != "" {
		t.Errorf("-p: %+v, %v", inv, err)
	}
	inv, err = dispatch("", []string{"add", "a flag"}, outputJSON, f)
	if err != nil || inv.interactive || inv.question != "add a flag" || !strings.Contains(inv.note, `hint -p "add a flag"`) {
		t.Errorf("positional: %+v, %v", inv, err)
	}
	if _, err := dispatch("q", []string{"q"}, outputText, f); err == nil {
		t.Error("-p and arguments together: want an error")
	}
	if _, err := dispatch("", nil, outputText, f); err == nil || !strings.Contains(err.Error(), "not a terminal") {
		t.Errorf("bare hint without a terminal: %v", err)
	}
	if _, err := dispatch("", nil, outputJSON, tty); err == nil || !strings.Contains(err.Error(), "--output json") {
		t.Errorf("bare hint with --output json: %v", err)
	}
	inv, err = dispatch("", nil, outputText, tty)
	if err != nil || !inv.interactive {
		t.Errorf("bare hint with a terminal: %+v, %v", inv, err)
	}
}

// TestOpenSession walks the modes against one store: a new session per
// run by default, -c continuing the latest (or starting one when there is
// none), -r reading the choice through the shared line reader, --session
// naming one, and --no-session recording nothing.
func TestOpenSession(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	cwd := t.TempDir()
	ctx := context.Background()
	noInput := console.NewLineReader(strings.NewReader(""))

	if sess, err := openSession(ctx, store, cwd, sessionOff, "", noInput, io.Discard); sess != nil || err != nil {
		t.Fatalf("sessionOff = %v, %v; want nil, nil", sess, err)
	}

	// --session in an empty directory is refused, not silently replaced.
	if _, err := openSession(ctx, store, cwd, sessionByID, "abc", noInput, io.Discard); err == nil || !strings.Contains(err.Error(), "no recorded sessions") {
		t.Fatalf("--session with nothing recorded: %v", err)
	}

	var stderr bytes.Buffer
	first, err := openSession(ctx, store, cwd, sessionContinue, "", noInput, &stderr)
	if err != nil {
		t.Fatalf("continue with nothing to continue: %v", err)
	}
	if !strings.Contains(stderr.String(), "starting a new one") {
		t.Errorf("no notice about the fallback:\n%s", stderr.String())
	}
	if err := first.AppendMessage(agentapi.UserMessage("first")); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := openSession(ctx, store, cwd, sessionNew, "", noInput, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID() == first.ID() {
		t.Fatal("sessionNew reused the existing session")
	}
	_ = second.Close()

	stderr.Reset()
	// The first session is older by creation but touched last: -c follows
	// modification time.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(first.Path(), later, later); err != nil {
		t.Fatal(err)
	}
	cont, err := openSession(ctx, store, cwd, sessionContinue, "", noInput, &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if cont.ID() != first.ID() || cont.Len() != 1 {
		t.Fatalf("continue = %s with %d messages, want %s with 1", cont.ID(), cont.Len(), first.ID())
	}
	if !strings.Contains(stderr.String(), "continuing session "+first.ID()) {
		t.Errorf("no notice:\n%s", stderr.String())
	}
	_ = cont.Close()

	// -r: the second listed entry is the older, untouched session.
	stderr.Reset()
	picked, err := openSession(ctx, store, cwd, sessionResume, "", console.NewLineReader(strings.NewReader("2\n")), &stderr)
	if err != nil {
		t.Fatal(err)
	}
	if picked.ID() != second.ID() {
		t.Fatalf("resume picked %s, want %s", picked.ID(), second.ID())
	}
	_ = picked.Close()

	// --session: the full id, or an unambiguous prefix of it.
	stderr.Reset()
	named, err := openSession(ctx, store, cwd, sessionByID, second.ID(), noInput, &stderr)
	if err != nil || named.ID() != second.ID() {
		t.Fatalf("--session %s: %v, %v", second.ID(), named, err)
	}
	_ = named.Close()
	if !strings.Contains(stderr.String(), "continuing session "+second.ID()) {
		t.Errorf("no notice:\n%s", stderr.String())
	}
	if _, err := openSession(ctx, store, cwd, sessionByID, "no-such-id", noInput, io.Discard); !errors.Is(err, session.ErrUnknownSession) {
		t.Errorf("--session with an unknown id: %v", err)
	}

	// -r with nobody answering is a refusal, not a silent new session.
	if _, err := openSession(ctx, store, cwd, sessionResume, "", noInput, io.Discard); !errors.Is(err, session.ErrNoChoice) {
		t.Fatalf("resume without an answer = %v, want ErrNoChoice", err)
	}

	// -r in a directory with no sessions starts a new one, like -c.
	fresh, err := openSession(ctx, store, t.TempDir(), sessionResume, "", noInput, io.Discard)
	if err != nil || fresh == nil {
		t.Fatalf("resume with nothing to resume = %v, %v", fresh, err)
	}
	_ = fresh.Close()
}

func TestSystemPrompt(t *testing.T) {
	pc := &project.Context{
		Dir:      "/w/app/sub",
		GitRoot:  "/w/app",
		Overview: "main.go\n(showing 2 levels; use list_dir or glob to see deeper)",
		Instructions: []project.Instruction{
			{Path: "/w/app/HINT.md", Content: "Answer in English.\n"},
			{Path: "/w/app/sub/AGENTS.md", Content: "Run go test.", Truncated: true},
		},
	}
	got := systemPrompt(pc)
	for _, want := range []string{
		"Working directory: /w/app/sub\n",
		"Git repository root: /w/app\n",
		"Contents of the working directory:\nmain.go\n",
		"<file path=\"/w/app/HINT.md\">\nAnswer in English.\n</file>",
		"Run go test.\n[... truncated at the instruction budget; the full file is /w/app/sub/AGENTS.md]",
		"nearest the working directory wins",
		"The instructions tool reads the project's instruction files",
		"A section that ends in [...] is shown as its heading and first sentence only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
	if i, j := strings.Index(got, "Contents of"), strings.Index(got, "<project_instructions>"); i > j {
		t.Error("instructions must come after the overview")
	}

	// Outside a repository, with nothing to show, the prompt says so and
	// carries no empty sections.
	got = systemPrompt(&project.Context{Dir: "/tmp/x"})
	if !strings.Contains(got, "Not inside a git repository") || strings.Contains(got, "Contents of") || strings.Contains(got, "<project_instructions>") {
		t.Errorf("bare prompt:\n%s", got)
	}
	if got = systemPrompt(&project.Context{Dir: "/w/app", GitRoot: "/w/app"}); !strings.Contains(got, "It is the root of a git repository") {
		t.Errorf("root prompt:\n%s", got)
	}
}

// fakeChat is a ChatProvider that answers every request with a fixed
// text, records the requests it saw, and can be told to fail or to hang
// until cancelled. It stands in for the provider stack so the CLI's own
// behaviour — output, history, session, REPL loop — is what gets tested.
type fakeChat struct {
	mu       sync.Mutex
	requests []agentapi.ChatRequest
	reply    func(n int) string
	fail     *agentapi.Error
	hang     bool
}

func (f *fakeChat) Name() string { return "fake" }

func (f *fakeChat) Stream(ctx context.Context, req agentapi.ChatRequest) (<-chan agentapi.ChatEvent, error) {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	n := len(f.requests)
	f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	out := make(chan agentapi.ChatEvent)
	go func() {
		defer close(out)
		if f.hang {
			<-ctx.Done()
			out <- agentapi.ChatEvent{Kind: agentapi.ChatError, Err: agentapi.WrapError(agentapi.ErrCanceled, ctx.Err(), "canceled")}
			return
		}
		for _, ev := range []agentapi.ChatEvent{
			{Kind: agentapi.ChatTextDelta, Text: f.reply(n)},
			{Kind: agentapi.ChatUsage, Usage: &agentapi.Usage{InputTokens: 10, OutputTokens: 3}},
			{Kind: agentapi.ChatDone, FinishReason: agentapi.FinishStop},
		} {
			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (f *fakeChat) seen() []agentapi.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agentapi.ChatRequest(nil), f.requests...)
}

// testAssembly builds an assembly over temp directories, a fake provider
// and buffers, in --no-session mode unless the caller changes it.
func testAssembly(t *testing.T, chat agentapi.ChatProvider, stdin string) (assembly, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, stderr bytes.Buffer
	cfg := &config.Config{
		Providers:       []config.Profile{{Name: "fake", Kind: config.KindOpenAI, APIKey: "sk-test-1234567890abcdef", Model: "m", ContextWindow: 4000}},
		DefaultProvider: "fake",
		Instructions:    config.InstructionSettings{Budget: config.DefaultInstructionBudget},
		Overview:        config.OverviewSettings{Depth: 1, MaxEntries: 10},
	}
	return assembly{
		chat:  chat,
		cfg:   cfg,
		opts:  options{mode: permission.ModeAsk, session: sessionOff, output: outputText},
		cwd:   t.TempDir(),
		store: session.NewStore(filepath.Join(t.TempDir(), "sessions")),
		io:    stdio{in: strings.NewReader(stdin), out: &out, err: &stderr, tty: true},
	}, &out, &stderr
}

func TestOneShotText(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "the answer" }}
	as, out, stderr := testAssembly(t, chat, "")
	as.opts.session = sessionNew
	a, err := assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.oneShot(context.Background(), "the question"); err != nil {
		t.Fatalf("oneShot: %v", err)
	}
	a.close()
	if out.String() != "the answer\n" {
		t.Errorf("stdout = %q", out.String())
	}
	if strings.Contains(stderr.String(), "not a terminal") {
		t.Errorf("stderr:\n%s", stderr.String())
	}
	// The request carried the preamble and the question, and the
	// session recorded both sides of the exchange.
	reqs := chat.seen()
	if len(reqs) != 1 || len(reqs[0].Messages) != 2 || reqs[0].Messages[1].Text() != "the question" {
		t.Fatalf("requests = %+v", reqs)
	}
	infos, err := as.store.List(as.cwd)
	if err != nil || len(infos) != 1 || infos[0].Messages != 2 {
		t.Errorf("sessions = %+v, %v; want one with 2 messages", infos, err)
	}
}

func TestOneShotJSON(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "42" }}
	as, out, _ := testAssembly(t, chat, "")
	as.opts.session = sessionNew
	as.opts.output = outputJSON
	a, err := assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.oneShot(context.Background(), "q"); err != nil {
		t.Fatalf("oneShot: %v", err)
	}
	var got jsonAnswer
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, out.String())
	}
	if got.Answer != "42" || got.FinishReason != agentapi.FinishStop || got.SessionID != a.sess.ID() || got.Usage == nil || got.Usage.OutputTokens != 3 || got.Error != nil {
		t.Errorf("answer = %+v", got)
	}
	a.close()

	// A failing provider is an exit status and an error field, and the
	// object is still the only thing on stdout.
	failing := &fakeChat{fail: agentapi.NewError(agentapi.ErrAuth, "bad key").WithProvider("fake")}
	as, out, _ = testAssembly(t, failing, "")
	as.opts.output = outputJSON
	a, err = assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	err = a.oneShot(context.Background(), "q")
	var e *agentapi.Error
	if !errors.As(err, &e) || e.Kind != agentapi.ErrAuth {
		t.Fatalf("oneShot with a failing provider: %v", err)
	}
	got = jsonAnswer{}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout: %v\n%s", err, out.String())
	}
	if got.Answer != "" || got.FinishReason != agentapi.FinishError || got.Error == nil || got.Error.Kind != agentapi.ErrAuth || got.SessionID != "" {
		t.Errorf("failed answer = %+v", got)
	}
}

// TestREPL drives the interactive loop through the shared line reader:
// commands, an unknown command, blank lines, two questions whose history
// accumulates, and /exit. Ctrl-C is a signal on the channel the loop
// watches: at the prompt it prints how to leave, during an answer it
// cancels that turn and the loop goes on.
func TestREPL(t *testing.T) {
	chat := &fakeChat{reply: func(n int) string { return strings.Repeat("a", n) }}
	as, out, stderr := testAssembly(t, chat, "/help\n/bogus x\n\nfirst\nsecond\n/exit\nnever sent\n")
	as.opts.session = sessionNew
	a, err := assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	sigs := make(chan os.Signal, 1)
	if err := a.repl(context.Background(), sigs); err != nil {
		t.Fatalf("repl: %v", err)
	}
	a.close()

	if out.String() != "a\naa\n" {
		t.Errorf("stdout = %q", out.String())
	}
	for _, want := range []string{"interactive session", "/exit, /quit", "unknown command /bogus", "> "} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	reqs := chat.seen()
	if len(reqs) != 2 || len(reqs[0].Messages) != 2 || len(reqs[1].Messages) != 4 {
		t.Fatalf("history did not accumulate: %d requests, messages %v", len(reqs), func() (n []int) {
			for _, r := range reqs {
				n = append(n, len(r.Messages))
			}
			return
		}())
	}
	if reqs[1].Messages[2].Text() != "a" || reqs[1].Messages[3].Text() != "second" {
		t.Errorf("second request carries %q then %q", reqs[1].Messages[2].Text(), reqs[1].Messages[3].Text())
	}
	infos, err := as.store.List(as.cwd)
	if err != nil || len(infos) != 1 || infos[0].Messages != 4 {
		t.Errorf("sessions = %+v, %v; want one with 4 messages", infos, err)
	}

	// EOF at the prompt ends the loop like /exit.
	as, _, _ = testAssembly(t, chat, "")
	a, err = assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.repl(context.Background(), sigs); err != nil {
		t.Errorf("repl at EOF: %v", err)
	}
}

func TestREPLInterrupt(t *testing.T) {
	// Ctrl-C during an answer: the provider hangs until cancelled, the
	// turn reports "interrupted", and the next line is still read.
	hanging := &fakeChat{hang: true}
	pr, pw := io.Pipe()
	as, out, stderr := testAssembly(t, hanging, "")
	as.io.in = pr
	a, err := assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	sigs := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() { done <- a.repl(context.Background(), sigs) }()

	go func() {
		_, _ = io.WriteString(pw, "slow question\n")
		// Wait until the turn is under way — the provider has been asked.
		for len(hanging.seen()) == 0 {
			time.Sleep(5 * time.Millisecond)
		}
		sigs <- os.Interrupt
		// Ctrl-C at the prompt is a hint, not an exit.
		time.Sleep(20 * time.Millisecond)
		sigs <- os.Interrupt
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(pw, "/quit\n")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("repl: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repl did not return")
	}
	_ = pw.Close()
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing from a cancelled turn", out.String())
	}
	for _, want := range []string{"hint: interrupted", "Ctrl-D or type /exit"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
}

// TestTrace checks the --debug log end to end: the profile, the system
// prompt, the messages and the turn's end are in it, and the API key is
// not — even though the profile line would print it if String() did not
// mask it and the writer did not redact it.
func TestTrace(t *testing.T) {
	chat := &fakeChat{reply: func(int) string { return "traced" }}
	as, _, stderr := testAssembly(t, chat, "")
	trace, err := debuglog.Open(filepath.Join(t.TempDir(), "log"), time.Now(), as.cfg.Secrets())
	if err != nil {
		t.Fatal(err)
	}
	as.trace = trace
	a, err := assemble(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.oneShot(context.Background(), "why?"); err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trace.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "sk-test-1234567890abcdef") {
		t.Fatalf("the key leaked into the trace:\n%s", text)
	}
	for _, want := range []string{"profile {Name:fake", "window 4000", "system prompt:", `turn: "why?"`, `message assistant:`, `"traced"`, "usage:", "turn end: stop"} {
		if !strings.Contains(text, want) {
			t.Errorf("trace lacks %q:\n%s", want, text)
		}
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr:\n%s", stderr.String())
	}
}

func TestPrintModelsAndHumanSize(t *testing.T) {
	var out bytes.Buffer
	err := printModels(&out, []provider.Model{
		{Name: "qwen2.5:7b", SizeBytes: 4_700_000_000, Modified: time.Date(2026, 8, 1, 12, 0, 0, 0, time.Local)},
		{Name: "gpt-4o", Owner: "openai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "qwen2.5:7b") || !strings.Contains(lines[0], "4.4 GB") || !strings.Contains(lines[0], "2026-08-01") ||
		!strings.HasPrefix(lines[1], "gpt-4o") || !strings.HasSuffix(lines[1], "openai") {
		t.Errorf("printModels:\n%s", out.String())
	}
	for n, want := range map[int64]string{512: "512 B", 2048: "2.0 KB", 3 << 20: "3.0 MB", 4_700_000_000: "4.4 GB"} {
		if got := humanSize(n); got != want {
			t.Errorf("humanSize(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestListSessions(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	cwd := t.TempDir()
	var out, stderr bytes.Buffer
	if err := listSessions(store, cwd, &out, &stderr); err != nil || out.Len() != 0 || !strings.Contains(stderr.String(), "no recorded sessions") {
		t.Errorf("empty: %v, out %q, stderr %q", err, out.String(), stderr.String())
	}
	s, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	out.Reset()
	if err := listSessions(store, cwd, &out, &stderr); err != nil || !strings.Contains(out.String(), s.ID()) || !strings.Contains(out.String(), "--session") {
		t.Errorf("one session: %v, out %q", err, out.String())
	}
}

func TestVersionFlag(t *testing.T) {
	root := newRootCommand()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "hint version ") {
		t.Fatalf("--version printed %q", out.String())
	}
}

func TestFormatVersion(t *testing.T) {
	// ldflags win; build info fills in only what the build left blank.
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.2.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "0123456789abcdef"},
			{Key: "vcs.time", Value: "2026-09-07T10:00:00Z"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	if got, want := formatVersion("dev", "none", "unknown", info), "v0.2.0 (commit 0123456-dirty, built 2026-09-07T10:00:00Z)"; got != want {
		t.Errorf("from build info: got %q, want %q", got, want)
	}
	if got, want := formatVersion("0.3.0", "abc1234", "2026-10-01", info), "0.3.0 (commit abc1234, built 2026-10-01)"; got != want {
		t.Errorf("ldflags set: got %q, want %q", got, want)
	}
	if got, want := formatVersion("dev", "none", "unknown", nil), "dev (commit none, built unknown)"; got != want {
		t.Errorf("no build info: got %q, want %q", got, want)
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}
	if got, want := formatVersion("dev", "none", "unknown", devel), "dev (commit none, built unknown)"; got != want {
		t.Errorf("(devel) is not a version: got %q, want %q", got, want)
	}
}
