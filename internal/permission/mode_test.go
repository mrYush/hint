package permission_test

import (
	"testing"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestMode_AsksMatrix is the checklist's "every class × every mode".
func TestMode_AsksMatrix(t *testing.T) {
	cases := []struct {
		mode  permission.Mode
		class agentapi.ActionClass
		want  bool
	}{
		{permission.ModeAsk, agentapi.ClassRead, false},
		{permission.ModeAsk, agentapi.ClassWrite, true},
		{permission.ModeAsk, agentapi.ClassExecute, true},
		{permission.ModeAutoEdit, agentapi.ClassRead, false},
		{permission.ModeAutoEdit, agentapi.ClassWrite, false},
		{permission.ModeAutoEdit, agentapi.ClassExecute, true},
		{permission.ModeYolo, agentapi.ClassRead, false},
		{permission.ModeYolo, agentapi.ClassWrite, false},
		{permission.ModeYolo, agentapi.ClassExecute, false},
		// A class this build does not know is treated as execute.
		{permission.ModeAutoEdit, agentapi.ActionClass("network"), true},
		{permission.ModeYolo, agentapi.ActionClass("network"), false},
	}
	for _, c := range cases {
		if got := c.mode.Asks(c.class); got != c.want {
			t.Errorf("%s.Asks(%s) = %v, want %v", c.mode, c.class, got, c.want)
		}
	}
}

func TestMode_ZeroValueIsAsk(t *testing.T) {
	var m permission.Mode
	if m != permission.ModeAsk || m.String() != "ask" {
		t.Fatalf("zero Mode = %s, want ask", m)
	}
}

func TestParseMode(t *testing.T) {
	for _, s := range []string{"ask", "auto-edit", "yolo"} {
		m, err := permission.ParseMode(s)
		if err != nil || m.String() != s {
			t.Errorf("ParseMode(%q) = %s, %v", s, m, err)
		}
	}
	if m, err := permission.ParseMode(""); err != nil || m != permission.ModeAsk {
		t.Errorf("ParseMode(\"\") = %s, %v; want ask", m, err)
	}
	if _, err := permission.ParseMode("trust-me"); err == nil {
		t.Error("unknown mode accepted")
	}
	if s := permission.Mode(42).String(); s != "Mode(42)" {
		t.Errorf("String of an unknown mode = %q", s)
	}
}
