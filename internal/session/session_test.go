package session_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/session"
	"github.com/mrYush/hint/pkg/agentapi"
)

// update regenerates testdata/golden_v1.jsonl from the current writer.
// Run `go test ./internal/session -update` only when the format changed on
// purpose, and review the diff: the golden file is the backward-compat
// gate for every session already on a user's disk.
var update = flag.Bool("update", false, "rewrite the golden session file")

const goldenPath = "testdata/golden_v1.jsonl"

// fixedClock hands out one minute per call from a fixed origin, so a
// written file is byte-for-byte reproducible.
func fixedClock() func() time.Time {
	t := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return func() time.Time {
		t = t.Add(time.Minute)
		return t
	}
}

func fixedIDs(ids ...string) func() (string, error) {
	i := 0
	return func() (string, error) {
		if i >= len(ids) {
			return "", errors.New("out of ids")
		}
		id := ids[i]
		i++
		return id, nil
	}
}

func newStore(t *testing.T, ids ...string) *session.Store {
	t.Helper()
	return session.NewStore(filepath.Join(t.TempDir(), "sessions"),
		session.WithClock(fixedClock()), session.WithIDs(fixedIDs(ids...)))
}

// goldenConversation is the turn the golden file records: a question, a
// tool round, a compaction that summarized an earlier exchange, and the
// final answer. Messages after the checkpoint are what Messages() must
// return on load.
func goldenConversation() (before, checkpoint, after []agentapi.Message) {
	call := agentapi.ToolCall{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{"path":"main.go"}`)}
	before = []agentapi.Message{
		agentapi.UserMessage("what does main.go do?"),
		{Role: agentapi.RoleAssistant, Content: []agentapi.ContentPart{agentapi.Thinking("look first")}, ToolCalls: []agentapi.ToolCall{call}},
		agentapi.TextResult("c1", "read_file", "package main\n").Message(),
	}
	checkpoint = []agentapi.Message{
		agentapi.SystemMessage("earlier: the user asked about main.go; it is a cobra CLI"),
		agentapi.UserMessage("add a --version flag"),
	}
	after = []agentapi.Message{
		agentapi.AssistantMessage("Done: --version prints the build version."),
	}
	return before, checkpoint, after
}

func writeGolden(t *testing.T, store *session.Store, cwd string) *session.Session {
	t.Helper()
	sess, err := store.Create(cwd)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before, checkpoint, after := goldenConversation()
	for _, m := range before {
		if err := sess.AppendMessage(m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	if err := sess.AppendCompaction(agentapi.Compaction{
		MessagesReplaced: 3, TokensBefore: 90, TokensAfter: 20,
		Summary: checkpoint[0].Text(),
		History: checkpoint,
	}); err != nil {
		t.Fatalf("AppendCompaction: %v", err)
	}
	for _, m := range after {
		if err := sess.AppendMessage(m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return sess
}

// TestGoldenFormat pins the on-disk format in both directions: what the
// writer produces today must equal the committed file, and the committed
// file must still load into the expected conversation. A change to either
// is a change to every session on a user's disk and has to be deliberate.
func TestGoldenFormat(t *testing.T) {
	store := newStore(t, "0123456789abcdef")
	sess := writeGolden(t, store, "/home/dev/project")

	got, err := os.ReadFile(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(goldenPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden file (run with -update to create it): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("session file differs from %s\n got:\n%s\nwant:\n%s", goldenPath, got, want)
	}
	if base := filepath.Base(sess.Path()); base != "20260906T120100Z_0123456789abcdef.jsonl" {
		t.Errorf("file name = %s, want the creation timestamp and id", base)
	}

	// The committed file, not the one just written, is what a user's disk
	// holds: load that.
	loaded, err := store.Open(goldenPath)
	if err != nil {
		t.Fatalf("Open golden: %v", err)
	}
	defer func() { _ = loaded.Close() }()
	_, checkpoint, after := goldenConversation()
	wantMessages := append(append([]agentapi.Message(nil), checkpoint...), after...)
	if !reflect.DeepEqual(loaded.Messages(), wantMessages) {
		t.Errorf("loaded conversation = %+v\nwant %+v", loaded.Messages(), wantMessages)
	}
	if loaded.ID() != "0123456789abcdef" || loaded.Cwd() != "/home/dev/project" {
		t.Errorf("header = %s %s, want the golden id and cwd", loaded.ID(), loaded.Cwd())
	}
	if len(loaded.Warnings()) != 0 {
		t.Errorf("warnings on a clean file: %v", loaded.Warnings())
	}
}

func TestOpenAppendsAfterLoad(t *testing.T) {
	store := newStore(t, "aaaa")
	sess := writeGolden(t, store, "/p")

	reopened, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.AppendMessage(agentapi.UserMessage("and tests?")); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reopened.AppendMessage(agentapi.UserMessage("late")); err == nil {
		t.Error("AppendMessage after Close must fail")
	}

	again, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	msgs := again.Messages()
	if len(msgs) != 4 || msgs[3].Text() != "and tests?" {
		t.Fatalf("after reopen: %d messages, last %+v", len(msgs), msgs[len(msgs)-1])
	}
}

func TestLoadSkipsNewerRecordsAndFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"kind":"session","at":"2026-09-06T12:00:00Z","version":1,"wire_version":1,"id":"x1","cwd":"/p","label":"future field"}`,
		`{"kind":"message","at":"2026-09-06T12:01:00Z","message":{"role":"user","content":[{"kind":"text","text":"hi"}]}}`,
		`{"kind":"bookmark","at":"2026-09-06T12:02:00Z","target":"c1"}`,
		`{"kind":"message","at":"2026-09-06T12:03:00Z","message":{"role":"user","content":[{"kind":"hologram","data":"..."}]}}`,
		`{"kind":"message","at":"2026-09-06T12:04:00Z","message":{"role":"assistant","content":[{"kind":"text","text":"hello"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sess, err := session.NewStore(dir).Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = sess.Close() }()
	if got := sess.Messages(); len(got) != 2 || got[0].Text() != "hi" || got[1].Text() != "hello" {
		t.Fatalf("messages = %+v, want the two known ones", got)
	}
	if w := sess.Warnings(); len(w) != 2 || !strings.Contains(w[0], "line 3") || !strings.Contains(w[1], "line 4") {
		t.Fatalf("warnings = %v, want one for line 3 and one for line 4", w)
	}
}

func TestLoadRefusesNewerFormatAndMalformedFiles(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"newer format version": `{"kind":"session","at":"2026-09-06T12:00:00Z","version":2,"id":"x","cwd":"/p"}` + "\n",
		"no header":            `{"kind":"message","at":"2026-09-06T12:00:00Z","message":{"role":"user","content":[{"kind":"text","text":"hi"}]}}` + "\n",
		"empty file":           "",
		"malformed line":       `{"kind":"session","at":"2026-09-06T12:00:00Z","version":1,"id":"x","cwd":"/p"}` + "\n{not json}\n",
		"invalid message":      `{"kind":"session","at":"2026-09-06T12:00:00Z","version":1,"id":"x","cwd":"/p"}` + "\n" + `{"kind":"message","at":"2026-09-06T12:00:00Z","message":{"role":"tool","content":[]}}` + "\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".jsonl")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := session.NewStore(dir).Open(path); err == nil {
				t.Fatal("Open must fail")
			}
		})
	}
}

func TestLoadIgnoresUnterminatedLastLine(t *testing.T) {
	store := newStore(t, "bbbb")
	sess := writeGolden(t, store, "/p")
	// Simulate a crash mid-write: a partial record with no newline.
	f, err := os.OpenFile(sess.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"kind":"message","at":"2026-09-06T12:09:00Z","message":{"role":"user","con`)
	_ = f.Close()

	loaded, err := store.Open(sess.Path())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = loaded.Close() }()
	if loaded.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (checkpoint of 2 + 1)", loaded.Len())
	}
	if w := loaded.Warnings(); len(w) != 1 || !strings.Contains(w[0], "unterminated") {
		t.Fatalf("warnings = %v", w)
	}
	// Appending after the partial line must start on a fresh line, or the
	// next reader would see one garbled record instead of two.
	if err := loaded.AppendMessage(agentapi.UserMessage("after the crash")); err != nil {
		t.Fatal(err)
	}
	_ = loaded.Close()
	again, err := store.Open(sess.Path())
	if err != nil {
		t.Fatalf("Open after append: %v", err)
	}
	defer func() { _ = again.Close() }()
	if msgs := again.Messages(); len(msgs) != 4 || msgs[3].Text() != "after the crash" {
		t.Fatalf("messages after append = %+v", msgs)
	}
}

