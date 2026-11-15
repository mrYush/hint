package permission_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"

	"github.com/mrYush/hint/internal/console"
	"github.com/mrYush/hint/internal/permission"
)

func promptWith(t *testing.T, input string, ask permission.Ask) (permission.Decision, string) {
	t.Helper()
	var out bytes.Buffer
	p := permission.NewReaderPrompter(strings.NewReader(input), &out)
	d, err := p.Prompt(context.Background(), ask)
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	return d, out.String()
}

func TestReaderPrompter_Answers(t *testing.T) {
	t.Parallel()
	ask := permission.Ask{Request: execReq("go test ./...")}
	ask.Request.Always = `commands starting with "go test"`
	cases := map[string]permission.Decision{
		"y\n":        permission.Allow,
		"yes\n":      permission.Allow,
		"  Y  \n":    permission.Allow,
		"n\n":        permission.Deny,
		"no\n":       permission.Deny,
		"\n":         permission.Deny, // Enter alone is the safe answer
		"a\n":        permission.AllowAlways,
		"always\n":   permission.AllowAlways,
		"what?\ny\n": permission.Allow, // re-asked after junk
		"":           permission.Deny,  // EOF
		"y":          permission.Allow, // last line without a newline
		// The same keys in a Cyrillic layout: н is y, т is n, ф is a.
		"н\n":      permission.Allow,
		"Нуы\n":    permission.Allow,
		"т\n":      permission.Deny,
		"ф\n":      permission.AllowAlways,
		"фдцфны\n": permission.AllowAlways,
	}
	for input, want := range cases {
		got, out := promptWith(t, input, ask)
		if got != want {
			t.Errorf("input %q: decision = %v, want %v\n%s", input, got, want, out)
		}
	}
}

func TestReaderPrompter_ShowsSummaryDetailAndAlways(t *testing.T) {
	t.Parallel()
	ask := permission.Ask{Request: execReq("go test ./...")}
	ask.Request.Always = `commands starting with "go test"`
	ask.Request.Summary = "edit main.go"
	ask.Request.Detail = "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	_, out := promptWith(t, "y\n", ask)
	for _, want := range []string{"hint: edit main.go\n", "-old\n+new\n", "[y]es / [N]o / [a]lways (commands starting with \"go test\"): "} {
		if !strings.Contains(out, want) {
			t.Errorf("prompt output lacks %q:\n%s", want, out)
		}
	}
}

