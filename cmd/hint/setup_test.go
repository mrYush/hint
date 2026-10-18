package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/pkg/agentapi"
)

func testSetup(t *testing.T) (*setupInfo, string) {
	t.Helper()
	global := filepath.Join(t.TempDir(), "hint", "config.yaml")
	return &setupInfo{
		profile: config.Profile{Name: "api-bar", BaseURL: "https://gw.test/openai", Model: "m1"},
		locs:    config.Locations{Global: global},
	}, global
}

func TestAskSetupWritesTheAnswersAndNeverTheKey(t *testing.T) {
	t.Setenv("MY_HINT_KEY", "") // set, so no "export it" reminder is expected
	s, global := testSetup(t)
	var out bytes.Buffer
	lines := lineIO{lines: console.NewLineReader(strings.NewReader("\n\nnew-model\nMY_HINT_KEY\n")), out: &out}
	if profile, err := askSetup(context.Background(), lines, &out, s); err != nil || profile != "api-bar" {
		t.Fatalf("askSetup = %q, %v", profile, err)
	}
	data, err := os.ReadFile(global)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{"name: api-bar", "base_url: https://gw.test/openai", "model: new-model", "api_key: ${MY_HINT_KEY}"} {
		if !strings.Contains(got, want) {
			t.Errorf("config lacks %q:\n%s", want, got)
		}
	}
	if !strings.Contains(out.String(), "saved to "+global) {
		t.Errorf("no word on where it went:\n%s", out.String())
	}
	if strings.Contains(out.String(), "is not set in this shell") {
		t.Errorf("reminded about a variable that is set:\n%s", out.String())
	}
}

