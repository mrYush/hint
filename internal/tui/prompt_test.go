package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

func promptFixture() []rpc.PromptInfo {
	return []rpc.PromptInfo{{
		Server: "docs", Name: "review", Description: "Review a file.",
		Arguments: []rpc.PromptArgument{{Name: "path", Description: "File to review.", Required: true}},
	}}
}

func TestPromptListAndExpansionRunAsUserTurn(t *testing.T) {
	eng := &fakeEngine{prompts: promptFixture()}
	h := newHarness(t, eng, Options{})
	h.run(h.submit("/prompts"))
	if got := h.m.entries[len(h.m.entries)-1].text; !strings.Contains(got, "/docs:review") || !strings.Contains(got, "path (required)") {
		t.Errorf("prompt list = %q", got)
	}

	h.run(h.submit("/docs:review main.go"))
	h.pump(until[turnDoneMsg])
	if eng.promptRequest.Server != "docs" || eng.promptRequest.Name != "review" || len(eng.promptRequest.Arguments) != 1 || eng.promptRequest.Arguments[0] != "main.go" {
		t.Errorf("prompt request = %+v", eng.promptRequest)
	}
	messages := eng.sessions["s1"].msgs
	if len(messages) == 0 || messages[0].Role != agentapi.RoleUser || messages[0].Text() != "Review main.go" {
		t.Errorf("expanded turn messages = %+v", messages)
	}
}

func TestPromptArgumentCompletion(t *testing.T) {
	eng := &fakeEngine{prompts: promptFixture()}
	h := newHarness(t, eng, Options{})
	h.typeText("/docs:review ma")
	list := h.press("tab")
	if list == nil {
		t.Fatal("Tab did not request the prompt list")
	}
	completion := h.update(list())
	if completion == nil {
		t.Fatal("loading the list did not continue to argument completion")
	}
	h.run(completion)
	if got := h.m.ta.Value(); got != "/docs:review main.go" {
		t.Errorf("completed input = %q", got)
	}
	if eng.promptComplete.Argument != "path" || eng.promptComplete.Value != "ma" || eng.promptComplete.Prompt != "review" {
		t.Errorf("completion request = %+v", eng.promptComplete)
	}
}

func TestPromptExpansionCancelAndQuit(t *testing.T) {
	for _, action := range []string{"ctrl+c", "ctrl+d", "/exit"} {
		t.Run(action, func(t *testing.T) {
			eng := &fakeEngine{prompts: promptFixture()}
			h := newHarness(t, eng, Options{})
			expansion := h.submit("/docs:review main.go")
			if !h.m.busy || h.m.cancelTurn == nil {
				t.Fatal("prompt expansion has no cancellation")
			}
			if action == "/exit" {
				h.submit(action)
			} else {
				h.press(action)
			}
			msg := expansion().(promptExpansionMsg)
			next, cmd := h.m.promptExpanded(msg)
			h.m = next
			if cmd != nil {
				t.Error("a cancelled or quitting expansion started another command")
			}
			if len(eng.sessions["s1"].msgs) != 0 {
				t.Errorf("cancelled expansion started a turn: %+v", eng.sessions["s1"].msgs)
			}
		})
	}
}

func TestPromptServersMethodNotFoundLeavesTabWorking(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.typeText("/he")
	h.m.promptServersLoaded = false
	h.m.status = readingPromptServers
	next, cmd := h.m.promptServersReady(promptServersMsg{
		err:   &rpc.Error{Code: rpc.CodeMethodNotFound, Message: `method "prompt.servers" not found`},
		input: "/he",
	})
	h.m = next
	if cmd != nil {
		t.Fatal("method not found started another request")
	}
	if !h.m.promptServersLoaded {
		t.Fatal("method not found left server names unloaded")
	}
	if strings.Contains(h.m.status, "could not read") {
		t.Errorf("status = %q", h.m.status)
	}
	if got := h.m.ta.Value(); got != "/help" {
		t.Errorf("completed input = %q", got)
	}
}

func TestPromptServersOtherErrorIsRemembered(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.m.promptServersLoaded = false
	h.m.status = readingPromptServers
	next, cmd := h.m.promptServersReady(promptServersMsg{err: errors.New("broken")})
	h.m = next
	if cmd != nil {
		t.Fatal(cmd)
	}
	if !h.m.promptServersLoaded {
		t.Fatal("an error left server names unloaded")
	}
	if !strings.Contains(h.m.status, "could not read MCP server names: broken") {
		t.Errorf("status = %q", h.m.status)
	}
	h.typeText("/he")
	if cmd := h.press("tab"); cmd != nil {
		t.Fatal("Tab asked the core again")
	}
	if got := h.m.ta.Value(); got != "/help" {
		t.Errorf("completed input = %q", got)
	}
}

func TestPromptCompletionReportsFurtherValues(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.m.prompts, h.m.promptsLoaded = promptFixture(), true
	h.typeText("/docs:review ma")
	next := h.m.promptCompleted(promptCompletionMsg{
		input: h.m.ta.Value(), values: []string{"main.go", "model.go"}, hasMore: true,
	})
	if next.status != "more prompt values exist" {
		t.Errorf("status = %q", next.status)
	}
}