func TestMessagesRepairsDanglingToolCalls(t *testing.T) {
	store := newStore(t, "cccc")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	calls := []agentapi.ToolCall{
		{ID: "c1", Name: "read_file", Arguments: json.RawMessage(`{}`)},
		{ID: "c2", Name: "bash", Arguments: json.RawMessage(`{}`)},
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(sess.AppendMessage(agentapi.UserMessage("go")))
	must(sess.AppendMessage(agentapi.Message{Role: agentapi.RoleAssistant, ToolCalls: calls}))
	must(sess.AppendMessage(agentapi.TextResult("c1", "read_file", "ok").Message()))
	// c2 never got a result: the run was interrupted.
	_ = sess.Close()

	loaded, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loaded.Close() }()
	msgs := loaded.Messages()
	if len(msgs) != 4 {
		t.Fatalf("got %d messages, want 4 (a result synthesized for c2): %+v", len(msgs), msgs)
	}
	if msgs[3].Role != agentapi.RoleTool || msgs[3].ToolCallID != "c2" || !strings.Contains(msgs[3].Text(), "no result was recorded") {
		t.Errorf("synthesized result = %+v", msgs[3])
	}
	if err := (agentapi.ChatRequest{Messages: msgs}).Validate(); err != nil {
		t.Errorf("repaired history must be sendable: %v", err)
	}
	if loaded.Len() != 3 {
		t.Errorf("Len = %d, want 3: repair is a view, not a rewrite", loaded.Len())
	}
}

