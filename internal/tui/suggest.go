package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// Completion of what the user is typing (WP1.6): a short continuation
// from a model, shown in the status line, accepted with Tab. The core
// answers complete; this file decides when to ask — on Tab, after a pause,
// or never, as the configuration and /completion say — and keeps a
// request from ever getting in the way of typing: a request runs beside
// the input, an edit cancels it, and an answer for text that is no longer
// in the input is dropped.

// The thresholds of the idle trigger, which spends tokens without a key
// press and so is held back harder than Tab: a question shorter than
// minIdleInput is not worth a request, and a request follows the last one
// only after minIdleGap and minIdleGrowth more characters.
const (
	minIdleInput  = 12
	minIdleGap    = 2 * time.Second
	minIdleGrowth = 3
	// maxFailures is how many requests may fail in a row before the
	// shell stops asking for the rest of the session.
	maxFailures = 3
)

// completer is the completion state of the input. It is a pointer in the
// model, like the dialog and the pickers, because every Update sees the
// same request in flight.
type completer struct {
	// settings is the configuration's completion block; override is the
	// mode /completion set for this session, "" for none.
	settings rpc.CompletionSettings
	loaded   bool
	override string
	// disabled is why the shell stopped asking: the core's reason, or
	// the failures. Empty while asking is allowed.
	disabled string

	// seq numbers idle ticks and requests, so a stale one is told apart.
	seq int
	// inflight is the request the shell is waiting for, if any.
	inflight *suggestRequest
	// sugg is the suggestion on offer, for the exact text it was made for.
	sugg *suggestion

	requests, failed int
	failures         int
	usage            agentapi.Usage
	lastAt           time.Time
	lastLen          int

	// models caches profile.models per profile, for Tab on a model
	// argument; loading names the profile a load is in flight for.
	models  map[string][]string
	loading string
}

type suggestRequest struct {
	id      string
	forText string
	byTab   bool
}

type suggestion struct {
	forText string
	text    string
}

// The messages of this file.
type (
	completionSettingsMsg struct {
		s   rpc.CompletionSettings
		err error
	}
	completionMsg struct {
		id  string
		res rpc.CompleteResult
		err error
	}
	// idleTickMsg fires idle_after after an edit; seq says which edit.
	idleTickMsg struct{ seq int }
	// warnTickMsg fires warn_after after a request started; id says which.
	warnTickMsg struct{ id string }
	modelsMsg   struct {
		profile string
		models  []string
		err     error
	}
)

func newCompleter() *completer {
	return &completer{models: map[string][]string{}}
}

// mode is the trigger in force: the session's override, else the
// configuration's.
func (c *completer) mode() string {
	if c.override != "" {
		return c.override
	}
	if c.settings.Mode == "" {
		return "off"
	}
	return c.settings.Mode
}

func onTab(mode string) bool  { return mode == "tab" || mode == "both" }
func onIdle(mode string) bool { return mode == "idle" || mode == "both" }

func (c *completer) idleAfter() time.Duration {
	if c.settings.IdleAfterMs > 0 {
		return time.Duration(c.settings.IdleAfterMs) * time.Millisecond
	}
	return 600 * time.Millisecond
}

func (c *completer) warnAfter() time.Duration {
	if c.settings.WarnAfterMs > 0 {
		return time.Duration(c.settings.WarnAfterMs) * time.Millisecond
	}
	return 2 * time.Second
}

// loadCompletionSettings asks the core for the completion block; the
// answer decides the triggers.
func (m model) loadCompletionSettings() tea.Cmd {
	cl, ctx := m.cl, m.ctx
	return func() tea.Msg {
		s, err := cl.CompletionSettings(ctx)
		return completionSettingsMsg{s: s, err: err}
	}
}

func (m model) completionSettingsLoaded(msg completionSettingsMsg) model {
	if msg.err != nil {
		// An older core has no such method: completion stays off, and
		// /completion says why.
		m.cmpl.disabled = "this core has no completion (" + msg.err.Error() + ")"
		return m
	}
	m.cmpl.settings, m.cmpl.loaded = msg.s, true
	return m
}

// canAsk says whether a request makes sense right now: the input is a
// question, nothing else has the screen, and the shell is allowed to ask.
func (m model) canAsk() bool {
	c := m.cmpl
	if c.disabled != "" || c.mode() == "off" || m.sessionID == "" || m.busy {
		return false
	}
	if m.dialog != nil || m.picker != nil || m.ppicker != nil || m.setupForm != nil || m.sel != noSelection {
		return false
	}
	text := m.ta.Value()
	if strings.TrimSpace(text) == "" || strings.HasPrefix(text, "/") {
		return false
	}
	return m.ta.Line() == m.ta.LineCount()-1
}

