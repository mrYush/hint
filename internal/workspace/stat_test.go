package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestLeaseStatMetadata(t *testing.T) {
	_, session, root := newWorkspace(t)
	write(t, filepath.Join(root.Dir(), "file"), "contents")
	for _, path := range []string{"file", "."} {
		t.Run(path, func(t *testing.T) {
			lease, err := session.Resolve("", path)
			if err != nil {
				t.Fatal(err)
			}
			got, err := lease.Stat()
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.Stat(lease.Path)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(got, want) || got.Mode() != want.Mode() || got.Size() != want.Size() {
				t.Fatalf("metadata mismatch: got %v, want %v", got, want)
			}
		})
	}
	lease, err := session.Resolve("", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := lease.Stat(); info != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source: info=%v err=%v", info, err)
	}
	if info, err := (Lease{}).Stat(); info != nil || !errors.Is(err, ErrChanged) {
		t.Fatalf("invalid lease: info=%v err=%v", info, err)
	}
}

func TestLeaseStatRechecks(t *testing.T) {
	for _, scenario := range []string{"replacement", "removed", "detached", "denied", "nested-marker", "nested-alias", "outside-alias", "root-replaced"} {
		t.Run(scenario, func(t *testing.T) {
			if scenario == "root-replaced" && runtime.GOOS == "windows" {
				t.Skip("Windows does not allow renaming an open root directory")
			}
			_, session, _ := newWorkspace(t)
			root, err := tool.NewRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			path := filepath.Join(root.Dir(), "sub", "file")
			write(t, path, "before")
			checks, trigger, changed, denied := 0, 0, false, false
			var mutate func() error
			checked := root.WithAccessCheck(func(abs string) error {
				if abs == path {
					checks++
					if trigger > 0 && checks == trigger {
						changed = true
						if err := mutate(); err != nil {
							return err
						}
					}
					if denied {
						return tool.ErrDenied
					}
				}
				return nil
			})
			info, err := session.Attach(checked, "source")
			if err != nil {
				t.Fatal(err)
			}
			wantErr := ErrChanged
			switch scenario {
			case "replacement":
				mutate = func() error {
					// Keep the old inode alive so reuse cannot conceal replacement.
					if err := os.Rename(path, path+".old"); err != nil {
						return err
					}
					return os.WriteFile(path, []byte("after"), 0600)
				}
			case "removed":
				wantErr = os.ErrNotExist
				mutate = func() error { return os.Remove(path) }
			case "detached":
				mutate = func() error { return session.Detach(info.ID) }
			case "denied":
				wantErr = tool.ErrDenied
				mutate = func() error { denied = true; return nil }
			case "nested-marker":
				wantErr = ErrBoundary
				mutate = func() error {
					return os.WriteFile(filepath.Join(filepath.Dir(path), ".git"), []byte("gitdir: elsewhere"), 0600)
				}
			case "nested-alias", "outside-alias":
				original := path
				path = filepath.Join(root.Dir(), "alias")
				if err := os.Symlink(original, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				targetDir := t.TempDir()
				wantErr = tool.ErrOutsideRoot
				if scenario == "nested-alias" {
					targetDir = filepath.Join(root.Dir(), "child")
					if err := os.Mkdir(targetDir, 0700); err != nil {
						t.Fatal(err)
					}
					if _, err := session.AttachLocal(targetDir, "child"); err != nil {
						t.Fatal(err)
					}
					wantErr = ErrBoundary
				}
				target := filepath.Join(targetDir, "file")
				// The inode remains the same: ownership must be rechecked even
				// when the two metadata observations agree on identity.
				if err := os.Link(original, target); err != nil {
					t.Fatal(err)
				}
				mutate = func() error {
					if err := os.Remove(path); err != nil {
						return err
					}
					return os.Symlink(target, path)
				}
			case "root-replaced":
				mutate = func() error {
					old := filepath.Join(t.TempDir(), "old")
					if err := os.Rename(root.Dir(), old); err != nil {
						return err
					}
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						return err
					}
					return os.WriteFile(path, []byte("replacement root"), 0600)
				}
			}
			rel, err := filepath.Rel(root.Dir(), path)
			if err != nil {
				t.Fatal(err)
			}
			lease, err := session.Resolve(info.ID, rel)
			if err != nil {
				t.Fatal(err)
			}
			checks = 0
			if err := lease.Verify(); err != nil {
				t.Fatal(err)
			}
			// Count Verify's checks independently of its boundary traversal.
			// Mutate immediately before Stat's second metadata observation.
			trigger, checks = checks+2, 0
			got, err := lease.Stat()
			if !changed || got != nil || !errors.Is(err, wantErr) {
				t.Fatalf("changed=%v info=%v err=%v; want %v (checks=%d trigger=%d)", changed, got, err, wantErr, checks, trigger)
			}
		})
	}
}