func TestPromptCompletionHasMoreDoesNotQualifyNoMatch(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.m.prompts, h.m.promptsLoaded = promptFixture(), true
	h.typeText("/docs:review zz")
	next := h.m.promptCompleted(promptCompletionMsg{
		input: h.m.ta.Value(), values: []string{"main.go"}, hasMore: true,
	})
	if next.status != "no prompt argument matches zz" {
		t.Errorf("status = %q", next.status)
	}
}

func TestPromptServersReadyKeepsOtherStatus(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.m.status = "the turn failed"
	next, _ := h.m.promptServersReady(promptServersMsg{servers: []string{"docs"}})
	if next.status != "the turn failed" {
		t.Errorf("status = %q", next.status)
	}
	h.m.status = readingPromptServers
	next, _ = h.m.promptServersReady(promptServersMsg{})
	if next.status != "" {
		t.Errorf("own status left behind: %q", next.status)
	}
}

func TestPromptBusyWithoutCancelDoesNotPanic(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.m.busy = true
	h.press("ctrl+c")
	h.m.busy = true
	h.press("ctrl+d")
}

func TestPromptCommandCompletionWithoutColon(t *testing.T) {
	eng := &fakeEngine{prompts: promptFixture()}
	h := newHarness(t, eng, Options{})
	h.typeText("/docs")
	list := h.press("tab")
	if list == nil {
		t.Fatal("a matching server prefix did not request prompts")
	}
	h.update(list())
	if got := h.m.ta.Value(); got != "/docs:review " {
		t.Errorf("completed command = %q", got)
	}
}

func TestPromptCompletionSendsPreviousArguments(t *testing.T) {
	prompts := promptFixture()
	prompts[0].Arguments = append(prompts[0].Arguments, rpc.PromptArgument{Name: "focus"})
	eng := &fakeEngine{prompts: prompts}
	h := newHarness(t, eng, Options{})
	h.typeText(`/docs:review "the auth flow" `)
	list := h.press("tab")
	if list == nil {
		t.Fatal("Tab did not request the prompt list")
	}
	completion := h.update(list())
	if completion == nil {
		t.Fatal("Tab did not request argument completion")
	}
	h.run(completion)
	if eng.promptComplete.Argument != "focus" || eng.promptComplete.Arguments["path"] != "the auth flow" {
		t.Errorf("completion request = %+v", eng.promptComplete)
	}
}

func TestPromptCompletionQuotesAndClearsStatus(t *testing.T) {
	h := newHarness(t, &fakeEngine{prompts: promptFixture()}, Options{})
	h.m.prompts = promptFixture()
	h.m.promptsLoaded = true
	h.m.ta.SetValue("/docs:review main")
	h.m.status = "completing the prompt argument…"
	h.m = h.m.promptCompleted(promptCompletionMsg{input: "/docs:review main", values: []string{"main file.go"}})
	if got := h.m.ta.Value(); got != `/docs:review main\ file.go` {
		t.Errorf("completed input = %q", got)
	}
	if h.m.status != "" {
		t.Errorf("status = %q", h.m.status)
	}
}

func TestPromptListSuccessReplacesLoadingStatus(t *testing.T) {
	h := newHarness(t, &fakeEngine{prompts: promptFixture()}, Options{})
	h.run(h.submit("/prompts"))
	if strings.Contains(h.m.status, "loading") || !strings.Contains(h.m.status, "1 MCP prompt loaded") {
		t.Errorf("status = %q", h.m.status)
	}
}

func TestPromptExpansionAcceptsQuotedArguments(t *testing.T) {
	eng := &fakeEngine{prompts: promptFixture()}
	h := newHarness(t, eng, Options{})
	h.m.prompts = promptFixture()
	h.m.promptsLoaded = true
	h.run(h.submit(`/docs:review "the auth flow"`))
	h.pump(until[turnDoneMsg])
	if len(eng.promptRequest.Arguments) != 1 || eng.promptRequest.Arguments[0] != "the auth flow" {
		t.Errorf("prompt request = %+v", eng.promptRequest)
	}
}

func TestPromptCommandCompletionWithStaticPrefix(t *testing.T) {
	eng := &fakeEngine{prompts: []rpc.PromptInfo{{Server: "s", Name: "review"}}}
	h := newHarness(t, eng, Options{})
	h.typeText("/s")
	list := h.press("tab")
	if list == nil {
		t.Fatal("a server sharing a static-command prefix did not request prompts")
	}
	h.update(list())
	if eng.promptListCalls != 1 || !strings.Contains(h.m.status, "/s:review") {
		t.Errorf("prompt calls = %d, status = %q", eng.promptListCalls, h.m.status)
	}
}

func TestTabUnknownCommandDoesNotConnectMCP(t *testing.T) {
	for _, input := range []string{"/hlep", "/tmp/x"} {
		t.Run(input, func(t *testing.T) {
			eng := &fakeEngine{prompts: promptFixture()}
			h := newHarness(t, eng, Options{})
			h.typeText(input)
			if cmd := h.press("tab"); cmd != nil {
				t.Error("an unknown command requested MCP prompts")
			}
			if eng.promptListCalls != 0 {
				t.Errorf("prompt.list calls = %d", eng.promptListCalls)
			}
			if !strings.Contains(h.m.status, "no command matches") {
				t.Errorf("status = %q", h.m.status)
			}
		})
	}
}
