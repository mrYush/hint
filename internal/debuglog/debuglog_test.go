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

func TestRetention(t *testing.T) {
	now := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name               string
		directory, removed bool
	}{
		{"20261105-115959-123.log", false, true},
		{"20261105-120000-123.log", false, false},
		{"20261112-110000-123.log", false, false},
		{"20261101-120000-123.txt", false, false},
		{"20261301-120000-123.log", false, false},
		{"20261101-120000-abc.log", false, false},
		{"20261101-120000-.log", false, false},
		{"20261101-120000-123.log", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name)
			var err error
			if tc.directory {
				err = os.Mkdir(path, 0o700)
			} else {
				err = os.WriteFile(path, []byte("trace"), 0o600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if !tc.directory {
				stamp, parseErr := time.ParseInLocation("20060102-150405", tc.name[:15], now.Location())
				if parseErr == nil {
					if err := os.Chtimes(path, stamp, stamp); err != nil {
						t.Fatal(err)
					}
				}
			}
			trace, err := Open(dir, now, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := trace.Close(); err != nil {
				t.Fatal(err)
			}
			_, err = os.Stat(path)
			if tc.removed {
				if !os.IsNotExist(err) {
					t.Fatalf("old trace remains: %v", err)
				}
			} else if err != nil {
				t.Fatalf("retained entry: %v", err)
			}
		})
	}
}

func TestOpenSurvivesPruneFailure(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("requires Unix directory permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Error(err)
		}
	}()
	// The directory permits creating a trace, but not listing old traces.
	trace, err := Open(dir, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := trace.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(trace.Path())
	if err != nil || !strings.Contains(string(body), "warning: pruning old debug logs") {
		t.Fatalf("trace = %s, %v", body, err)
	}
}

func TestRetentionKeepsRecentlyWrittenOldTrace(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	path := filepath.Join(dir, now.AddDate(0, 0, -8).Format("20060102-150405")+"-123.log")
	if err := os.WriteFile(path, []byte("still active"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prune(dir, now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("recently written trace removed: %v", err)
	}
}
