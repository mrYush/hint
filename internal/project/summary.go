package project

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mrYush/hint/pkg/agentapi"
)

// Summarizer rewrites an instruction file that cannot fit its budget even
// as an outline (rung 3 of WP0.12). It is declared here, on the consumer's
// side, so the project package needs no provider: [ChatSummarizer] is the
// implementation cmd/hint installs, tests use a stub.
type Summarizer interface {
	// Summarize returns a summary of content, the text of the file at
	// path, meant to fit in maxBytes. A longer answer is cut by the
	// caller; an error leaves the file unsummarized.
	Summarize(ctx context.Context, path, content string, maxBytes int) (string, error)
}

// summarySystemPrompt asks for a faithful compression, not a rewrite:
// rules and paths are the point of an instruction file.
const summarySystemPrompt = `You compress a project's instruction file for an AI coding assistant that will read the summary instead of the file.
Keep every rule, prohibition, command, path and name; drop explanations, examples and repetition.
Keep the original headings and order so the assistant can ask for a section by name. Write plain Markdown, no preamble.`

// summaryPromptVersion tracks changes to the prompt format and budget conversion.
const summaryPromptVersion = "1"

// ChatSummarizer is the [Summarizer] over an [agentapi.ChatProvider]: one
// plain-text request with no tools, the same shape as the agent's own
// compaction summary.
type ChatSummarizer struct {
	Provider agentapi.ChatProvider
}

// Summarize implements [Summarizer].
func (s ChatSummarizer) Summarize(ctx context.Context, path, content string, maxBytes int) (string, error) {
	if s.Provider == nil {
		return "", errors.New("no provider to summarize with")
	}
	// A byte budget means little to a model; words do. Four bytes a token
	// and a token and a half a word is close enough for a limit the
	// caller enforces anyway.
	words := max(maxBytes/6, 50)
	req := agentapi.ChatRequest{Messages: []agentapi.Message{
		agentapi.SystemMessage(summarySystemPrompt),
		agentapi.UserMessage(fmt.Sprintf("Summarize %s in at most %d words.\n\n%s", path, words, content)),
	}}
	events, err := s.Provider.Stream(ctx, req)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for ev := range events {
		switch ev.Kind {
		case agentapi.ChatTextDelta:
			text.WriteString(ev.Text)
		case agentapi.ChatError:
			return "", ev.Err
		case agentapi.ChatThinkingDelta, agentapi.ChatToolCall, agentapi.ChatUsage, agentapi.ChatDone:
			// A summary is plain text; the rest of the stream is not
			// part of it.
		}
	}
	return text.String(), nil
}

// CachedSummarizer keeps summaries under Dir by prompt, provider configuration,
// path, content and byte budget. A summary costs a model call per file; the cache is
// what makes the opt-in affordable across runs. A cache that cannot be
// read or written is simply not used — the summary still comes back.
type CachedSummarizer struct {
	Dir   string
	Inner Summarizer
	// ProviderKey identifies the configured primary and fallback models and endpoints.
	ProviderKey string
}

// DefaultSummaryCacheDir is $XDG_CACHE_HOME/hint/instructions when the
// variable is set, otherwise ~/.cache/hint/instructions.
func DefaultSummaryCacheDir() (string, error) {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "hint", "instructions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "hint", "instructions"), nil
}

// Summarize implements [Summarizer].
func (c CachedSummarizer) Summarize(ctx context.Context, path, content string, maxBytes int) (string, error) {
	hash := sha256.New()
	for _, field := range []string{summaryPromptVersion, summarySystemPrompt, c.ProviderKey, path, strconv.Itoa(maxBytes), content} {
		fmt.Fprintf(hash, "%d:%s", len(field), field)
	}
	file := filepath.Join(c.Dir, hex.EncodeToString(hash.Sum(nil))+".md")
	if data, err := os.ReadFile(file); err == nil && len(data) > 0 {
		return string(data), nil
	}
	summary, err := c.Inner.Summarize(ctx, path, content, maxBytes)
	if err != nil || strings.TrimSpace(summary) == "" {
		return summary, err
	}
	// A cache write failure does not invalidate a successful summary.
	_ = writeSummaryCache(file, summary)
	return summary, nil
}

func writeSummaryCache(path, summary string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".summary-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, writeErr := tmp.WriteString(summary)
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
