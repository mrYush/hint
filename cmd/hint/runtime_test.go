package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/config"
	"github.com/mrYush/hint/internal/session"
)

func TestCLIProcess(t *testing.T) {
	if os.Getenv("HINT_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"hint"}, os.Args[i+1:]...)
			break
		}
	}
	main()
	os.Exit(0)
}

func cliProcess(t *testing.T, env []string, args ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{"-test.run=^TestCLIProcess$", "--"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(env, "HINT_TEST_PROCESS=1", "PATH="+os.Getenv("PATH"), "SystemRoot="+os.Getenv("SystemRoot"))
	return cmd
}

func TestCLIExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		code int
	}{
		{"help", []string{"--help"}, 0},
		{"version", []string{"--version"}, 0},
		{"unknown flag", []string{"--bogus"}, 2},
		{"invalid mode", []string{"--mode", "bogus"}, 2},
		{"extra argument", []string{"serve", "extra"}, 2},
		{"conflicting flags", []string{"--ask", "--yolo"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := cliProcess(t, nil, tc.args...)
			output, err := cmd.CombinedOutput()
			code := 0
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				code = exit.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, output)
			}
		})
	}
}

func TestOneShotInterruptExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported by os.Process.Signal on Windows")
	}
	for _, tc := range []struct {
		name   string
		signal os.Signal
		code   int
	}{
		{"SIGINT", os.Interrupt, 130}, {"SIGTERM", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {

			started := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				started <- struct{}{}
				<-r.Context().Done()
			}))
			defer server.Close()
			cmd := cliProcess(t, []string{"HOME=" + t.TempDir(), "HINT_API_KEY=fixture", "HINT_MODEL=fixture", "HINT_API_URL=" + server.URL + "/v1"}, "--no-session", "-p", "hello")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("provider request did not start")
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			err := cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("exit = %v: %s", err, output.String())
			}
			if strings.Contains(output.String(), "hint: context canceled") || strings.Contains(output.String(), "hint: interrupt") || strings.Contains(output.String(), "hint: terminated") {
				t.Fatalf("noisy cancellation: %s", output.String())
			}
		})
	}
}

