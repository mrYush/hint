package main

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/rpc"
)

// twoProfiles is a configuration with the fake default profile and a
// second one, "other", that a session can switch to; "nokey" exists but
// cannot be used.
func twoProfiles() *config.Config {
	return &config.Config{
		Providers: []config.Profile{
			{Name: "fake", Kind: config.KindOpenAI, APIKey: "sk-test-1234567890abcdef", Model: "m", ContextWindow: 4000},
			{Name: "other", Kind: config.KindOpenAI, APIKey: "sk-other-1234567890abcdef", Model: "mo", ContextWindow: 2000},
			{Name: "nokey", Kind: config.KindOpenAI, Model: "mn"},
		},
		DefaultProvider: "fake",
		Instructions:    config.InstructionSettings{Budget: config.DefaultInstructionBudget},
		Overview:        config.OverviewSettings{Depth: 1, MaxEntries: 10},
	}
}

// testEngine serves the engine built from as and returns it with a
// client, so a test can seed the engine's provider cache.
func testEngine(t *testing.T, as assembly) (*rpcEngine, *rpc.Client) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	eng, err := newEngine(ctx, as)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	srv := &rpc.Server{Engine: eng}
	shell, core := net.Pipe()
	go func() { _ = srv.ServeConn(ctx, core) }()
	cl, err := rpc.Dial(ctx, shell, "")
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cl.Close(); srv.Close(); cancel() })
	return eng, cl
}

// drive runs one text-only turn to its end.
func drive(t *testing.T, cl *rpc.Client, sessionID, text string) {
	t.Helper()
	turn, err := cl.StartTurn(context.Background(), sessionID, text)
	if err != nil {
		t.Fatalf("StartTurn: %v", err)
	}
	for range turn.Events() {
	}
	if turn.Err() != nil {
		t.Fatalf("turn: %v", turn.Err())
	}
}