func TestSaveRemindsAboutAnUnsetVariableAndAnOverridingFile(t *testing.T) {
	s, _ := testSetup(t)
	project := filepath.Join(t.TempDir(), ".hint", "config.yaml")
	s.locs.Read = []string{project}
	msg, err := s.save("api-bar", map[string]string{"api_key": "${HINT_TEST_SURELY_UNSET}"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "HINT_TEST_SURELY_UNSET is not set in this shell") || !strings.Contains(msg, project+" is read after the global file") {
		t.Errorf("message = %q", msg)
	}
	if _, err := (&setupInfo{}).save("a", nil); err == nil {
		t.Error("saved without anywhere to write")
	}
}

func TestAuthHelp(t *testing.T) {
	s, global := testSetup(t)
	auth := agentapi.NewError(agentapi.ErrAuth, "Unauthorized")

	a := &app{setup: s}
	if help := a.authHelp(auth); !strings.Contains(help, "setup writes to "+global) || !strings.Contains(help, `profile "api-bar"`) {
		t.Errorf("help = %q; want the profile and the file", help)
	}
	if help := a.authHelp(agentapi.NewError(agentapi.ErrNetwork, "down")); help != "" {
		t.Errorf("a network error got setup help: %q", help)
	}
	remote := &app{opts: options{connect: connectAuto}}
	if help := remote.authHelp(auth); !strings.Contains(help, "another process") {
		t.Errorf("--connect help = %q", help)
	}
}

// scripted is a setupIO with a hidden key, for the path a bare terminal takes.
type scripted struct {
	answers []string
	secret  string
	asked   []string
}

func (s *scripted) Line(_ context.Context, prompt string) (string, error) {
	s.asked = append(s.asked, prompt)
	a := s.answers[0]
	s.answers = s.answers[1:]
	return a, nil
}

func (s *scripted) Secret(_ context.Context, prompt string) (string, bool, error) {
	s.asked = append(s.asked, prompt)
	return s.secret, true, nil
}

func TestAskSetupTakesTheKeyWhereItCanBeHidden(t *testing.T) {
	s, global := testSetup(t)
	s.profile.Model = ""
	// profile, base URL, then the model: empty is asked again, since the
	// profile has none.
	in := &scripted{answers: []string{"", "", "", "picked-model"}, secret: "sk-typed-123"}
	var out bytes.Buffer
	if _, err := askSetup(context.Background(), in, &out, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(global)
	got := string(data)
	if !strings.Contains(got, "api_key: sk-typed-123") || !strings.Contains(got, "model: picked-model") {
		t.Errorf("the typed key or the model is not in the config:\n%s", got)
	}
	if strings.Contains(out.String(), "sk-typed-123") {
		t.Errorf("the key was echoed:\n%s", out.String())
	}
	asked := strings.Join(in.asked, "\n")
	if strings.Count(asked, "model [") != 2 || !strings.Contains(asked, "required") || !strings.Contains(out.String(), "a model is needed") {
		t.Errorf("an empty model was not asked again as required: %v\n%s", in.asked, out.String())
	}
}

func TestFirstRunSetupOnlyForMissingConfiguration(t *testing.T) {
	var out bytes.Buffer
	notATerminal := stdio{in: strings.NewReader(""), out: &out, err: &out}
	if firstRunSetup(context.Background(), notATerminal, config.ErrNoProvider) {
		t.Error("asked questions on input that is not a terminal")
	}
	tty := stdio{in: os.Stdin, out: &out, err: &out, tty: true}
	if firstRunSetup(context.Background(), tty, errors.New("config: parsing x: bad yaml")) {
		t.Error("offered setup for an error setup cannot fix")
	}
}

// secrets is a setupIO whose hidden answers come one per question.
type secrets struct{ keys []string }

func (s *secrets) Line(context.Context, string) (string, error) { return "", nil }

func (s *secrets) Secret(context.Context, string) (string, bool, error) {
	k := s.keys[0]
	s.keys = s.keys[1:]
	return k, true, nil
}

func TestAskKeyRepeatsUntilAProfileWithoutAKeyGetsOne(t *testing.T) {
	var out bytes.Buffer
	in := &secrets{keys: []string{"", "", "sk-third"}}
	key, err := askKey(context.Background(), in, &out, true)
	if err != nil || key != "sk-third" {
		t.Fatalf("askKey = %q, %v", key, err)
	}
	if strings.Count(out.String(), "a key is needed") != 2 || !strings.Contains(out.String(), "API key") {
		t.Errorf("the empty answers were not explained:\n%s", out.String())
	}
	// With a key already in place, Enter keeps it.
	key, err = askKey(context.Background(), &secrets{keys: []string{""}}, &out, false)
	if err != nil || key != "" {
		t.Errorf("optional key = %q, %v", key, err)
	}
}

func TestConfigSetUnsetShow(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("APIBAR_KEY", "sk-from-env-1234567890")
	t.Setenv("HOME", home)
	s := newSetupInfo(nil, cwd, config.Flags{})
	s.cwd = cwd

	if _, err := s.set("overview.depth", "-3", false); err == nil {
		t.Error("a negative depth was written")
	}
	if _, err := s.set("providers.api-bar.api_key", "${APIBAR_KEY}", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.set("providers.api-bar.model", "m", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.set("overview.depth", "4", true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.set("providers.api-bar.api_key", "sk-literal", true); err == nil || !strings.Contains(err.Error(), "git") {
		t.Errorf("a literal key went into the project file: %v", err)
	}
	if _, err := s.set("providers.api-bar.api_key", "${OTHER}", true); err != nil {
		t.Errorf("a ${VAR} key was refused for the project file: %v", err)
	}
	project, err := os.ReadFile(filepath.Join(cwd, ".hint", "config.yaml"))
	if err != nil || !strings.Contains(string(project), "depth: 4") {
		t.Fatalf("project file = %q, %v", project, err)
	}

	// The project value wins; show says so, with the key masked.
	s.locs = config.Locate(home, cwd, os.LookupEnv)
	t.Setenv("OTHER", "sk-other-0987654321")
	out, err := s.show()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"overview.depth = 4", "providers.api-bar.kind = openai", "providers.api-bar.header_timeout = (unset)"} {
		if !strings.Contains(out, want) {
			t.Errorf("show lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "sk-other-0987654321") {
		t.Errorf("show printed the key:\n%s", out)
	}

	// Unset in the project hands the key back to the global layer.
	if _, err := s.unset("overview.depth", true); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.show(); !strings.Contains(out, "overview.depth = 2") {
		t.Errorf("after unset the default should apply:\n%s", out)
	}
	if !strings.Contains(keysText(), "providers.<profile>.context_window") {
		t.Error("keys does not list the profile fields")
	}
}
