package tui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/internal/shell"
)

type promptListMsg struct {
	prompts []rpc.PromptInfo
	err     error
	show    bool
	input   string
}

type promptCompletionMsg struct {
	input   string
	values  []string
	hasMore bool
	err     error
}

type promptExpansionMsg struct {
	ctx  context.Context
	text string
	err  error
}

type promptServersMsg struct {
	servers []string
	err     error
	input   string
}

func (m model) loadPromptServers(input string) (model, tea.Cmd) {
	m.promptServersLoading = true
	m.status = readingPromptServers
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		servers, err := cl.PromptServers(ctx)
		return promptServersMsg{servers: servers, err: err, input: input}
	}
}

func (m model) promptServersReady(msg promptServersMsg) (model, tea.Cmd) {
	m.promptServersLoading = false
	if msg.err != nil {
		// An older core has no prompt.servers. Any error, including a
		// transient one, is remembered until restart: retrying on every
		// Tab would never finish the line. /prompts still lists prompts.
		m.promptServers, m.promptServersLoaded = nil, true
		var rpcErr *rpc.Error
		if errors.As(msg.err, &rpcErr) && rpcErr.Code == rpc.CodeMethodNotFound {
			if m.status == readingPromptServers {
				m.status = ""
			}
		} else if m.status == "" || m.status == readingPromptServers {
			m.status = "could not read MCP server names: " + msg.err.Error()
		}
		if msg.input != "" && m.ta.Value() == msg.input {
			return m.tab()
		}
		return m, nil
	}
	m.promptServers = msg.servers
	m.promptServersLoaded = true
	if m.status == readingPromptServers {
		m.status = ""
	}
	if msg.input == "" || m.ta.Value() != msg.input {
		return m, nil
	}
	first := firstCommandWord(msg.input)
	if strings.Contains(first, ":") || m.promptServerMatch(first) {
		return m.loadPrompts(false, msg.input)
	}
	return m.tab()
}

const readingPromptServers = "reading MCP server configuration…"

func (m model) promptServerMatch(first string) bool {
	prefix := strings.TrimPrefix(first, "/")
	if prefix == "" || strings.Contains(prefix, ":") {
		return false
	}
	for _, server := range m.promptServers {
		if strings.HasPrefix(server, prefix) {
			return true
		}
	}
	return false
}

// loadPrompts discovers prompts only when a list or command completion asks.
func (m model) loadPrompts(show bool, input string) (model, tea.Cmd) {
	m.promptsLoading = true
	m.status = "loading MCP prompts…"
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		prompts, err := cl.Prompts(ctx)
		return promptListMsg{prompts: prompts, err: err, show: show, input: input}
	}
}

func (m model) promptListLoaded(msg promptListMsg) (model, tea.Cmd) {
	m.promptsLoading = false
	if msg.err != nil {
		m.status = "could not list MCP prompts: " + msg.err.Error()
		return m, nil
	}
	m.prompts, m.promptsLoaded = msg.prompts, true
	if len(msg.prompts) == 1 {
		m.status = "1 MCP prompt loaded; tab completes it"
	} else {
		m.status = fmt.Sprintf("%d MCP prompts loaded; tab completes them", len(msg.prompts))
	}
	if msg.show {
		m.add(entry{kind: entryNotice, text: formatPromptList(msg.prompts), done: true})
	}
	if msg.input != "" && m.ta.Value() == msg.input {
		return m.tab()
	}
	return m, nil
}

func formatPromptList(prompts []rpc.PromptInfo) string {
	if len(prompts) == 0 {
		return "No MCP prompts are available."
	}
	var b strings.Builder
	b.WriteString("MCP prompts:")
	for _, prompt := range prompts {
		fmt.Fprintf(&b, "\n%s", prompt.Command())
		if prompt.Description != "" {
			fmt.Fprintf(&b, " — %s", prompt.Description)
		}
		if len(prompt.Arguments) > 0 {
			args := make([]string, len(prompt.Arguments))
			for i, arg := range prompt.Arguments {
				args[i] = arg.Name
				if arg.Required {
					args[i] += " (required)"
				}
			}
			fmt.Fprintf(&b, "\n  arguments: %s", strings.Join(args, ", "))
		}
	}
	return b.String()
}

