//go:build linux || darwin

package repomap

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/workspace"
)

func TestPolicyFIFO(t *testing.T) {
	if mode := os.Getenv("HINT_TEST_POLICY_FIFO"); mode != "" {
		testPolicyFIFOChild(t, mode, os.Getenv("HINT_TEST_POLICY_DIR"))
		return
	}
	for _, mode := range []string{"existing", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// A regression must kill the blocked child, not hang this test's
			// cleanup or leave a goroutine owning the manager's shared slot.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestPolicyFIFO$", "-test.count=1")
			cmd.Env = append(os.Environ(), "HINT_TEST_POLICY_FIFO="+mode, "HINT_TEST_POLICY_DIR="+t.TempDir())
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("policy read or manager shutdown blocked: %v\n%s", ctx.Err(), output)
			}
			if err != nil {
				t.Fatalf("policy subprocess: %v\n%s", err, output)
			}
		})
	}
}

func testPolicyFIFOChild(t *testing.T, mode, base string) {
	t.Helper()
	dir := filepath.Join(base, "root")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".gitignore")
	if mode == "existing" {
		if err := syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
	} else {
		managerWrite(t, path, "before\n")
	}
	root, err := tool.NewRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(root.Dir(), ".gitignore")
	armed, checks, replaced := false, 0, false
	root = root.WithAccessCheck(func(abs string) error {
		if armed && abs == path {
			checks++
			// Lease.Verify resolves the path, then Open stats it, then the
			// rooted open checks access. Swap only at that final boundary.
			if checks == 3 {
				armed = false
				if err := os.Remove(path); err != nil {
					return err
				}
				if err := syscall.Mkfifo(path, 0600); err != nil {
					return err
				}
				replaced = true
			}
		}
		return nil
	})
	registry, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(t.Context(), Options{Path: filepath.Join(base, "index.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(m.Close(), registry.Close(), root.Close()); err != nil {
			t.Error(err)
		}
	})
	space := registry.NewSession()
	lease, err := space.Resolve("", ".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	armed = mode == "replacement"
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err = func() (string, error) {
		ctx, release, err := m.acquireFocused(ctx)
		if err != nil {
			return "", err
		}
		defer release()
		return policyFingerprint(ctx, lease)
	}()
	if !errors.Is(err, workspace.ErrNotRegular) {
		t.Fatalf("FIFO policy must be rejected as non-regular: %v", err)
	}
	if mode == "replacement" && !replaced {
		t.Fatalf("replacement hook did not run: checks=%d", checks)
	}
	// A rejected source must leave admission usable and regular fingerprints
	// unchanged; no writer ever opens the FIFO to unblock the test.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	managerWrite(t, path, "ignored\n")
	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Second)
	defer cancel2()
	ctx2, release, err := m.acquireFocused(ctx2)
	if err != nil {
		t.Fatalf("shared slot was not released: %v", err)
	}
	got, readErr := policyFingerprint(ctx2, lease)
	release()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%d:%d:%x", info.Size(), info.ModTime().UnixNano(), sha256.Sum256([]byte("ignored\n")))
	if readErr != nil || got != want {
		t.Fatalf("regular fingerprint=%q, %v; want %q", got, readErr, want)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if _, err := policyFingerprint(canceled, lease); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled policy read: %v", err)
	}
	m.cancel()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
}
