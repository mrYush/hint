package permission

import (
	"fmt"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Mode is a run's confirmation policy: which action classes ask the user
// before running. The zero value is [ModeAsk], the safe default.
type Mode int

const (
	// ModeAsk confirms every write and execute. The default.
	ModeAsk Mode = iota
	// ModeAutoEdit lets writes through silently and still confirms every
	// execute: a shell command is the one action tool.Root cannot confine.
	ModeAutoEdit
	// ModeYolo confirms nothing. The CLI prints a loud warning when it is
	// selected; the mode itself does not lift the working-directory
	// confinement of the file tools.
	ModeYolo
)

// String returns the mode's flag spelling.
func (m Mode) String() string {
	switch m {
	case ModeAsk:
		return "ask"
	case ModeAutoEdit:
		return "auto-edit"
	case ModeYolo:
		return "yolo"
	default:
		return fmt.Sprintf("Mode(%d)", int(m))
	}
}

// ParseMode returns the Mode spelled by s ("ask", "auto-edit", "yolo").
func ParseMode(s string) (Mode, error) {
	switch s {
	case "ask", "":
		return ModeAsk, nil
	case "auto-edit":
		return ModeAutoEdit, nil
	case "yolo":
		return ModeYolo, nil
	default:
		return ModeAsk, fmt.Errorf("permission: unknown mode %q (want ask, auto-edit or yolo)", s)
	}
}

// Asks reports whether class needs the user's confirmation under m. Read
// never asks in any mode; an unknown class is treated as execute, the
// most restrictive answer, so a mislabelled tool errs on the side of
// prompting.
func (m Mode) Asks(class agentapi.ActionClass) bool {
	switch class {
	case agentapi.ClassRead:
		return false
	case agentapi.ClassWrite:
		return m == ModeAsk
	case agentapi.ClassExecute:
		return m != ModeYolo
	default:
		return m != ModeYolo
	}
}