func TestProfileSwitchChangesTheProviderAndIsRecorded(t *testing.T) {
	first := &fakeChat{reply: func(int) string { return "from fake" }}
	second := &fakeChat{reply: func(int) string { return "from other" }}
	as, _, _, _ := testAssembly(t, first, "")
	as.cfg = twoProfiles()
	eng, cl := testEngine(t, as)
	// The provider of "other" would be a real client; the cache seam
	// stands a fake in for it, as the assembly does for the default.
	eng.providers[rpc.ProfileRef{Profile: "other"}] = second
	ctx := context.Background()

	view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if view.Profile != "fake" || view.Model != "m" {
		t.Fatalf("a new session answers as %s/%s; want the default profile and its model", view.Profile, view.Model)
	}
	drive(t, cl, view.ID, "one")

	ref, err := cl.SetProfile(ctx, view.ID, rpc.ProfileRef{Profile: "other"})
	if err != nil {
		t.Fatalf("SetProfile: %v", err)
	}
	if ref != (rpc.ProfileRef{Profile: "other", Model: "mo"}) {
		t.Errorf("effective profile = %+v; want other/mo", ref)
	}
	drive(t, cl, view.ID, "two")
	if len(first.seen()) != 1 || len(second.seen()) != 1 {
		t.Fatalf("requests: fake %d, other %d; want one each", len(first.seen()), len(second.seen()))
	}
	// The conversation went on: the second provider saw the first
	// exchange in its history.
	if req := second.seen()[0]; len(req.Messages) < 3 || req.Messages[len(req.Messages)-1].Text() != "two" {
		t.Errorf("the switched provider got %d messages ending %q", len(req.Messages), req.Messages[len(req.Messages)-1].Text())
	}

	var rpcErr *rpc.Error
	if _, err := cl.SetProfile(ctx, view.ID, rpc.ProfileRef{Profile: "nope"}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Errorf("unknown profile = %v; want code %d", err, rpc.CodeNotFound)
	}
	if _, err := cl.SetProfile(ctx, view.ID, rpc.ProfileRef{Profile: "nokey"}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams || !strings.Contains(err.Error(), "API key") {
		t.Errorf("profile without a key = %v; want code %d naming the key", err, rpc.CodeInvalidParams)
	}

	// A later run restores the recorded profile and answers with it.
	again := as
	again.chat = &fakeChat{reply: func(int) string { return "fake again" }}
	eng2, cl2 := testEngine(t, again)
	third := &fakeChat{reply: func(int) string { return "other again" }}
	eng2.providers[rpc.ProfileRef{Profile: "other"}] = third
	restored, err := cl2.AttachSession(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Profile != "other" || restored.Model != "mo" || len(restored.Warnings) != 0 {
		t.Fatalf("restored view = %s/%s %v; want other/mo and no warnings", restored.Profile, restored.Model, restored.Warnings)
	}
	drive(t, cl2, view.ID, "three")
	if len(third.seen()) != 1 {
		t.Errorf("the restored profile's provider saw %d requests, want 1", len(third.seen()))
	}

	// A profile the configuration lost falls back to the default, with a
	// warning the client shows.
	lost := as
	lost.cfg = twoProfiles()
	lost.cfg.Providers = lost.cfg.Providers[:1]
	_, cl3 := testEngine(t, lost)
	fallen, err := cl3.AttachSession(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fallen.Profile != "fake" || len(fallen.Warnings) != 1 || !strings.Contains(fallen.Warnings[0], "not usable") {
		t.Errorf("attach with the profile gone = %s %v; want the default and one warning", fallen.Profile, fallen.Warnings)
	}
}

func TestProfilesReflectTheReloadedConfiguration(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "x" }}, "")
	as.cfg = twoProfiles()
	reloaded := twoProfiles()
	reloaded.Providers = append(reloaded.Providers, config.Profile{Name: "new", Kind: config.KindOllama, BaseURL: "http://127.0.0.1:1", Model: "tiny"})
	reloaded.FallbackProvider = "other"
	as.reload = func() (*config.Config, error) { return reloaded, nil }
	_, cl := testEngine(t, as)
	ctx := context.Background()

	infos, err := cl.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]rpc.ProfileInfo{}
	for _, p := range infos {
		byName[p.Name] = p
	}
	if len(infos) != 4 || !byName["fake"].Default || !byName["other"].Fallback || byName["new"].Kind != "ollama" {
		t.Fatalf("profiles = %+v; want the reloaded four with their roles", infos)
	}
	if byName["nokey"].Usable || !strings.Contains(byName["nokey"].Problem, "API key") {
		t.Errorf("nokey = %+v; want it marked unusable for its key", byName["nokey"])
	}
	// The switch sees the reloaded file too: a profile written since the
	// start is usable without a restart.
	view, err := cl.OpenSession(ctx, rpc.OpenOptions{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref, err := cl.SetProfile(ctx, view.ID, rpc.ProfileRef{Profile: "new"}); err != nil || ref.Model != "tiny" {
		t.Errorf("switch to a reloaded profile = %+v, %v", ref, err)
	}
}

func TestForkCutsAtATurn(t *testing.T) {
	chat := &fakeChat{reply: func(n int) string { return []string{"a1", "a2", "a3"}[n-1] }}
	as, _, _, _ := testAssembly(t, chat, "")
	_, cl := testEngine(t, as)
	ctx := context.Background()

	view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	drive(t, cl, view.ID, "q1")
	drive(t, cl, view.ID, "q2")

	fork, err := cl.ForkSession(ctx, rpc.ForkOptions{ID: view.ID, Turn: 1})
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if fork.ID == view.ID || fork.Parent == nil || *fork.Parent != (rpc.Parent{ID: view.ID, Turn: 1}) {
		t.Fatalf("fork = %+v; want a new session with parent %s@1", fork, view.ID)
	}
	if len(fork.Messages) != 2 || fork.Messages[0].Text() != "q1" || fork.Messages[1].Text() != "a1" {
		t.Fatalf("fork history = %d messages; want q1 and a1", len(fork.Messages))
	}
	if fork.Profile != "fake" {
		t.Errorf("fork answers as %q; want the source's profile", fork.Profile)
	}
	// The fork is a session of its own: a turn continues it, the source
	// is untouched.
	drive(t, cl, fork.ID, "q3")
	src, err := cl.AttachSession(ctx, view.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Messages) != 4 {
		t.Errorf("the source grew to %d messages", len(src.Messages))
	}
	infos := sessionsOf(t, as)
	var forks int
	for _, info := range infos {
		if info.Parent != nil {
			forks++
			if info.ID != fork.ID || info.Messages != 4 || info.Parent.ID != view.ID {
				t.Errorf("listed fork = %+v", info)
			}
		}
	}
	if len(infos) != 2 || forks != 1 {
		t.Errorf("%d sessions on disk, %d forks; want 2 and 1", len(infos), forks)
	}

	var rpcErr *rpc.Error
	if _, err := cl.ForkSession(ctx, rpc.ForkOptions{ID: view.ID, Turn: 3}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
		t.Errorf("fork past the last turn = %v; want code %d", err, rpc.CodeInvalidParams)
	}
	if _, err := cl.ForkSession(ctx, rpc.ForkOptions{ID: "nosuch", Turn: 1}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Errorf("fork of an unknown session = %v; want code %d", err, rpc.CodeNotFound)
	}

	// An ephemeral fork stays off the disk.
	eph, err := cl.ForkSession(ctx, rpc.ForkOptions{ID: view.ID, Turn: 2, Ephemeral: true})
	if err != nil || len(eph.Messages) != 4 || eph.Parent == nil {
		t.Fatalf("ephemeral fork = %+v, %v", eph, err)
	}
	if n := len(sessionsOf(t, as)); n != 2 {
		t.Errorf("the ephemeral fork reached the disk: %d sessions", n)
	}

	// A source that is not open on the engine is read from its file, by
	// id or by a unique prefix; no turn means the whole conversation.
	_, cl2 := testEngine(t, as)
	cold, err := cl2.ForkSession(ctx, rpc.ForkOptions{ID: view.ID[:6]})
	if err != nil || len(cold.Messages) != 4 || cold.Parent.ID != view.ID[:6] || cold.Parent.Turn != 2 {
		t.Fatalf("fork from disk = %+v, %v", cold, err)
	}
	empty, err := cl2.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cl2.ForkSession(ctx, rpc.ForkOptions{ID: empty.ID}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeInvalidParams {
		t.Errorf("fork of an empty session = %v; want code %d", err, rpc.CodeInvalidParams)
	}
}

// TestREPLProfileCommand pins /profile in the line REPL: alone it lists
// the profiles with the one in use marked, with a name it switches the
// session, and a profile that cannot be used is refused with its reason.
func TestREPLProfileCommand(t *testing.T) {
	first := &fakeChat{reply: func(int) string { return "from fake" }}
	second := &fakeChat{reply: func(int) string { return "from other" }}
	as, sio, out, stderr := testAssembly(t, first, "/profile\n/profile nokey\n/profile other\nhello\n/exit\n")
	as.cfg = twoProfiles()
	as.opts.session = sessionNew
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	a.srv.Engine.(*rpcEngine).providers[rpc.ProfileRef{Profile: "other"}] = second
	if a.profile != (rpc.ProfileRef{Profile: "fake", Model: "m"}) {
		t.Fatalf("the app starts with profile %+v", a.profile)
	}
	if err := a.repl(context.Background(), nil); err != nil {
		t.Fatalf("repl: %v", err)
	}
	a.close()
	if out.String() != "from other\n" {
		t.Errorf("stdout = %q; want the answer of the switched profile", out.String())
	}
	for _, want := range []string{"fake          openai   m  (in use, default)", "nokey", "✗", "needs an API key", "answering with other/mo from now on"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	if len(first.seen()) != 0 || len(second.seen()) != 1 {
		t.Errorf("requests: fake %d, other %d; want the question to reach other only", len(first.seen()), len(second.seen()))
	}
	if infos := sessionsOf(t, as); len(infos) != 1 || infos[0].Messages != 2 {
		t.Errorf("sessions = %+v; want one with the exchange", infos)
	}
}

func TestUseTUI(t *testing.T) {
	notATerminal, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = notATerminal.Close() }()
	terminal, err := os.OpenFile("/dev/null", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no /dev/null to stand in for a terminal")
	}
	defer func() { _ = terminal.Close() }()
	cases := []struct {
		name           string
		tuiFlag, plain bool
		stdout         *os.File
		want           bool
	}{
		{"terminal is the shell", false, false, terminal, true},
		{"a pipe is the REPL", false, false, notATerminal, false},
		{"--plain is the REPL", false, true, terminal, false},
		{"--tui asks for the shell", true, false, notATerminal, true},
	}
	for _, c := range cases {
		if got := useTUI(c.tuiFlag, c.plain, c.stdout); got != c.want {
			t.Errorf("%s: useTUI = %v, want %v", c.name, got, c.want)
		}
	}
}
