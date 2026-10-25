package session_test

import (
	"errors"
	"testing"

	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// twoTurns is a conversation of two user turns, the first with a tool
// round in it, so that a cut at turn 1 has to keep the tool messages the
// client never counted.
func twoTurns() []agentapi.Message {
	call := agentapi.ToolCall{ID: "c1", Name: "read_file", Arguments: []byte(`{"path":"a.go"}`)}
	return []agentapi.Message{
		agentapi.UserMessage("what is in a.go?"),
		{Role: agentapi.RoleAssistant, ToolCalls: []agentapi.ToolCall{call}},
		{Role: agentapi.RoleTool, ToolCallID: "c1", Content: []agentapi.ContentPart{agentapi.Text("package a")}},
		agentapi.AssistantMessage("a package clause"),
		agentapi.UserMessage("and b.go?"),
		agentapi.AssistantMessage("nothing yet"),
	}
}

func TestTurnsAndUpToTurn(t *testing.T) {
	msgs := twoTurns()
	if got := session.Turns(msgs); got != 2 {
		t.Fatalf("Turns = %d, want 2", got)
	}
	cut, err := session.UpToTurn(msgs, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(cut) != 4 || cut[3].Text() != "a package clause" {
		t.Fatalf("turn 1 = %d messages ending %q, want 4 ending with the answer", len(cut), cut[len(cut)-1].Text())
	}
	whole, err := session.UpToTurn(msgs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != len(msgs) {
		t.Fatalf("turn 2 = %d messages, want all %d", len(whole), len(msgs))
	}
	whole[0] = agentapi.UserMessage("changed")
	if msgs[0].Text() == "changed" {
		t.Fatal("UpToTurn must return a copy")
	}
	for _, n := range []int{0, 3, -1} {
		if _, err := session.UpToTurn(msgs, n); !errors.Is(err, session.ErrNoSuchTurn) {
			t.Errorf("UpToTurn(%d) = %v, want ErrNoSuchTurn", n, err)
		}
	}
}

func TestForkWritesAStandaloneSession(t *testing.T) {
	store := newStore(t, "src1", "fork")
	src, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range twoTurns() {
		if err := src.AppendMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := src.AppendProfile(session.ProfileRef{Name: "local", Model: "qwen"}); err != nil {
		t.Fatal(err)
	}
	cut, err := session.UpToTurn(src.Messages(), 1)
	if err != nil {
		t.Fatal(err)
	}
	fork, err := store.Fork("/p", session.Parent{ID: src.ID(), Turn: 1}, cut, src.Profile())
	if err != nil {
		t.Fatal(err)
	}
	if err := fork.Close(); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := store.Open(fork.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if reopened.ID() != "fork" {
		t.Errorf("fork id = %q", reopened.ID())
	}
	parent, ok := reopened.Parent()
	if !ok || parent != (session.Parent{ID: "src1", Turn: 1}) {
		t.Errorf("parent = %+v, %v; want src1@1", parent, ok)
	}
	if got := reopened.Profile(); got != (session.ProfileRef{Name: "local", Model: "qwen"}) {
		t.Errorf("profile = %+v, want the source's", got)
	}
	if got := reopened.Messages(); len(got) != 4 || session.Turns(got) != 1 {
		t.Errorf("fork holds %d messages / %d turns, want 4 / 1", len(got), session.Turns(got))
	}
	if w := reopened.Warnings(); len(w) != 0 {
		t.Errorf("warnings = %v", w)
	}

	infos, err := store.List("/p")
	if err != nil {
		t.Fatal(err)
	}
	var forkInfo *session.Info
	for i := range infos {
		if infos[i].ID == "fork" {
			forkInfo = &infos[i]
		}
	}
	if forkInfo == nil || forkInfo.Parent == nil || forkInfo.Parent.ID != "src1" {
		t.Fatalf("List does not carry the fork's parent: %+v", infos)
	}
	if forkInfo.FirstPrompt != "what is in a.go?" {
		t.Errorf("fork first prompt = %q", forkInfo.FirstPrompt)
	}
	if _, err := store.Fork("/p", session.Parent{}, nil, session.ProfileRef{}); err == nil {
		t.Error("Fork without a parent id must fail")
	}
}

func TestProfileRecordIsOptionalAndLast(t *testing.T) {
	store := newStore(t, "s1")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	if got := sess.Profile(); got != (session.ProfileRef{}) {
		t.Fatalf("new session profile = %+v, want none", got)
	}
	if err := sess.AppendProfile(session.ProfileRef{}); err == nil {
		t.Error("AppendProfile without a name must fail")
	}
	for _, name := range []string{"api-bar", "local"} {
		if err := sess.AppendProfile(session.ProfileRef{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := sess.AppendMessage(agentapi.UserMessage("hi")); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if got := reopened.Profile(); got.Name != "local" {
		t.Errorf("profile after reload = %+v, want the last one, local", got)
	}
	if n := len(reopened.Messages()); n != 1 {
		t.Errorf("profile records must not count as messages: %d", n)
	}
	rec := session.Record{Kind: session.KindProfile}
	if err := rec.Validate(); err == nil {
		t.Error("a profile record without a payload must not validate")
	}
	header := session.Record{Kind: session.KindSession, Version: 1, ID: "x", Parent: &session.Parent{Turn: 1}}
	if err := header.Validate(); err == nil {
		t.Error("a parent without an id must not validate")
	}
}
