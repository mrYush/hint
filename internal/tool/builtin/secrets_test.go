package builtin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool/builtin"
)

// TestBash_RunsWithoutTheKeys pins the environment a subprocess sees:
// what WithEnviron returns, not the process's own, so `env` cannot hand
// a key to the model.
func TestBash_RunsWithoutTheKeys(t *testing.T) {
	needsPOSIXShell(t)
	t.Setenv("APIBAR_KEY", "sk-env-1234567890abcdef")
	t.Setenv("KEEP_ME", "yes")
	root := newRoot(t, nil)

	env := func() []string {
		var out []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "APIBAR_KEY=") {
				out = append(out, kv)
			}
		}
		return out
	}
	got := ok(t, run(t, builtin.NewBash(root, builtin.WithEnviron(env)), `{"command":"env"}`))
	if strings.Contains(got, "sk-env-1234567890abcdef") || strings.Contains(got, "APIBAR_KEY=") {
		t.Errorf("the key reached the subprocess:\n%s", got)
	}
	if !strings.Contains(got, "KEEP_ME=yes") {
		t.Errorf("the rest of the environment was lost:\n%s", got)
	}
	// Without the option the process environment is passed as it is.
	got = ok(t, run(t, builtin.NewBash(root), `{"command":"env"}`))
	if !strings.Contains(got, "APIBAR_KEY=") {
		t.Errorf("the default environment lost a variable:\n%s", got)
	}
}

// TestWalksSkipDeniedDirectories pins that list_dir, glob and grep — the
// pure-Go walks and ripgrep alike — never list, match or search inside a
// denied directory, and that read_file refuses it.
func TestWalksSkipDeniedDirectories(t *testing.T) {
	base := newRoot(t, map[string]string{
		"src/main.go":               "package main // needle\n",
		".config/hint/config.yaml":  "api_key: needle-sk-1234567890\n",
		".local/share/hint/s.jsonl": "{\"needle\":1}\n",
		".hint/config.yaml":         "model: needle\n",
		".hint/rules/go.md":         "needle rule\n",
		"notes/needle.txt":          "needle\n",
	})
	root := base.Deny(filepath.Join(base.Dir(), ".config", "hint"), filepath.Join(base.Dir(), ".local", "share", "hint"), filepath.Join(base.Dir(), ".hint", "config.yaml"))

	res := run(t, builtin.NewReadFile(root), `{"path":".config/hint/config.yaml"}`)
	if !res.IsError || !strings.Contains(res.Text(), "hint's own") {
		t.Errorf("read_file of the config = %q (error %v)", res.Text(), res.IsError)
	}
	if got := ok(t, run(t, builtin.NewReadFile(root), `{"path":".hint/rules/go.md"}`)); !strings.Contains(got, "needle rule") {
		t.Errorf("a rule file was refused: %q", got)
	}

	engines := []string{""}
	if p, err := exec.LookPath("rg"); err == nil {
		engines = append(engines, p)
	}
	for _, rg := range engines {
		opts := []builtin.Option{builtin.WithRipgrep(rg), builtin.WithGit("")}
		name := "pure-go"
		if rg != "" {
			name = "ripgrep"
		}
		t.Run(name, func(t *testing.T) {
			// Hidden directories are skipped by every walk anyway; the
			// denied ones must stay out even when asked for by name.
			listing := ok(t, run(t, builtin.NewListDir(root, opts...), `{"path":".config"}`))
			if strings.Contains(listing, "hint") {
				t.Errorf("list_dir .config shows the denied directory:\n%s", listing)
			}
			if res := run(t, builtin.NewListDir(root, opts...), `{"path":".config/hint"}`); !res.IsError {
				t.Errorf("list_dir of a denied directory succeeded:\n%s", res.Text())
			}
			found := ok(t, run(t, builtin.NewGlob(root, opts...), `{"pattern":"**/*.yaml","path":".hint"}`))
			if strings.Contains(found, "config.yaml") {
				t.Errorf("glob found the denied file:\n%s", found)
			}
			hits := ok(t, run(t, builtin.NewGrep(root, opts...), `{"pattern":"needle"}`))
			for _, bad := range []string{"config.yaml", "s.jsonl"} {
				if strings.Contains(hits, bad) {
					t.Errorf("grep searched a denied path (%s):\n%s", bad, hits)
				}
			}
			if !strings.Contains(hits, "src/main.go") || !strings.Contains(hits, "notes/needle.txt") {
				t.Errorf("grep lost allowed hits:\n%s", hits)
			}
			if res := run(t, builtin.NewGrep(root, opts...), `{"pattern":"needle","path":".config/hint"}`); !res.IsError {
				t.Errorf("grep inside a denied directory succeeded:\n%s", res.Text())
			}
		})
	}
}