func (m model) promptCommands() []command {
	out := make([]command, 0, len(m.prompts))
	for _, prompt := range m.prompts {
		prompt := prompt
		out = append(out, command{
			name: prompt.Command(), help: prompt.Description,
			next: func(_ model, prev []string) argSpec {
				if len(prev) >= len(prompt.Arguments) {
					return argSpec{}
				}
				return argSpec{expected: true}
			},
		})
	}
	return out
}

// promptCompletionNeeded identifies the current argument of a dynamic prompt
// command. Argument names and order come from prompts/list; values come from
// completion/complete.
func (m model) promptCompletionNeeded(input string) (rpc.PromptCompleteRequest, bool) {
	cmd, prev, partial, ok := m.parseLine(input)
	if !ok {
		return rpc.PromptCompleteRequest{}, false
	}
	server, prompt, ok := rpc.ParsePromptCommand(cmd.name)
	if !ok {
		return rpc.PromptCompleteRequest{}, false
	}
	for _, info := range m.prompts {
		if info.Server != server || info.Name != prompt || len(prev) >= len(info.Arguments) {
			continue
		}
		arg := info.Arguments[len(prev)]
		resolved := make(map[string]string, len(prev))
		for i, value := range prev {
			resolved[info.Arguments[i].Name] = value
		}
		return rpc.PromptCompleteRequest{Server: server, Prompt: prompt, Argument: arg.Name, Value: partial, Arguments: resolved}, true
	}
	return rpc.PromptCompleteRequest{}, false
}

func (m model) loadPromptCompletion(req rpc.PromptCompleteRequest, input string) (model, tea.Cmd) {
	m.promptValuesLoading = true
	m.status = "completing the prompt argument…"
	cl, ctx := m.cl, m.ctx
	return m, func() tea.Msg {
		values, hasMore, err := cl.CompletePrompt(ctx, req)
		return promptCompletionMsg{input: input, values: values, hasMore: hasMore, err: err}
	}
}

func (m model) promptCompleted(msg promptCompletionMsg) model {
	m.promptValuesLoading = false
	if msg.err != nil {
		m.status = "prompt argument completion failed: " + msg.err.Error()
		return m
	}
	if msg.input != m.ta.Value() {
		return m
	}
	cmd, prev, partial, ok := m.parseLine(msg.input)
	if !ok {
		return m
	}
	head := commandHead(cmd.name, prev)
	var candidates []string
	for _, value := range msg.values {
		if strings.HasPrefix(value, partial) {
			candidates = append(candidates, head+shell.QuoteWord(value))
		}
	}
	switch len(candidates) {
	case 0:
		m.status = "no prompt argument matches " + partial
	case 1:
		m.status = ""
		m.ta.SetValue(candidates[0])
		if m.wantsMore(candidates[0]) {
			m.ta.SetValue(candidates[0] + " ")
		}
	default:
		sort.Strings(candidates)
		if prefix := commonPrefix(candidates); len(prefix) > len(msg.input) {
			m.ta.SetValue(prefix)
		}
		m.comp = &completion{cands: candidates, shown: m.ta.Value(), idx: -1}
		m.status = strings.Join(candidates, "  ")
	}
	// A server that has more values but none matching this prefix is
	// still a miss; "more values exist" would contradict that status.
	if msg.hasMore && len(candidates) > 0 {
		if m.status != "" {
			m.status += " (more values exist)"
		} else {
			m.status = "more prompt values exist"
		}
	}
	return m
}

func (m model) expandPrompt(server, prompt string, args []string) (model, tea.Cmd) {
	m.busy = true
	m.status = "expanding " + server + ":" + prompt + "…"
	cl := m.cl
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancelTurn = cancel
	return m, func() tea.Msg {
		text, err := cl.ExpandPrompt(ctx, rpc.PromptRequest{Server: server, Name: prompt, Arguments: args})
		return promptExpansionMsg{ctx: ctx, text: text, err: err}
	}
}

func (m model) promptExpanded(msg promptExpansionMsg) (model, tea.Cmd) {
	m.busy = false
	cancelled := msg.ctx != nil && msg.ctx.Err() != nil
	if m.cancelTurn != nil {
		m.cancelTurn()
		m.cancelTurn = nil
	}
	if m.quitting || cancelled {
		if !m.quitting {
			m.status = "prompt expansion stopped"
		}
		return m, nil
	}
	if msg.err != nil {
		m.status = "prompt expansion failed: " + msg.err.Error()
		return m, nil
	}
	if strings.TrimSpace(msg.text) == "" {
		m.status = "the MCP prompt returned no text"
		return m, nil
	}
	return m.submitText(msg.text)
}
