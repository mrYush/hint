package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/pkg/agentapi"
)

// bashCall makes the fake model call bash with command on its first
// request and answer with text afterwards.
func bashCall(command string) func(int) *agentapi.ToolCall {
	return func(n int) *agentapi.ToolCall {
		if n != 1 {
			return nil
		}
		return &agentapi.ToolCall{ID: "call-1", Name: "bash", Arguments: json.RawMessage(`{"command":` + strconv.Quote(command) + `}`)}
	}
}

func readCall(path string) func(int) *agentapi.ToolCall {
	return func(n int) *agentapi.ToolCall {
		if n != 1 {
			return nil
		}
		return &agentapi.ToolCall{ID: "call-1", Name: "read_file", Arguments: json.RawMessage(`{"path":` + strconv.Quote(path) + `}`)}
	}
}

// toolResultText is the text of the tool result the fake provider saw in
// its second request.
func toolResultText(t *testing.T, chat *fakeChat) string {
	t.Helper()
	reqs := chat.seen()
	if len(reqs) < 2 {
		t.Fatalf("%d requests, want the call and the answer", len(reqs))
	}
	for _, m := range reqs[1].Messages {
		if m.Role == agentapi.RoleTool {
			return m.Text()
		}
	}
	t.Fatal("no tool result in the second request")
	return ""
}

const testKey = "sk-test-1234567890abcdef"

