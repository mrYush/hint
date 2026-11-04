package tui

import (
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
