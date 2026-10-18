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
	if err := askSetup(context.Background(), lines, &out, s); err != nil {
		t.Fatal(err)
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
	msg, err := s.save("api-bar", "", "", "${HINT_TEST_SURELY_UNSET}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "HINT_TEST_SURELY_UNSET is not set in this shell") || !strings.Contains(msg, project+" is read after the global file") {
		t.Errorf("message = %q", msg)
	}
	if _, err := (&setupInfo{}).save("a", "", "", ""); err == nil {
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
	in := &scripted{answers: []string{"", "", ""}, secret: "sk-typed-123"}
	var out bytes.Buffer
	if err := askSetup(context.Background(), in, &out, s); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(global)
	got := string(data)
	if !strings.Contains(got, "api_key: sk-typed-123") {
		t.Errorf("the typed key is not in the config:\n%s", got)
	}
	if strings.Contains(got, "model:") {
		t.Errorf("an unanswered model was written instead of left to the default:\n%s", got)
	}
	if strings.Contains(out.String(), "sk-typed-123") {
		t.Errorf("the key was echoed:\n%s", out.String())
	}
	if !strings.Contains(strings.Join(in.asked, "\n"), "built-in default of the kind") {
		t.Errorf("the model question does not say what an empty answer means: %v", in.asked)
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