func TestListAndLatest(t *testing.T) {
	store := newStore(t, "first", "second", "other")
	if _, err := store.Latest("/p"); !errors.Is(err, session.ErrNoSessions) {
		t.Fatalf("Latest on an empty store = %v, want ErrNoSessions", err)
	}
	if infos, err := store.List("/p"); err != nil || len(infos) != 0 {
		t.Fatalf("List on an empty store = %v, %v", infos, err)
	}

	first, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	_ = first.AppendMessage(agentapi.UserMessage("first prompt\nsecond line"))
	_ = first.Close()
	second, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	_ = second.AppendMessage(agentapi.UserMessage("second prompt"))
	_ = second.Close()
	// Another project's session must not show up.
	other, err := store.Create("/q")
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()

	// Touch the first so it becomes the most recently used: "latest" is
	// about when a conversation last grew, not when it started.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(first.Path(), later, later); err != nil {
		t.Fatal(err)
	}

	infos, err := store.List("/p")
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 2 || infos[0].ID != "first" || infos[1].ID != "second" {
		t.Fatalf("List = %+v, want [first, second] by modification time", infos)
	}
	if infos[0].FirstPrompt != "first prompt\nsecond line" || infos[0].Messages != 1 || infos[0].Cwd != "/p" {
		t.Errorf("Info = %+v", infos[0])
	}

	latest, err := store.Latest("/p")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = latest.Close() }()
	if latest.ID() != "first" {
		t.Errorf("Latest = %s, want first", latest.ID())
	}
}

func TestListSkipsDamagedFiles(t *testing.T) {
	store := newStore(t, "good")
	good, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	_ = good.Close()
	if err := os.WriteFile(filepath.Join(store.ProjectDir("/p"), "zzz_bad.jsonl"), []byte("garbage\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	infos, err := store.List("/p")
	if err != nil || len(infos) != 1 || infos[0].ID != "good" {
		t.Fatalf("List = %+v, %v; want just the good session", infos, err)
	}
}

func TestDefaultDir(t *testing.T) {
	env := func(vars map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	}
	if got := session.DefaultDir("/home/u", env(nil)); got != filepath.Join("/home/u", ".local", "share", "hint", "sessions") {
		t.Errorf("DefaultDir without XDG = %s", got)
	}
	if got := session.DefaultDir("/home/u", env(map[string]string{"XDG_DATA_HOME": "/data"})); got != filepath.Join("/data", "hint", "sessions") {
		t.Errorf("DefaultDir with XDG = %s", got)
	}
	if got := session.DefaultDir("/home/u", nil); !strings.HasPrefix(got, "/home/u") {
		t.Errorf("DefaultDir with nil lookup = %s", got)
	}
}

func TestProjectDirIsStableAndPerProject(t *testing.T) {
	store := session.NewStore("/base")
	a, b := store.ProjectDir("/home/u/proj"), store.ProjectDir("/home/u/proj/")
	if a != b {
		t.Errorf("ProjectDir differs for the same cleaned path: %s vs %s", a, b)
	}
	if store.ProjectDir("/home/u/other") == a {
		t.Error("ProjectDir must differ between projects")
	}
	if !strings.HasPrefix(a, "/base"+string(filepath.Separator)) || strings.Contains(filepath.Base(a), "/") {
		t.Errorf("ProjectDir = %s", a)
	}
}

func TestRecorder(t *testing.T) {
	store := newStore(t, "rec")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	preamble := []agentapi.Message{agentapi.SystemMessage("you are hint")}
	rec := session.NewRecorder(sess, len(preamble))

	assistant := agentapi.AssistantMessage("answer")
	rec.Observe(agentapi.Event{Kind: agentapi.EventTurnStart})
	rec.Observe(agentapi.Event{Kind: agentapi.EventTextDelta, Text: "ans"})
	rec.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &assistant})
	compacted := append(append([]agentapi.Message(nil), preamble...),
		agentapi.SystemMessage("summary"), agentapi.UserMessage("latest"))
	rec.Observe(agentapi.Event{Kind: agentapi.EventCompaction, Compaction: &agentapi.Compaction{
		MessagesReplaced: 4, Summary: "summary", History: compacted,
	}})
	rec.Observe(agentapi.Event{Kind: agentapi.EventTurnEnd, FinishReason: agentapi.FinishStop})
	if err := rec.Err(); err != nil {
		t.Fatalf("Recorder error: %v", err)
	}
	_ = sess.Close()

	loaded, err := store.Open(sess.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loaded.Close() }()
	// The checkpoint is stored without the preamble: the next run prepends
	// its own.
	want := []agentapi.Message{agentapi.SystemMessage("summary"), agentapi.UserMessage("latest")}
	if got := loaded.Messages(); !reflect.DeepEqual(got, want) {
		t.Errorf("loaded = %+v, want the checkpoint without the preamble %+v", got, want)
	}
	if compacted[0].Text() != "you are hint" {
		t.Error("the recorder must not mutate the event's History")
	}
}

