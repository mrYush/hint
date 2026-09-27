package session_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// TestRecorderConversation covers what the REPL relies on: the Recorder
// starts from the continued session, keeps the prompt it was handed and
// the messages it observed, and a compaction replaces the lot with the
// checkpoint minus the preamble — in memory as well as on disk.
func TestRecorderConversation(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendMessage(agentapi.UserMessage("earlier")); err != nil {
		t.Fatal(err)
	}

	rec := session.NewRecorder(sess, 1)
	if got := rec.Messages(); len(got) != 1 || got[0].Text() != "earlier" {
		t.Fatalf("seeded messages = %v", got)
	}
	if err := rec.Append(agentapi.UserMessage("now")); err != nil {
		t.Fatal(err)
	}
	answer := agentapi.AssistantMessage("answer")
	rec.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &answer})
	if got := rec.Messages(); len(got) != 3 || got[2].Text() != "answer" {
		t.Fatalf("after a turn: %v", got)
	}
	// The copy is the caller's: growing it must not touch the Recorder.
	_ = append(rec.Messages(), agentapi.UserMessage("stray"))

	summary := agentapi.SystemMessage("summary")
	rec.Observe(agentapi.Event{Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{
		MessagesReplaced: 3,
		History:          []agentapi.Message{agentapi.SystemMessage("preamble"), summary},
	}})
	if got := rec.Messages(); len(got) != 1 || got[0].Text() != "summary" {
		t.Fatalf("after compaction: %v", got)
	}
	if err := rec.Err(); err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()

	reopened, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Messages(); len(got) != 1 || got[0].Text() != "summary" {
		t.Fatalf("on disk: %v", got)
	}
	_ = reopened.Close()

	// Without a session the conversation lives in memory alone.
	mem := session.NewRecorder(nil, 0)
	if err := mem.Append(agentapi.UserMessage("q")); err != nil {
		t.Fatal(err)
	}
	mem.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &answer})
	if got := mem.Messages(); len(got) != 2 {
		t.Fatalf("memory-only: %v", got)
	}
}

// TestRecorderAppendReportsWriteFailure: a prompt that cannot be
// journaled is an error the caller sees, and the memory keeps it anyway.
func TestRecorderAppendReportsWriteFailure(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	sess, err := store.Create(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	rec := session.NewRecorder(sess, 0)
	if err := rec.Append(agentapi.UserMessage("q")); err == nil {
		t.Fatal("Append on a closed session: want an error")
	}
	if len(rec.Messages()) != 1 || rec.Err() == nil {
		t.Errorf("messages = %v, err = %v", rec.Messages(), rec.Err())
	}
}

func TestFind(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	cwd := t.TempDir()

	if _, err := store.Find(cwd, "abc"); !errors.Is(err, session.ErrNoSessions) {
		t.Fatalf("empty directory: %v, want ErrNoSessions", err)
	}

	var ids []string
	for i := 0; i < 2; i++ {
		s, err := store.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, s.ID())
		_ = s.Close()
	}

	got, err := store.Find(cwd, ids[0])
	if err != nil || got.ID() != ids[0] {
		t.Fatalf("exact id: %v, %v", got, err)
	}
	_ = got.Close()

	// A prefix that is unique resolves; one shared by both is refused,
	// and so is a stranger.
	prefix := ids[1][:len(ids[1])-1]
	if strings.HasPrefix(ids[0], prefix) {
		t.Skip("random ids collide on a long prefix")
	}
	got, err = store.Find(cwd, prefix)
	if err != nil || got.ID() != ids[1] {
		t.Fatalf("unique prefix: %v, %v", got, err)
	}
	_ = got.Close()
	if _, err := store.Find(cwd, ""); !errors.Is(err, session.ErrUnknownSession) || !strings.Contains(err.Error(), "2 session ids") {
		t.Errorf("ambiguous prefix: %v", err)
	}
	if _, err := store.Find(cwd, "zzz-not-an-id"); !errors.Is(err, session.ErrUnknownSession) {
		t.Errorf("unknown id: %v", err)
	}
}

func TestPrint(t *testing.T) {
	store := session.NewStore(filepath.Join(t.TempDir(), "sessions"))
	cwd := t.TempDir()
	s, err := store.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AppendMessage(agentapi.UserMessage("first line\nsecond")); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	infos, err := store.List(cwd)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	session.Print(&out, infos)
	line := out.String()
	if !strings.HasPrefix(line, "  1. ") || !strings.Contains(line, s.ID()) || !strings.Contains(line, "1 msgs") || !strings.Contains(line, "first line...") {
		t.Errorf("Print = %q", line)
	}
}
