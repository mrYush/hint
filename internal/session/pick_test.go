package session_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/session"
)

func pickInfos() []session.Info {
	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	return []session.Info{
		{Path: "/s/2.jsonl", ID: "beefbeefbeefbeef", Cwd: "/p", Modified: base.Add(time.Hour), Messages: 4, FirstPrompt: "second session\nmore"},
		{Path: "/s/1.jsonl", ID: "bead000000000000", Cwd: "/p", Modified: base, Messages: 2, FirstPrompt: strings.Repeat("x", 100)},
	}
}

func choose(t *testing.T, input string) (session.Info, error, string) {
	t.Helper()
	var out bytes.Buffer
	info, err := session.Choose(context.Background(), &out, console.NewLineReader(strings.NewReader(input)), pickInfos())
	return info, err, out.String()
}

func TestChoose(t *testing.T) {
	cases := map[string]struct {
		wantID  string
		wantErr error
	}{
		"1\n":          {wantID: "beefbeefbeefbeef"},
		"2\n":          {wantID: "bead000000000000"},
		"beef\n":       {wantID: "beefbeefbeefbeef"},
		"be\n1\n":      {wantID: "beefbeefbeefbeef"}, // ambiguous prefix, then a number
		"9\n2\n":       {wantID: "bead000000000000"}, // out of range, then valid
		"nope\n2\n":    {wantID: "bead000000000000"}, // no such prefix, then valid
		"\n":           {wantErr: session.ErrNoChoice},
		"q\n":          {wantErr: session.ErrNoChoice},
		"":             {wantErr: session.ErrNoChoice}, // EOF
		"  beef  \n":   {wantID: "beefbeefbeefbeef"},
		"1":            {wantID: "beefbeefbeefbeef"}, // no trailing newline
		"junk\njunk\n": {wantErr: session.ErrNoChoice},
	}
	for input, c := range cases {
		info, err, out := choose(t, input)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("input %q: err = %v, want %v\n%s", input, err, c.wantErr, out)
			}
			continue
		}
		if err != nil || info.ID != c.wantID {
			t.Errorf("input %q: = %s, %v; want %s\n%s", input, info.ID, err, c.wantID, out)
		}
	}
}

func TestChooseListing(t *testing.T) {
	_, _, out := choose(t, "1\n")
	for _, want := range []string{"sessions in /p", "  1. ", "beefbeefbeefbeef", "4 msgs", "second session...", "  2. ", "xxxxxxxxxx..."} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "more") {
		t.Errorf("listing shows a second line of the prompt:\n%s", out)
	}
}

func TestChooseEmptyAndCancel(t *testing.T) {
	var out bytes.Buffer
	if _, err := session.Choose(context.Background(), &out, console.NewLineReader(strings.NewReader("1\n")), nil); !errors.Is(err, session.ErrNoSessions) {
		t.Errorf("Choose with nothing to choose = %v, want ErrNoSessions", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := session.Choose(ctx, &out, console.NewLineReader(strings.NewReader("")), pickInfos()); !errors.Is(err, context.Canceled) {
		t.Errorf("Choose with a cancelled context = %v, want context.Canceled", err)
	}
}

func TestChooseCapsTheList(t *testing.T) {
	var infos []session.Info
	for i := 0; i < 25; i++ {
		infos = append(infos, session.Info{ID: strings.Repeat(string(rune('a'+i%26)), 16), Cwd: "/p"})
	}
	var out bytes.Buffer
	info, err := session.Choose(context.Background(), &out, console.NewLineReader(strings.NewReader("20\n")), infos)
	if err != nil || info.ID != infos[19].ID {
		t.Fatalf("= %s, %v", info.ID, err)
	}
	if !strings.Contains(out.String(), "5 older sessions not listed") || strings.Contains(out.String(), " 21. ") {
		t.Errorf("listing:\n%s", out.String())
	}
	// An unlisted session is still reachable by id.
	info, err = session.Choose(context.Background(), &out, console.NewLineReader(strings.NewReader(infos[24].ID[:3]+"\n")), infos)
	if err != nil || info.ID != infos[24].ID {
		t.Fatalf("by id: = %s, %v", info.ID, err)
	}
}