func TestEngineUsesInjectedEnvironment(t *testing.T) {
	fixed := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	home, cwd := t.TempDir(), t.TempDir()
	values := map[string]string{"HINT_API_KEY": "fixture", "HINT_MODEL": "injected", "XDG_STATE_HOME": filepath.Join(home, "state"), "XDG_DATA_HOME": filepath.Join(home, "data")}
	env := environment{home: home, cwd: cwd, now: func() time.Time { return fixed }, args: []string{"hint", "injected-argument"}, lookupEnv: func(key string) (string, bool) { value, ok := values[key]; return value, ok }}
	t.Setenv("HINT_MODEL", "wrong-process-model")
	eng, cleanup, err := newRPCEngine(context.Background(), options{env: env, debug: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if eng.setup.cwd != cwd || eng.setup.env.home != home {
		t.Fatalf("setup paths = %#v", eng.setup)
	}
	cfg, err := eng.reload()
	if err != nil {
		t.Fatal(err)
	}
	profile, err := cfg.Default()
	if err != nil || profile.Model != "injected" {
		t.Fatalf("reloaded profile = %+v, %v", profile, err)
	}
	if eng.store.Dir() != session.DefaultDir(home, env.lookup) {
		t.Fatalf("store directory = %s", eng.store.Dir())
	}
	logs, err := filepath.Glob(filepath.Join(home, "state", "hint", "log", "20261112-120000-*.log"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("injected trace = %v, %v", logs, err)
	}
	body, err := os.ReadFile(logs[0])
	if err != nil || !strings.Contains(string(body), "injected-argument") {
		t.Fatalf("trace args = %s, %v", body, err)
	}
	info := firstRunInfo(env, config.ErrNoProvider)
	if info.cwd != cwd || info.profile.Model != "injected" {
		t.Fatalf("first run setup = %+v", info)
	}
}

func TestRPCInterruptExit(t *testing.T) {
	testServerSignalExit(t, []string{"--mode", "rpc"}, "hint: rpc on stdio")
}

func TestServeInterruptExit(t *testing.T) {
	testServerSignalExit(t, []string{"serve", "--listen", "tcp:127.0.0.1:0"}, "hint: listening on tcp")
}

func testServerSignalExit(t *testing.T, args []string, readyMessage string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported by os.Process.Signal on Windows")
	}
	for _, tc := range []struct {
		name   string
		signal os.Signal
		code   int
	}{
		{"SIGINT", os.Interrupt, 130}, {"SIGTERM", syscall.SIGTERM, 143},
	} {
		t.Run(tc.name, func(t *testing.T) {

			cmd := cliProcess(t, []string{"HOME=" + t.TempDir(), "HINT_API_KEY=fixture", "HINT_MODEL=fixture"}, args...)
			input, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = input.Close() }()
			stderr, err := cmd.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if cmd.ProcessState == nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
				}
			}()
			ready := make(chan bool, 1)
			captured := make(chan string, 1)
			go func() {
				scanner := bufio.NewScanner(stderr)
				var output strings.Builder
				announced := false
				for scanner.Scan() {
					output.WriteString(scanner.Text() + "\n")
					if !announced && strings.Contains(scanner.Text(), readyMessage) {
						ready <- true
						announced = true
					}
				}
				if !announced {
					ready <- false
				}
				captured <- output.String()
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("server startup failed")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("server startup timed out")
			}
			if err := cmd.Process.Signal(tc.signal); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("exit = %v", err)
			}
			if output := <-captured; strings.Contains(output, "hint: context canceled") || strings.Contains(output, "hint: interrupt") || strings.Contains(output, "hint: terminated") {
				t.Fatalf("noisy cancellation: %s", output)
			}
		})
	}
}

func TestEngineDefaultsClock(t *testing.T) {
	env := environment{home: t.TempDir(), cwd: t.TempDir(), lookupEnv: func(key string) (string, bool) {
		values := map[string]string{"HINT_API_KEY": "fixture", "HINT_MODEL": "fixture"}
		value, ok := values[key]
		return value, ok
	}}
	eng, cleanup, err := newRPCEngine(context.Background(), options{env: env, debug: true}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if eng.setup.env.now == nil {
		t.Fatal("engine clock is nil")
	}
}

func TestSetupUsesInjectedEnvironment(t *testing.T) {
	home, cwd, configHome := t.TempDir(), t.TempDir(), t.TempDir()
	values := map[string]string{"XDG_CONFIG_HOME": configHome, "HINT_API_KEY": "fixture", "HINT_MODEL": "snapshot-model", "SNAPSHOT_KEY": "fixture"}
	env := environment{home: home, cwd: cwd, lookupEnv: func(key string) (string, bool) { value, ok := values[key]; return value, ok }}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HINT_MODEL", "process-model")
	t.Setenv("SNAPSHOT_KEY", "")
	info := newSetupInfoWithEnvironment(nil, config.Flags{}, env)
	shown, err := info.show()
	if err != nil || !strings.Contains(shown, "snapshot-model") || strings.Contains(shown, "process-model") {
		t.Fatalf("show = %q, %v", shown, err)
	}
	if got := mcpConsentPath(home, env.lookup); got != filepath.Join(configHome, "hint", "mcp-consent.json") {
		t.Fatalf("consent path = %s", got)
	}
	info.store = config.StoreFile
	delete(values, "SNAPSHOT_KEY")
	message, err := info.save("fixture", map[string]string{"api_key": "${SNAPSHOT_KEY}"})
	if err != nil || !strings.Contains(message, "SNAPSHOT_KEY is not set") {
		t.Fatalf("save = %q, %v", message, err)
	}
}
