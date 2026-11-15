//go:build !windows

package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGitIgnoreProbeLocale(t *testing.T) {
	// This test changes process-wide environment or working directory.
	dir, git := gitProbeFixture(t)
	const script = `#!/bin/sh
if [ "$LC_ALL" != C ] || [ "$HINT_GIT_TEST" != preserved ]; then
    exit 2
fi
printf '%s\n' 'fatal: not a git repository' >&2
exit 128
`
	if err := os.WriteFile(git, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("LC_ALL", "ru_RU.UTF-8")
	t.Setenv("HINT_GIT_TEST", "preserved")
	for _, tc := range []struct {
		name string
		env  func() []string
	}{
		{"inherited", nil},
		{"sanitized", func() []string { return []string{"LC_ALL=ru_RU.UTF-8", "HINT_GIT_TEST=preserved"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ig, err := NewRules(git).WithEnviron(tc.env).Ignorer(t.Context(), dir)
			if err != nil || ig != Basic {
				t.Fatalf("Ignorer = %T, %v, want Basic without a warning", ig, err)
			}
		})
	}
}

func TestGitIgnoreProbeDeadline(t *testing.T) {
	t.Parallel()
	dir, git := gitProbeFixture(t)
	const script = "#!/bin/sh\nexec sleep 30\n"
	if err := os.WriteFile(git, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, err := NewRules(git).Ignorer(ctx, dir)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probe error = %v, want context.DeadlineExceeded", err)
	}
}

func TestGitIgnoreProbeExitError(t *testing.T) {
	t.Parallel()
	dir, git := gitProbeFixture(t)
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexit 128\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := NewRules(git).Ignorer(t.Context(), dir)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128 {
		t.Fatalf("probe error = %v, want exit status 128", err)
	}
}

func gitProbeFixture(t *testing.T) (dir, git string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, filepath.Join(dir, "git")
}
