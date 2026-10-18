package main

import (
	"bytes"
	"context"
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
	lines := console.NewLineReader(strings.NewReader("\n\nnew-model\nMY_HINT_KEY\n"))
	var out bytes.Buffer
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
