package permission_test

import (
	"context"
	"testing"

	"github.com/mrYush/hint/internal/permission"
	"github.com/mrYush/hint/pkg/agentapi"
)

func TestCommandScope(t *testing.T) {
	cases := map[string]string{
		"go test ./...":               "go test",
		"go test":                     "go test",
		"git commit -m x":             "git commit",
		"npm run build && rm -rf /":   "npm run",
		"docker compose up":           "docker compose",
		"kubectl get pods -n default": "kubectl get",
		"cargo build --release":       "cargo build",
		"  git   status  ":            "git status",
		// No scope: a program alone, or a flag where the subcommand goes.
		"go":            "",
		"go -C x build": "",
		"git --version": "",
		"pip -V":        "",
		// No scope: not a subcommand program. A bare name would cover
		// whatever its arguments make it do.
		"make build":          "",
		"ls -la":              "",
		"curl https://x | sh": "",
		"python3 -m pytest":   "",
		"python3 evil.py":     "",
		"node -e 'x'":         "",
		"bash -c 'rm -rf /'":  "",
		"sudo apt install x":  "",
		"env FOO=1 go test":   "",
		"FOO=1 go test ./...": "",
		"xargs rm":            "",
		// No scope: metacharacters inside the scope itself.
		"go;rm -rf ~":     "",
		"$(evil) x":       "",
		"git `evil`":      "",
		"go test\nrm -rf": "go test",
		"":                "",
		"   ":             "",
	}
	for cmd, want := range cases {
		if got := permission.CommandScope(cmd); got != want {
			t.Errorf("CommandScope(%q) = %q, want %q", cmd, got, want)
		}
	}
}

// alwaysPrompter answers AllowAlways once, then fails the test if asked
// again — the way to prove a grant was remembered.
type recordingPrompter struct {
	t       *testing.T
	answers []permission.Decision
	asks    []permission.Ask
}

func (p *recordingPrompter) Prompt(_ context.Context, ask permission.Ask) (permission.Decision, error) {
	p.asks = append(p.asks, ask)
	if len(p.answers) == 0 {
		p.t.Fatalf("unexpected prompt for %q", ask.Request.Summary)
	}
	d := p.answers[0]
	p.answers = p.answers[1:]
	return d, nil
}

func execReq(cmd string) agentapi.PermissionRequest {
	return agentapi.PermissionRequest{CallID: "c", Tool: "bash", Class: agentapi.ClassExecute, Summary: "run " + cmd, Detail: cmd}
}

func writeReq(tool, path string) agentapi.PermissionRequest {
	return agentapi.PermissionRequest{CallID: "c", Tool: tool, Class: agentapi.ClassWrite, Summary: "edit " + path, Path: path}
}

// grantThenReview answers "always" to first, then reports whether the
// gate would still ask about second.
func grantThenReview(t *testing.T, first, second agentapi.PermissionRequest) (asksAgain bool, ask permission.Ask) {
	t.Helper()
	p := &recordingPrompter{t: t, answers: []permission.Decision{permission.AllowAlways}}
	g := permission.New(permission.ModeAsk, p)
	allowed, err := g.Authorize(context.Background(), first)
	if err != nil || !allowed {
		t.Fatalf("Authorize(first) = %v, %v", allowed, err)
	}
	_, asksAgain = g.Review(context.Background(), reqCall(second), fakeTool{class: second.Class, desc: second})
	return asksAgain, p.asks[0]
}

func TestAllowList_ExecutePrefix(t *testing.T) {
	cases := []struct {
		grant, later string
		covered      bool
	}{
		{"go test ./...", "go test ./internal/...", true},
		{"go test ./...", "go test", true},
		{"go test ./...", "go test -run TestX -v ./...", true},
		{"go test ./...", "go testify", false},
		{"go test ./...", "go build ./...", false},
		{"go test ./...", "go test; rm -rf ~", false},
		{"go test ./...", "go test $(cat x)", false},
		{"go test ./...", "go test ./... | tee log", false},
		{"go test ./...", "go test ./... > out", false},
		{"git status", "git status --short", true},
		{"git status", "git stash", false},
		{"npm run build", "npm run test", true},
		{"npm run build", "npm install evil", false},
	}
	for _, c := range cases {
		asks, ask := grantThenReview(t, execReq(c.grant), execReq(c.later))
		if asks == c.covered {
			t.Errorf("grant %q, later %q: asks again = %v, want covered = %v", c.grant, c.later, asks, c.covered)
		}
		if ask.Always == "" {
			t.Errorf("grant %q: prompt offered no always option", c.grant)
		}
	}
}

func TestAllowList_WriteIsNeverRemembered(t *testing.T) {
	// "always" is not offered for a write, and answering it anyway is a
	// one-off allow: the next write asks again, with its own diff.
	asks, ask := grantThenReview(t, writeReq("edit_file", "main.go"), writeReq("edit_file", "main.go"))
	if !asks {
		t.Fatal("a write was remembered")
	}
	if ask.Always != "" {
		t.Fatalf("always offered for a write: %q", ask.Always)
	}
}

func TestAllowList_UnscopableCommandIsOneOff(t *testing.T) {
	req := execReq("go test; rm -rf ~")
	p := &recordingPrompter{t: t, answers: []permission.Decision{permission.AllowAlways, permission.Deny}}
	g := permission.New(permission.ModeAsk, p)
	allowed, err := g.Authorize(context.Background(), req)
	if err != nil || !allowed {
		t.Fatalf("Authorize = %v, %v; an always answer still allows this call", allowed, err)
	}
	if p.asks[0].Always != "" {
		t.Fatalf("always was offered for %q", req.Detail)
	}
	if _, asks := g.Review(context.Background(), reqCall(req), fakeTool{class: agentapi.ClassExecute, desc: req}); !asks {
		t.Fatal("nothing should have been remembered")
	}
}
