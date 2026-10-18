package main

import (
	"context"
	"fmt"

	"github.com/mrYush/hint/internal/tui"
)

// tui runs the interactive session as the full-screen shell. The screen
// is gone when it exits, so the note that says how to continue the
// conversation is printed afterwards, where the REPL's scrollback would
// have been.
func (a *app) tui(ctx context.Context) error {
	out, err := tui.Run(ctx, tui.Options{
		Client:        a.client,
		SessionID:     a.sessionID,
		Recorded:      a.recorded,
		History:       a.history,
		PickSession:   a.sessionID == "",
		ServerVersion: a.client.ServerVersion(),
	})
	if out.SessionID != "" && out.Recorded {
		fmt.Fprintf(a.io.err, "hint: session %s is recorded; hint -c continues it\n", out.SessionID)
	}
	return err
}
