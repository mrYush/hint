package debuglog

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestOpenAndRedact(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state", "log")
	now := time.Date(2026, 9, 7, 15, 30, 12, 0, time.UTC)
	l, err := Open(dir, now, []string{"sk-secret-1234567890"})
	if err != nil {
		t.Fatal(err)
	}
	l.Printf("POST %s key=%s body=%s", "https://x", "sk-secret-1234567890", `{"authorization":"Bearer sk-secret-1234567890"}`)
	l.Printf("plain line")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	if base := filepath.Base(l.Path()); !strings.HasPrefix(base, "20260907-153012-") || !strings.HasSuffix(base, ".log") || filepath.Dir(l.Path()) != dir {
		t.Errorf("path = %s", l.Path())
	}
	data, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "sk-secret") {
		t.Fatalf("secret leaked:\n%s", text)
	}
	if strings.Count(text, "***") != 2 || !strings.Contains(text, "plain line") {
		t.Errorf("trace:\n%s", text)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "2") || !strings.Contains(lines[0], " POST ") {
		t.Errorf("lines = %q", lines)
	}

	if runtime.GOOS != "windows" {
		if info, err := os.Stat(l.Path()); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("file mode = %v, %v; want 0600", info.Mode(), err)
		}
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v, %v; want 0700", info.Mode(), err)
		}
	}
}

func TestDefaultDir(t *testing.T) {
	lookup := func(env map[string]string) func(string) (string, bool) {
		return func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	}
	if got := DefaultDir("/home/u", lookup(nil)); got != filepath.Join("/home/u", ".local", "state", "hint", "log") {
		t.Errorf("DefaultDir = %q", got)
	}
	if got := DefaultDir("/home/u", lookup(map[string]string{"XDG_STATE_HOME": "/st"})); got != filepath.Join("/st", "hint", "log") {
		t.Errorf("DefaultDir with XDG = %q", got)
	}
	if got := DefaultDir("/home/u", lookup(map[string]string{"XDG_STATE_HOME": ""})); got != filepath.Join("/home/u", ".local", "state", "hint", "log") {
		t.Errorf("DefaultDir with empty XDG = %q", got)
	}
}
