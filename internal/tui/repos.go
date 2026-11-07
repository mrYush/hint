package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/shell"
)

type reposMsg struct {
	result    rpc.RepoResult
	err       error
	sessionID string
}

func (m model) reposCommand(args []string) (model, tea.Cmd) {
	req, err := shell.ParseRepos(m.sessionID, args)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	m.busy = true
	m.cancelTurn = cancel
	m.status = "Reading workspace…"
	return m, func() tea.Msg {
		defer cancel()
		result, err := m.cl.Repos(ctx, req)
		return reposMsg{result: result, err: err, sessionID: req.SessionID}
	}
}

func (m model) reposResult(msg reposMsg) (model, tea.Cmd) {
	if msg.sessionID != m.sessionID {
		return m, nil
	}
	m.busy = false
	m.cancelTurn = nil
	if msg.err != nil {
		m.status = msg.err.Error()
		return m, nil
	}
	m.status = ""
	m.add(entry{kind: entryNotice, text: msg.result.Text, done: true})
	return m, nil
}
