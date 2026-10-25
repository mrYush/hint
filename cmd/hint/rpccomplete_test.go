package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/rpc"
)

func TestCompleteAsksTheSessionProviderWithoutTools(t *testing.T) {
	chat := &fakeChat{reply: func(n int) string {
		if n == 1 {
			return "the answer"
		}
		return "\"the parser\"\nsecond line"
	}}
	as, _, _, _ := testAssembly(t, chat, "")
	as.cfg.Completion = config.CompletionSettings{Mode: config.CompletionTab, IdleAfter: config.DefaultCompletionIdleAfter, WarnAfter: config.DefaultCompletionWarnAfter}
	_, cl := testEngine(t, as)
	ctx := context.Background()
	view, err := cl.OpenSession(ctx, rpc.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	drive(t, cl, view.ID, "what does main.go do?")

	res, err := cl.Complete(ctx, rpc.CompleteRequest{ID: "c1", SessionID: view.ID, Text: "and where is"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if res.Suggestion != " the parser" || res.Usage == nil || res.Usage.InputTokens != 10 {
		t.Errorf("result = %+v; want the first line, unquoted, with a leading space and the usage", res)
	}
	reqs := chat.seen()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want the turn and the completion", len(reqs))
	}
	req := reqs[1]
	if len(req.Tools) != 0 || req.MaxTokens != completionMaxTokens || req.Temperature == nil {
		t.Errorf("completion request has %d tools, max_tokens %d, temperature %v", len(req.Tools), req.MaxTokens, req.Temperature)
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != "system" || !strings.Contains(req.Messages[1].Text(), "what does main.go do?") || !strings.Contains(req.Messages[1].Text(), "and where is") {
		t.Errorf("completion messages = %+v; want the instruction, the last question and the text", req.Messages)
	}
	// Nothing of it reached the session.
	again, err := cl.AttachSession(ctx, view.ID)
	if err != nil || len(again.Messages) != 2 {
		t.Errorf("session has %d messages after a completion, want 2", len(again.Messages))
	}

	// Off in the configuration is the client's trigger, not a refusal: a
	// session that turned completion on with /completion is answered. A
	// completion profile that cannot be used is a refusal, not an error.
	as.cfg.Completion.Mode = config.CompletionOff
	_, cl2 := testEngine(t, as)
	if res, err := cl2.Complete(ctx, rpc.CompleteRequest{ID: "c2", Text: "x"}); err != nil || res.Disabled {
		t.Errorf("off = %+v, %v; want an answer", res, err)
	}
	as.cfg.Completion.Profile = "ghost"
	_, cl2 = testEngine(t, as)
	if res, err := cl2.Complete(ctx, rpc.CompleteRequest{ID: "c2", Text: "x"}); err != nil || !res.Disabled || !strings.Contains(res.Reason, "ghost") {
		t.Errorf("unusable completion profile = %+v, %v; want disabled naming it", res, err)
	}
	as.cfg.Completion.Profile = ""
	as.cfg.Completion.Mode = config.CompletionIdle
	_, cl3 := testEngine(t, as)
	var rpcErr *rpc.Error
	if _, err := cl3.Complete(ctx, rpc.CompleteRequest{ID: "c3", SessionID: "ghost", Text: "x"}); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Errorf("unknown session = %v; want code %d", err, rpc.CodeNotFound)
	}
	// Without a session the default profile answers.
	if res, err := cl3.Complete(ctx, rpc.CompleteRequest{ID: "c4", Text: "no session"}); err != nil || res.Disabled {
		t.Errorf("sessionless completion = %+v, %v", res, err)
	}
}

func TestCompleteUsesTheConfiguredProfile(t *testing.T) {
	main := &fakeChat{reply: func(int) string { return "from main" }}
	small := &fakeChat{reply: func(int) string { return "from small" }}
	as, _, _, _ := testAssembly(t, main, "")
	as.cfg = twoProfiles()
	as.cfg.Completion = config.CompletionSettings{Mode: config.CompletionBoth, Profile: "other", Model: "tiny", IdleAfter: config.DefaultCompletionIdleAfter, WarnAfter: config.DefaultCompletionWarnAfter}
	eng, cl := testEngine(t, as)
	eng.providers[rpc.ProfileRef{Profile: "other", Model: "tiny"}] = small
	ctx := context.Background()
	view, err := cl.OpenSession(ctx, rpc.OpenOptions{Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	if res, err := cl.Complete(ctx, rpc.CompleteRequest{ID: "c1", SessionID: view.ID, Text: "hello"}); err != nil || res.Suggestion != " from small" {
		t.Errorf("Complete = %+v, %v; want the small model's answer", res, err)
	}
	if len(main.seen()) != 0 || len(small.seen()) != 1 {
		t.Errorf("requests: main %d, small %d", len(main.seen()), len(small.seen()))
	}
	settings, err := cl.CompletionSettings(ctx)
	if err != nil || settings.Mode != "both" || settings.Profile != "other" || settings.Model != "tiny" || settings.IdleAfterMs != 600 || settings.WarnAfterMs != 2000 {
		t.Errorf("CompletionSettings = %+v, %v", settings, err)
	}
}

func TestModelsListsWhatTheProfileServes(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{reply: func(int) string { return "x" }}, "")
	as.cfg = twoProfiles()
	eng, cl := testEngine(t, as)
	var asked config.Profile
	eng.listModels = func(_ context.Context, p config.Profile) ([]provider.Model, error) {
		asked = p
		return []provider.Model{{Name: "a"}, {Name: "b"}}, nil
	}
	ctx := context.Background()
	models, err := cl.Models(ctx, "other")
	if err != nil || len(models) != 2 || models[1] != "b" || asked.Name != "other" {
		t.Fatalf("Models = %v, %v (asked %s)", models, err, asked.Name)
	}
	var rpcErr *rpc.Error
	if _, err := cl.Models(ctx, "nope"); !errors.As(err, &rpcErr) || rpcErr.Code != rpc.CodeNotFound {
		t.Errorf("unknown profile = %v; want code %d", err, rpc.CodeNotFound)
	}
}

func TestTrimSuggestion(t *testing.T) {
	cases := []struct{ typed, raw, want string }{
		{"add a test for", " the parser", " the parser"},
		{"add a test for", "the parser\nand more", " the parser"},
		{"add a test for", "\"the parser\"", " the parser"},
		{"add a test for", "add a test for the parser", " the parser"},
		{"add a test for ", "the parser", "the parser"},
		{"what is", "?", "?"},
		{"x", "   ", ""},
		{"x", "", ""},
		{"count", "one two three four five six seven eight nine ten eleven twelve thirteen", " one two three four five six seven eight nine ten eleven twelve"},
		// A repeat in another case is cut too, in the model's own bytes:
		// lowering İ or the Kelvin sign makes them shorter and Ⱥ longer,
		// so the length of a lowered copy is no index into the original.
		{"go to", "Go to İzmir", " İzmir"},
		{"go", "GO İİİİ", " İİİİ"},
		{"go", "GO ȺȺȺ", " ȺȺȺ"},
		{"kelvin", "KELVIN \u212a", " \u212a"},
		{"добавь тест", "Добавь Тест для парсера", " для парсера"},
	}
	for _, c := range cases {
		if got := trimSuggestion(c.typed, c.raw); got != c.want {
			t.Errorf("trimSuggestion(%q, %q) = %q, want %q", c.typed, c.raw, got, c.want)
		}
	}
}

// FuzzTrimSuggestion: whatever a model answers, trimming it neither
// panics — it runs on the server's goroutine, where a panic ends the
// process — nor hands the shell broken text or more than one line.
func FuzzTrimSuggestion(f *testing.F) {
	f.Add("add a test for", "add a test for the parser")
	f.Add("go", "GO İİİİ")
	f.Add("go", "GO ȺȺȺ")
	f.Add("kelvin", "KELVIN \u212a")
	f.Add("İ", "i\u0307x")
	f.Fuzz(func(t *testing.T, typed, raw string) {
		got := trimSuggestion(typed, raw)
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("trimSuggestion(%q, %q) = %q; want one line", typed, raw, got)
		}
		if utf8.ValidString(typed) && utf8.ValidString(raw) && !utf8.ValidString(got) {
			t.Errorf("trimSuggestion(%q, %q) = %q; want valid UTF-8", typed, raw, got)
		}
	})
}