func TestRecorderStickyErrorAndNilSession(t *testing.T) {
	session.NewRecorder(nil, 1).Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &agentapi.Message{}})

	store := newStore(t, "closed")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.Close()
	rec := session.NewRecorder(sess, 0)
	m := agentapi.UserMessage("x")
	rec.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &m})
	first := rec.Err()
	if first == nil {
		t.Fatal("writing to a closed session must fail")
	}
	rec.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &m})
	if !errors.Is(rec.Err(), first) {
		t.Errorf("Err changed after the first failure: %v", rec.Err())
	}
}

func TestAppendRejectsInvalidMessage(t *testing.T) {
	store := newStore(t, "inv")
	sess, err := store.Create("/p")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if err := sess.AppendMessage(agentapi.Message{Role: agentapi.RoleUser}); err == nil {
		t.Error("an empty user message must be rejected before it reaches the file")
	}
	if err := sess.AppendCompaction(agentapi.Compaction{History: []agentapi.Message{{Role: "nope"}}}); err == nil {
		t.Error("a checkpoint with an invalid message must be rejected")
	}
	if sess.Len() != 0 {
		t.Errorf("Len = %d after rejected appends", sess.Len())
	}
}

func TestRecordValidate(t *testing.T) {
	msg := agentapi.UserMessage("hi")
	cases := []struct {
		name    string
		rec     session.Record
		wantErr bool
		unknown bool
	}{
		{"header", session.Record{Kind: session.KindSession, Version: 1, ID: "x"}, false, false},
		{"header without version", session.Record{Kind: session.KindSession, ID: "x"}, true, false},
		{"message", session.Record{Kind: session.KindMessage, Message: &msg}, false, false},
		{"message without payload", session.Record{Kind: session.KindMessage}, true, false},
		{"compaction", session.Record{Kind: session.KindCompaction, Compaction: &agentapi.Compaction{History: []agentapi.Message{msg}}}, false, false},
		{"compaction without payload", session.Record{Kind: session.KindCompaction}, true, false},
		{"no kind", session.Record{}, true, false},
		{"unknown kind", session.Record{Kind: "branch"}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.rec.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() = %v, wantErr %v", err, c.wantErr)
			}
			if errors.Is(err, session.ErrUnknownKind) != c.unknown {
				t.Errorf("ErrUnknownKind match = %v, want %v (%v)", !c.unknown, c.unknown, err)
			}
		})
	}
}

func TestAge(t *testing.T) {
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	cases := map[time.Duration]string{
		10 * time.Second: "just now",
		5 * time.Minute:  "5m ago",
		3 * time.Hour:    "3h ago",
		72 * time.Hour:   "3d ago",
	}
	for d, want := range cases {
		if got := session.Age(now.Add(-d), now); got != want {
			t.Errorf("Age(-%s) = %q, want %q", d, got, want)
		}
	}
}

// ExampleRecorder shows the wiring a CLI does for every turn: record the
// user message through the Recorder, send the run's preamble followed by
// the Recorder's conversation, then feed the agent's events back to it.
func ExampleRecorder() {
	store := session.NewStore(os.TempDir(), session.WithClock(fixedClock()), session.WithIDs(fixedIDs("example")))
	sess, _ := store.Create("/p")
	defer func() { _ = os.Remove(sess.Path()) }()
	defer func() { _ = sess.Close() }()

	preamble := []agentapi.Message{agentapi.SystemMessage("system prompt")}
	rec := session.NewRecorder(sess, len(preamble))
	_ = rec.Append(agentapi.UserMessage("hello"))
	history := append(append([]agentapi.Message(nil), preamble...), rec.Messages()...)

	answer := agentapi.AssistantMessage("hi")
	rec.Observe(agentapi.Event{Kind: agentapi.EventMessage, Message: &answer})
	fmt.Println(len(history), sess.Len(), len(rec.Messages()), rec.Err())
	// Output: 2 2 2 <nil>
}