// TestBashRunsWithoutTheKeys: `env` from the model does not show the
// key, whether it sits under a variable the profile refers to, a
// well-known one, or a copy under another name.
func TestBashRunsWithoutTheKeys(t *testing.T) {
	t.Setenv("MY_PROFILE_KEY", testKey)
	t.Setenv("APIBAR_KEY", "sk-wellknown-1234567890")
	t.Setenv("COPY_OF_KEY", testKey)
	t.Setenv("HARMLESS", "stays")
	chat := &fakeChat{reply: func(int) string { return "done" }, call: bashCall("env")}
	as, sio, _, _ := testAssembly(t, chat, "y\n")
	// A configuration loaded from a file, so the loader records which
	// variable the key came from.
	home := t.TempDir()
	// XDG_CONFIG_HOME beats HomeDir in the loader; a runner that sets it
	// would send the loader past the file written below.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	if err := os.MkdirAll(filepath.Join(home, ".config", "hint"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "hint", "config.yaml"), []byte("providers:\n  - name: fake\n    kind: openai\n    api_key: ${MY_PROFILE_KEY}\n    model: m\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadFrom(config.Options{HomeDir: home, WorkingDir: as.cwd, LookupEnv: os.LookupEnv})
	if err != nil {
		t.Fatal(err)
	}
	cfg.Overview = as.cfg.Overview
	as.cfg = cfg
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.oneShot(context.Background(), "show the environment"); err != nil {
		t.Fatalf("oneShot: %v", err)
	}
	a.close()
	got := toolResultText(t, chat)
	for _, bad := range []string{"MY_PROFILE_KEY=", "APIBAR_KEY=", "COPY_OF_KEY=", testKey, "sk-wellknown"} {
		if strings.Contains(got, bad) {
			t.Errorf("the subprocess saw %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, "HARMLESS=stays") {
		t.Errorf("the rest of the environment was lost:\n%s", got)
	}
}

// TestToolOutputWithTheKeyIsRedacted: a key that reaches a tool's output
// some other way is "***" by the time the model, the event and the
// session see it.
func TestToolOutputWithTheKeyIsRedacted(t *testing.T) {
	// The key sits in a file the model asks bash to print: the command
	// itself is clean, so everything that carries the key downstream is
	// the tool's output.
	chat := &fakeChat{reply: func(int) string { return "done" }, call: bashCall("cat secret.txt")}
	as, sio, _, stderr := testAssembly(t, chat, "y\n")
	if err := os.WriteFile(filepath.Join(as.cwd, "secret.txt"), []byte("token="+testKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	as.opts.session = sessionNew
	a, err := assemble(context.Background(), as, sio)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.oneShot(context.Background(), "print it"); err != nil {
		t.Fatalf("oneShot: %v", err)
	}
	a.close()
	if got := toolResultText(t, chat); !strings.Contains(got, "token=***") || strings.Contains(got, testKey) {
		t.Errorf("the model saw %q", got)
	}
	if strings.Contains(stderr.String(), testKey) {
		t.Errorf("the key reached the terminal:\n%s", stderr.String())
	}
	infos := sessionsOf(t, as)
	if len(infos) != 1 {
		t.Fatalf("%d sessions", len(infos))
	}
	data, err := os.ReadFile(infos[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(testKey)) {
		t.Errorf("the key reached the session file:\n%s", data)
	}
}

// TestFileToolsRefuseTheConfigAndSessions: with the working directory at
// $HOME, read_file of the config file and of a session is refused, and
// the project's own .hint/config.yaml too, while .hint/rules is read. The
// legacy names the loader still reads are refused as well.
func TestFileToolsRefuseTheConfigAndSessions(t *testing.T) {
	base, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "x" }}, "")
	home := base.cwd
	configDir := filepath.Join(home, ".config", "hint")
	sessions := filepath.Join(home, ".local", "share", "hint", "sessions")
	for _, p := range []string{filepath.Join(configDir, "config.yaml"), filepath.Join(sessions, "x", "s.jsonl"), filepath.Join(home, ".hint", "config.yaml"), filepath.Join(home, ".hint", "rules", "go.md"), filepath.Join(home, ".config", "hint.yaml"), filepath.Join(home, "hint.yaml")} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("api_key: "+testKey+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		path    string
		refused bool
	}{
		{".config/hint/config.yaml", true},
		{".local/share/hint/sessions/x/s.jsonl", true},
		{".hint/config.yaml", true},
		{".hint/rules/go.md", false},
		{".config/hint.yaml", true},
		{"hint.yaml", true},
	} {
		chat := &fakeChat{reply: func(int) string { return "done" }, call: readCall(c.path)}
		as, sio, _, _ := testAssembly(t, chat, "")
		as.cwd = home
		as.denied = deniedPaths(home, home, func(string) (string, bool) { return "", false }, sessions)
		a, err := assemble(context.Background(), as, sio)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.oneShot(context.Background(), "read it"); err != nil {
			t.Fatalf("oneShot: %v", err)
		}
		a.close()
		got := toolResultText(t, chat)
		if c.refused && (!strings.Contains(got, "hint's own") || strings.Contains(got, testKey)) {
			t.Errorf("read_file %s = %q; want it refused", c.path, got)
		}
		if !c.refused && strings.Contains(got, "hint's own") {
			t.Errorf("read_file %s was refused: %q", c.path, got)
		}
	}
}

func TestSetupStoresTheKeyInTheKeyring(t *testing.T) {
	s, global := testSetup(t)
	ring := config.MapKeyring{}
	s.store, s.keyring = config.StoreAuto, ring
	msg, err := s.save("api-bar", map[string]string{"api_key": testKey, "model": "m"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(global)
	if strings.Contains(string(data), testKey) || !strings.Contains(string(data), "api_key: keyring:api-bar") {
		t.Errorf("the file holds the key or no reference:\n%s", data)
	}
	if ring["api-bar"] != testKey || !strings.Contains(msg, "secret store") || !strings.Contains(msg, "show-key") {
		t.Errorf("stored %q, message %q", ring["api-bar"], msg)
	}
	// A reference is written as it is, nothing is stored.
	if _, err := s.save("api-bar", map[string]string{"api_key": "${VAR}"}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(global)
	if !strings.Contains(string(data), "api_key: ${VAR}") || len(ring) != 1 {
		t.Errorf("a reference was stored or rewritten:\n%s", data)
	}

	// No store answering: auto falls back to the file and says so;
	// keyring insists and fails; file never asks.
	s.keyring = failingRing{}
	msg, err = s.save("api-bar", map[string]string{"api_key": testKey})
	if err != nil || !strings.Contains(msg, "no secret store answered") || !strings.Contains(msg, "anyone running as you") {
		t.Errorf("auto without a store: %q, %v", msg, err)
	}
	data, _ = os.ReadFile(global)
	if !strings.Contains(string(data), "api_key: "+testKey) {
		t.Errorf("the fallback did not write the key:\n%s", data)
	}
	s.store = config.StoreKeyring
	if _, err := s.save("api-bar", map[string]string{"api_key": "sk-other-1234567890"}); err == nil || !strings.Contains(err.Error(), "D-Bus") {
		t.Errorf("keyring policy without a store: %v", err)
	}
	s.store, s.keyring = config.StoreFile, ring
	if _, err := s.save("api-bar", map[string]string{"api_key": "sk-file-1234567890"}); err != nil || len(ring) != 1 {
		t.Errorf("file policy touched the store: %v, %v", err, ring)
	}
}

type failingRing struct{}

func (failingRing) Get(string) (string, error) {
	return "", errors.New("config: secret store: no D-Bus session")
}
func (failingRing) Set(string, string) error {
	return errors.New("config: secret store: no D-Bus session")
}
func (failingRing) Delete(string) error { return errors.New("config: secret store: no D-Bus session") }

func TestShowKeyAsksAndMasks(t *testing.T) {
	cfg := &config.Config{Providers: []config.Profile{
		{Name: "p", Kind: config.KindOpenAI, APIKey: testKey, Model: "m", KeyFrom: config.KeyFromKeyring, KeyRef: "keyring:p"},
		{Name: "nokey", Kind: config.KindOpenAI, Model: "m", KeyFrom: config.KeyFromKeyring, KeyRef: "keyring:nokey", KeyError: config.ErrKeyringNotFound},
	}, DefaultProvider: "p"}
	var out bytes.Buffer
	in := &scripted{answers: []string{"y"}}
	if err := showKey(cfg, "p", false, in, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), testKey) || !strings.Contains(out.String(), config.Mask(testKey)) {
		t.Errorf("masked output = %q", out.String())
	}
	if !strings.Contains(in.asked[0], "secret store") || !strings.Contains(in.asked[0], "masked") {
		t.Errorf("the question does not say where from and how: %q", in.asked[0])
	}
	out.Reset()
	in = &scripted{answers: []string{"y"}}
	if err := showKey(cfg, "p", true, in, &out); err != nil || strings.TrimSpace(out.String()) != testKey {
		t.Errorf("--reveal = %q, %v", out.String(), err)
	}
	out.Reset()
	in = &scripted{answers: []string{"n"}}
	if err := showKey(cfg, "p", true, in, &out); err == nil || out.Len() != 0 {
		t.Errorf("a refused question printed %q, %v", out.String(), err)
	}
	if err := showKey(cfg, "nokey", false, &scripted{answers: []string{"y"}}, &out); err == nil || !strings.Contains(err.Error(), "keyring:nokey") {
		t.Errorf("a profile without a key: %v", err)
	}
	if err := showKey(cfg, "ghost", false, &scripted{answers: []string{"y"}}, &out); err == nil {
		t.Error("an unknown profile was shown")
	}
}

func TestWarnFileKeys(t *testing.T) {
	cfg := &config.Config{Providers: []config.Profile{{Name: "p", APIKey: testKey, KeyFrom: config.KeyFromFile}}}
	var out bytes.Buffer
	cfg.SecretStore.Store = config.StoreFile
	warnFileKeys(cfg, &out)
	if out.Len() != 0 {
		t.Errorf("store file warned: %q", out.String())
	}
	// With the store policy auto the warning depends on the real OS
	// store; only that it never prints the key is checked here.
	cfg.SecretStore.Store = config.StoreAuto
	warnFileKeys(cfg, &out)
	if strings.Contains(out.String(), testKey) {
		t.Errorf("the warning printed the key: %q", out.String())
	}
}