// typed is what every edit of the input does to the completion: the
// suggestion is for text that is gone, a request in flight is for it
// too, and the idle trigger starts counting again.
func (m model) typed() (model, tea.Cmd) {
	c := m.cmpl
	c.sugg = nil
	var cmds []tea.Cmd
	if cmd := m.cancelSuggest(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if onIdle(c.mode()) && m.canAsk() {
		c.seq++
		seq := c.seq
		cmds = append(cmds, tea.Tick(c.idleAfter(), func(time.Time) tea.Msg { return idleTickMsg{seq: seq} }))
	}
	return m, tea.Batch(cmds...)
}

// cancelSuggest forgets the request in flight and tells the core to stop
// it; the core's answer, if it still comes, is for an id nobody waits for.
func (m model) cancelSuggest() tea.Cmd {
	c := m.cmpl
	if c.inflight == nil {
		return nil
	}
	id := c.inflight.id
	c.inflight = nil
	cl, ctx := m.cl, m.ctx
	return func() tea.Msg {
		_ = cl.CancelComplete(ctx, id)
		return nil
	}
}

// idleTick is the pause after an edit: the newest one asks, under the
// idle thresholds, and an older one is ignored.
func (m model) idleTick(msg idleTickMsg) (model, tea.Cmd) {
	c := m.cmpl
	if msg.seq != c.seq || !onIdle(c.mode()) || !m.canAsk() || c.inflight != nil || c.sugg != nil {
		return m, nil
	}
	text := m.ta.Value()
	n := len([]rune(text))
	if n < minIdleInput || time.Since(c.lastAt) < minIdleGap || n-c.lastLen < minIdleGrowth {
		return m, nil
	}
	return m.suggest(false)
}

// suggest sends the request for the input as it stands.
func (m model) suggest(byTab bool) (model, tea.Cmd) {
	c := m.cmpl
	// A Tab while a request is in flight restarts it for the text as it
	// stands now; the old answer would be for older text.
	stop := m.cancelSuggest()
	c.seq++
	text := m.ta.Value()
	req := rpc.CompleteRequest{ID: fmt.Sprintf("c%d", c.seq), SessionID: m.sessionID, Text: text}
	c.inflight = &suggestRequest{id: req.ID, forText: text, byTab: byTab}
	c.requests++
	c.lastAt, c.lastLen = time.Now(), len([]rune(text))
	if byTab {
		m.status = "completing…"
	}
	cl, ctx := m.cl, m.ctx
	ask := func() tea.Msg {
		res, err := cl.Complete(ctx, req)
		return completionMsg{id: req.ID, res: res, err: err}
	}
	warn := tea.Tick(c.warnAfter(), func(time.Time) tea.Msg { return warnTickMsg{id: req.ID} })
	return m, tea.Batch(stop, ask, warn)
}

// completed takes the core's answer, if it is the one being waited for
// and the input still reads as it did when the request was sent.
func (m model) completed(msg completionMsg) model {
	c := m.cmpl
	if c.inflight == nil || c.inflight.id != msg.id {
		return m
	}
	req := c.inflight
	c.inflight = nil
	switch {
	case msg.err != nil:
		c.failed++
		c.failures++
		if c.failures >= maxFailures {
			c.disabled = fmt.Sprintf("switched off after %d failures in a row; last: %s", c.failures, strings.TrimPrefix(msg.err.Error(), "config: "))
			m.add(entry{kind: entryNotice, text: "completion " + c.disabled + "; /completion on tries again", done: true})
			return m
		}
		m.status = "completion failed: " + strings.TrimPrefix(msg.err.Error(), "config: ")
		return m
	case msg.res.Disabled:
		c.disabled = msg.res.Reason
		if req.byTab {
			m.status = "completion is off: " + msg.res.Reason
		}
		return m
	}
	c.failures = 0
	if msg.res.Usage != nil {
		c.usage = c.usage.Add(*msg.res.Usage)
	}
	if req.forText != m.ta.Value() {
		return m
	}
	if msg.res.Suggestion == "" {
		if req.byTab {
			m.status = "no completion"
		}
		return m
	}
	c.sugg = &suggestion{forText: req.forText, text: msg.res.Suggestion}
	if m.status == "completing…" {
		m.status = ""
	}
	return m
}

// warnTick says the model is slow when the request it names is still in
// flight.
func (m model) warnTick(msg warnTickMsg) model {
	c := m.cmpl
	if c.inflight != nil && c.inflight.id == msg.id {
		m.status = fmt.Sprintf("the completion model is slow (over %s); keep typing, the answer is dropped if the text changes", c.warnAfter())
	}
	return m
}

// tabOnQuestion is Tab with a question in the input: it accepts the
// suggestion on offer, or asks for one when the mode allows.
func (m model) tabOnQuestion() (model, tea.Cmd) {
	c := m.cmpl
	text := m.ta.Value()
	if c.sugg != nil && c.sugg.forText == text {
		m.ta.SetValue(text + c.sugg.text)
		m.ta.CursorEnd()
		c.sugg = nil
		c.lastLen = len([]rune(m.ta.Value()))
		return m, nil
	}
	switch {
	case c.disabled != "":
		m.status = "completion is off: " + c.disabled
	case !onTab(c.mode()):
		if c.mode() == "off" {
			m.status = "completion is off; /completion tab turns it on for this session, completion.mode in the config for good"
		}
	case !m.canAsk():
	default:
		return m.suggest(true)
	}
	return m, nil
}

// suggestionLine is what the status line shows while a suggestion is on
// offer.
func (m model) suggestionLine() string {
	if c := m.cmpl; c.sugg != nil && c.sugg.forText == m.ta.Value() {
		return "tab accepts: " + strings.TrimSpace(c.sugg.text)
	}
	return ""
}

// completionCommand is /completion: alone it reports the state and the
// cost so far; with a mode it sets the trigger for this session.
func (m model) completionCommand(arg string) (model, tea.Cmd) {
	c := m.cmpl
	switch arg {
	case "":
	case "on":
		c.override, c.disabled, c.failures = "both", "", 0
	case "off", "tab", "idle", "both":
		c.override, c.disabled, c.failures = arg, "", 0
	default:
		m.status = "usage: /completion [off|tab|idle|both]"
		return m, nil
	}
	source := "config"
	if c.override != "" {
		source = "this session"
	}
	who := "the session's profile"
	if c.settings.Profile != "" {
		who = "profile " + c.settings.Profile
		if c.settings.Model != "" {
			who += "/" + c.settings.Model
		}
	}
	line := fmt.Sprintf("completion: %s (%s) · %s\nrequests: %d (%d failed) · tokens: %d in, %d out",
		c.mode(), source, who, c.requests, c.failed, c.usage.InputTokens, c.usage.OutputTokens)
	if c.disabled != "" {
		line += "\noff: " + c.disabled
	}
	m.add(entry{kind: entryNotice, text: line, done: true})
	return m, m.loadCompletionSettings()
}

// modelsNeeded says which profile's models the line under Tab asks for
// and the shell has not got yet: the model argument of /profile, or the
// value of a providers.<p>.model key in /config set.
func (m model) modelsNeeded(input string) string {
	cmd, prev, _, ok := m.parseLine(input)
	if !ok {
		return ""
	}
	var profile string
	switch cmd.name {
	case "/profile":
		if len(prev) == 1 {
			profile = prev[0]
		}
	case "/config":
		args := prev
		if len(args) > 0 && args[0] == "set" {
			args = args[1:]
		} else {
			return ""
		}
		if len(args) > 0 && args[0] == "--project" {
			args = args[1:]
		}
		if len(args) == 1 {
			if rest, ok := strings.CutPrefix(args[0], "providers."); ok {
				if name, field, ok := strings.Cut(rest, "."); ok && field == "model" {
					profile = name
				}
			}
		}
	}
	if profile == "" || m.cmpl.models[profile] != nil || m.cmpl.loading == profile {
		return ""
	}
	return profile
}

// loadModels asks the core what a profile serves, for Tab.
func (m model) loadModels(profile string) (model, tea.Cmd) {
	m.cmpl.loading = profile
	m.status = "loading the models of " + profile + "…"
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		models, err := cl.Models(ctx, profile)
		return modelsMsg{profile: profile, models: models, err: err}
	}
}

func (m model) modelsLoaded(msg modelsMsg) model {
	c := m.cmpl
	if c.loading == msg.profile {
		c.loading = ""
	}
	if msg.err != nil {
		c.models[msg.profile] = []string{}
		m.status = "no model list for " + msg.profile + ": " + strings.TrimPrefix(msg.err.Error(), "config: ")
		return m
	}
	c.models[msg.profile] = msg.models
	if m.status == "loading the models of "+msg.profile+"…" {
		m.status = fmt.Sprintf("%d models of %s loaded; tab completes them", len(msg.models), msg.profile)
	}
	return m
}

// modelsOf is the cached model list of a profile, for an argSpec.
func (m model) modelsOf(profile string) []string {
	return m.cmpl.models[profile]
}

// cancelOnSubmit drops the completion state when a question is sent: the
// answer will change what comes next anyway.
func (m model) cancelOnSubmit() tea.Cmd {
	m.cmpl.sugg = nil
	return m.cancelSuggest()
}