func TestReaderPrompter_AlwaysNotOfferedIsRejected(t *testing.T) {
	t.Parallel()
	ask := permission.Ask{Request: execReq("go test; rm -rf ~")}
	got, out := promptWith(t, "a\ny\n", ask)
	if got != permission.Allow {
		t.Fatalf("decision = %v, want Allow after the rejected 'a'", got)
	}
	if strings.Contains(out, "[a]lways") || !strings.Contains(out, "not available") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestReaderPrompter_LongDetailIsTruncatedOnScreen(t *testing.T) {
	t.Parallel()
	ask := permission.Ask{Request: writeReq("write_file", "big.txt")}
	ask.Request.Detail = strings.Repeat("+line of a very long diff\n", 2000)
	_, out := promptWith(t, "y\n", ask)
	if len(out) > 20_000 || !strings.Contains(out, "output truncated") {
		t.Fatalf("detail not truncated: %d bytes of output", len(out))
	}
	if ask.Request.Detail != strings.Repeat("+line of a very long diff\n", 2000) {
		t.Fatal("the request itself was modified")
	}
}

func TestReaderPrompter_StaleLineIsNotAnAnswer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		var out bytes.Buffer
		lines := console.NewLineReader(pr)
		p := permission.NewLinePrompter(lines, &out)
		ask := permission.Ask{Request: execReq("ls")}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := p.Prompt(ctx, ask); !errors.Is(err, context.Canceled) {
			t.Fatalf("first prompt = %v, want context.Canceled", err)
		}
		if _, err := io.WriteString(pw, "y\n"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait() // The reader is blocked offering the stale approval.

		answered := make(chan struct{})
		go func() {
			defer close(answered)
			d, err := p.Prompt(t.Context(), ask)
			if d != permission.Deny || err != nil {
				t.Errorf("second prompt = %v, %v; want Deny from the fresh line", d, err)
			}
		}()
		synctest.Wait()
		select {
		case <-answered:
			t.Error("second prompt answered before the fresh line was written")
		default:
		}
		if _, err := io.WriteString(pw, "n\n"); err != nil {
			t.Fatal(err)
		}
		<-answered
		if err := pw.Close(); err != nil {
			t.Fatal(err)
		}
		// Drain through EOF even if the prompt returned before consuming "n".
		for {
			_, err := lines.ReadLine(t.Context())
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReaderPrompter_EOFStaysDeniedAfterDrain(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	p := permission.NewReaderPrompter(strings.NewReader(""), &out)
	for i := 0; i < 2; i++ {
		d, err := p.Prompt(context.Background(), permission.Ask{Request: execReq("ls")})
		if d != permission.Deny || err != nil {
			t.Fatalf("prompt %d after EOF = %v, %v", i, d, err)
		}
	}
}

func TestReaderPrompter_EOFDeniesAndSaysSo(t *testing.T) {
	t.Parallel()
	got, out := promptWith(t, "", permission.Ask{Request: execReq("ls")})
	if got != permission.Deny || !strings.Contains(out, "no answer") {
		t.Fatalf("decision = %v, output:\n%s", got, out)
	}
}

func TestReaderPrompter_InputErrors(t *testing.T) {
	t.Parallel()
	cause := errors.New("input failed")
	for _, tc := range []struct {
		name string
		err  error
		want error
	}{
		{"read failure", cause, cause},
		{"wrapped read failure", fmt.Errorf("reader: %w", cause), cause},
		{"EOF", io.EOF, nil},
		{"wrapped EOF", fmt.Errorf("reader: %w", io.EOF), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			p := permission.NewReaderPrompter(iotest.ErrReader(tc.err), &out)
			g := permission.New(permission.ModeAsk, p)
			for i := 0; i < 2; i++ {
				allowed, err := g.Authorize(context.Background(), execReq("go test"))
				if allowed || !errors.Is(err, tc.want) {
					t.Fatalf("prompt %d = %v, %v; want denied, %v", i, allowed, err, tc.want)
				}
				if tc.want != nil && strings.Contains(out.String(), "; denied") {
					t.Fatalf("input failure reported as an ordinary denial: %s", out.String())
				}
			}
		})
	}
}

func TestReaderPrompter_CancelWhileWaiting(t *testing.T) {
	t.Parallel()
	// The pipe stays quiet until the answer is written, so the only way
	// out of the first prompt is the context. The answer is written
	// after that next prompt is already waiting: Prompt drops a line
	// that is already queued, and writing it earlier leaves the prompt
	// blocked on a line that will never come.
	synctest.Test(t, func(t *testing.T) {
		pr, pw := io.Pipe()
		defer func() { _ = pw.Close() }()
		var out bytes.Buffer
		p := permission.NewReaderPrompter(pr, &out)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		canceled := make(chan struct{})
		go func() {
			defer close(canceled)
			d, err := p.Prompt(ctx, permission.Ask{Request: execReq("ls")})
			if d != permission.Deny || !errors.Is(err, context.Canceled) {
				t.Errorf("= %v, %v; want Deny, context.Canceled", d, err)
			}
		}()
		synctest.Wait()
		cancel()
		<-canceled

		// The same prompter is usable for the next question: the reader
		// goroutine survived the cancel and picks up the next line.
		type answer struct {
			d   permission.Decision
			err error
		}
		got := make(chan answer, 1)
		go func() {
			d, err := p.Prompt(context.Background(), permission.Ask{Request: execReq("ls")})
			got <- answer{d, err}
		}()
		synctest.Wait()
		if _, err := io.WriteString(pw, "y\n"); err != nil {
			t.Fatal(err)
		}
		res := <-got
		if res.d != permission.Allow || res.err != nil {
			t.Fatalf("second prompt = %v, %v; want Allow", res.d, res.err)
		}

		// Closing the pipe delivers EOF. Read it so the reader goroutine
		// exits: synctest.Test waits for every goroutine in the bubble.
		if err := pw.Close(); err != nil {
			t.Fatal(err)
		}
		d, err := p.Prompt(context.Background(), permission.Ask{Request: execReq("ls")})
		if d != permission.Deny || err != nil {
			t.Fatalf("prompt after EOF = %v, %v; want Deny", d, err)
		}
	})
}
