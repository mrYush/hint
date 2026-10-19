package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/provider"
	"github.com/mrYush/hint/internal/rpc"
	"github.com/mrYush/hint/pkg/agentapi"
)

// The completion half of the engine (WP1.6): a short continuation of what
// the user is typing, from the session's own provider unless the
// configuration names another. It is a plain chat request without tools
// that never touches the session file.

// Bounds of one completion request: what goes to the model and what may
// come back. The text is the user's own words, so the cut is from the
// front of a long question, keeping what the cursor is at.
const (
	completionMaxText     = 1000
	completionMaxQuestion = 500
	completionMaxTokens   = 24
	completionMaxWords    = 12
)

var completionTemperature = 0.2

// completionSystem is the instruction the model gets. It asks for the
// continuation alone: the shell appends what comes back to the input.
const completionSystem = "You complete what a developer is typing to a coding assistant. " +
	"Reply with the continuation only: the next few words that would follow the text, " +
	"on one line, at most twelve words, without quotes, without repeating the text, without explanation. " +
	"If nothing sensible follows, reply with nothing."

// Complete asks the completion provider for a continuation of req.Text.
func (e *rpcEngine) Complete(ctx context.Context, req rpc.CompleteRequest) (rpc.CompleteResult, error) {
	e.cfgMu.Lock()
	settings := e.cfg.Completion
	e.cfgMu.Unlock()
	if settings.Mode == config.CompletionOff {
		return rpc.CompleteResult{Disabled: true, Reason: "completion.mode is off; set it to tab, idle or both"}, nil
	}
	chat, question, err := e.completionSource(req.SessionID, settings)
	if err != nil {
		return rpc.CompleteResult{}, err
	}
	text := tail(req.Text, completionMaxText)
	var b strings.Builder
	if question != "" {
		fmt.Fprintf(&b, "The previous question in this conversation:\n%s\n\n", tail(question, completionMaxQuestion))
	}
	fmt.Fprintf(&b, "The text typed so far:\n%s", text)
	events, err := chat.Stream(ctx, agentapi.ChatRequest{
		Messages: []agentapi.Message{
			{Role: agentapi.RoleSystem, Content: []agentapi.ContentPart{agentapi.Text(completionSystem)}},
			agentapi.UserMessage(b.String()),
		},
		Temperature: &completionTemperature,
		MaxTokens:   completionMaxTokens,
	})
	if err != nil {
		return rpc.CompleteResult{}, err
	}
	var out strings.Builder
	var usage *agentapi.Usage
	for ev := range events {
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			out.WriteString(ev.Text)
		case agentapi.ChatUsage:
			usage = ev.Usage
		case agentapi.ChatError:
			if ev.Err != nil {
				return rpc.CompleteResult{}, ev.Err
			}
			return rpc.CompleteResult{}, errors.New("the completion stream failed")
		case agentapi.ChatThinkingDelta, agentapi.ChatToolCall, agentapi.ChatDone:
		}
	}
	suggestion := trimSuggestion(text, out.String())
	e.tracef("complete %s: %d chars -> %q", req.ID, len(req.Text), suggestion)
	return rpc.CompleteResult{Suggestion: suggestion, Usage: usage}, nil
}

// completionSource picks the provider a completion goes to — the
// configured completion profile, else the session's own with the
// configured model override — and the last question of the session for
// context.
func (e *rpcEngine) completionSource(sessionID string, settings config.CompletionSettings) (agentapi.ChatProvider, string, error) {
	var live *rpcSession
	if sessionID != "" {
		e.mu.Lock()
		live = e.live[sessionID]
		e.mu.Unlock()
		if live == nil {
			return nil, "", fmt.Errorf("%w: session %q is not open on this server", rpc.ErrNotFound, sessionID)
		}
	}
	question := ""
	if live != nil {
		for _, m := range live.rec.Messages() {
			if m.Role == agentapi.RoleUser {
				question = m.Text()
			}
		}
	}
	ref := rpc.ProfileRef{Profile: settings.Profile, Model: settings.Model}
	if ref.Profile == "" {
		if live != nil {
			ref.Profile = live.Asked().Profile
			if ref.Model == "" {
				ref.Model = live.Asked().Model
			}
		} else {
			ref.Profile = e.defaultRef().Profile
		}
	}
	chat, _, err := e.providerFor(ref, false)
	if err != nil {
		return nil, "", err
	}
	return chat, question, nil
}

// CompletionSettings reads the completion block as the files stand now.
func (e *rpcEngine) CompletionSettings(context.Context) (rpc.CompletionSettings, error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if err := e.refresh(); err != nil {
		return rpc.CompletionSettings{}, err
	}
	c := e.cfg.Completion
	return rpc.CompletionSettings{
		Mode:        string(c.Mode),
		Profile:     c.Profile,
		Model:       c.Model,
		IdleAfterMs: int(c.IdleAfter.Milliseconds()),
		WarnAfterMs: int(c.WarnAfter.Milliseconds()),
	}, nil
}

// Models lists what the profile's endpoint serves, by name.
func (e *rpcEngine) Models(ctx context.Context, name string) ([]string, error) {
	e.cfgMu.Lock()
	p, err := e.cfg.Profile(name)
	e.cfgMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", rpc.ErrNotFound, strings.TrimPrefix(err.Error(), "config: "))
	}
	models, err := e.models(ctx, p)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		names = append(names, m.Name)
	}
	return names, nil
}

// tail keeps the last n runes of s.
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

// trimSuggestion turns what the model wrote into text the shell can
// append to typed: the first line only, quotes and fences off, a repeat
// of the typed text removed, at most completionMaxWords words, and a
// space at the seam when both sides are word characters.
func trimSuggestion(typed, raw string) string {
	s := raw
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`\"'“”‘’")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// A model that repeats the prompt: keep only what follows it.
	if t := strings.TrimSpace(typed); t != "" {
		if rest, ok := strings.CutPrefix(s, t); ok {
			s = strings.TrimLeft(rest, " ")
		} else if rest, ok := strings.CutPrefix(strings.ToLower(s), strings.ToLower(t)); ok {
			s = strings.TrimLeft(s[len(s)-len(rest):], " ")
		}
	}
	if s == "" {
		return ""
	}
	words := strings.Fields(s)
	if len(words) > completionMaxWords {
		words = words[:completionMaxWords]
	}
	s = strings.Join(words, " ")
	if typed != "" {
		last := []rune(typed)[len([]rune(typed))-1]
		first := []rune(s)[0]
		if wordRune(last) && wordRune(first) {
			s = " " + s
		}
	}
	return s
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// models is provider.Models unless a test replaced it.
func (e *rpcEngine) models(ctx context.Context, p config.Profile) ([]provider.Model, error) {
	if e.listModels != nil {
		return e.listModels(ctx, p)
	}
	return provider.Models(ctx, p)
}
