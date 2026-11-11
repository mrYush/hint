//go:build windows

package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/internal/tool/builtin"
	"github.com/mrYush/hint/pkg/agentapi"
	"golang.org/x/sys/windows"
)

// TestWindowsBashHelper runs inside the shell's process tree, using the test
// executable so the regressions do not depend on an installed scripting runtime.
func TestWindowsBashHelper(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" {
			if err := windowsBashHelper(os.Args[i+1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			os.Exit(0)
		}
	}
	t.Skip("only executed as a shell subprocess")
}

func windowsBashHelper(args []string) error {
	switch args[0] {
	case "args":
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetEscapeHTML(false)
		return encoder.Encode(args[1:])
	case "cwd":
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(dir)
	case "env":
		return json.NewEncoder(os.Stdout).Encode([]string{os.Getenv("KEEP_ME"), os.Getenv("APIBAR_KEY")})
	case "status":
		fmt.Fprintln(os.Stdout, "out")
		fmt.Fprintln(os.Stderr, "err")
		os.Exit(3)
	case "output":
		_, err := fmt.Fprint(os.Stdout, "head\n"+strings.Repeat("0123456789\n", 10000)+"tail\n")
		return err
	case "silent":
		return nil
	case "leaf":
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			return err
		}
		time.Sleep(time.Minute)
	case "tree", "tree-pipes":
		child := exec.Command(os.Args[0], "-test.run=TestWindowsBashHelper", "--", "leaf", args[1])
		if args[0] == "tree-pipes" {
			child.Stdout, child.Stderr = os.Stdout, os.Stderr
		}
		if err := child.Start(); err != nil {
			return err
		}
		if args[2] == "wait" {
			return child.Wait()
		}
		if err := child.Process.Release(); err != nil {
			return err
		}
		// The test opens the descendant's process handle before allowing this
		// parent to exit, so even normal-exit cleanup is directly observable.
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(args[2]); err == nil {
				return nil
			} else if !os.IsNotExist(err) {
				return err
			}
			time.Sleep(10 * time.Millisecond)
		}
		return fmt.Errorf("parent exit was not released")
	}
	return nil
}

func windowsBashCommand(executable string) string {
	return `"` + executable + `" -test.run=TestWindowsBashHelper -- `
}

func TestWindowsBashQuotesAndOperators(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The first token being a quoted path with spaces is a separate cmd.exe
	// quoting case from quoted arguments to an executable without spaces.
	copied := filepath.Join(t.TempDir(), "helper with spaces.exe")
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{executable, copied} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			tl := builtin.NewBash(newRoot(t, map[string]string{"input with spaces.txt": "from-file\r\n"}))
			helper := windowsBashCommand(path)
			for _, tt := range []struct {
				name, command, want string
			}{
				{"quoted args", helper + `args "TestX" "two words" "a&b|c" "C:\some path\file"`, `["TestX","two words","a&b|c","C:\\some path\\file"]`},
				{"surrounding space", "  " + helper + `args " spaced "  `, `[" spaced "]`},
				{"pipeline", `echo pipeline| ` + `findstr "pipeline"`, "pipeline"},
				{"redirect and chain", `type "input with spaces.txt" > "output with spaces.txt" && type "output with spaces.txt"`, "from-file"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					args, err := json.Marshal(map[string]string{"command": tt.command})
					if err != nil {
						t.Fatal(err)
					}
					got := ok(t, run(t, tl, string(args)))
					if strings.TrimSpace(got) != tt.want {
						t.Fatalf("output = %q, want %q", got, tt.want)
					}
				})
			}
		})
	}
}

func TestWindowsBashContracts(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := windowsBashCommand(executable)
	root := newRoot(t, nil)
	tl := builtin.NewBash(root)
	if !strings.Contains(tl.Description(), "cmd.exe syntax") {
		t.Fatalf("shell syntax missing from description: %q", tl.Description())
	}
	command := func(tl agentapi.Tool, script string) agentapi.ToolResult {
		t.Helper()
		args, err := json.Marshal(map[string]string{"command": script})
		if err != nil {
			t.Fatal(err)
		}
		return run(t, tl, string(args))
	}
	var cwd string
	if err := json.Unmarshal([]byte(ok(t, command(tl, helper+"cwd"))), &cwd); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(cwd, root.Dir()) {
		t.Fatalf("cwd = %q, want %q", cwd, root.Dir())
	}
	if got := ok(t, command(tl, helper+"silent")); got != "(no output)" {
		t.Fatalf("silent output = %q", got)
	}
	got := failed(t, command(tl, helper+"status"), "[exit code 3]")
	if got != "out\n--- stderr ---\nerr\n[exit code 3]" {
		t.Fatalf("stdout/stderr = %q", got)
	}
	t.Setenv("APIBAR_KEY", "sk-test-secret")
	env := func() []string {
		var out []string
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(strings.ToUpper(kv), "APIBAR_KEY=") && !strings.HasPrefix(strings.ToUpper(kv), "KEEP_ME=") {
				out = append(out, kv)
			}
		}
		return append(out, "KEEP_ME=yes")
	}
	tl = builtin.NewBash(root, builtin.WithEnviron(env))
	if got := strings.TrimSpace(ok(t, command(tl, helper+"env"))); got != `["yes",""]` {
		t.Fatalf("filtered environment = %q", got)
	}
	// cmd.exe emits CRLF; the helper emits LF, exercising bounded output
	// independently of the shell's own line-ending convention.
	tl = builtin.NewBash(root, builtin.WithLimits(tool.Limits{MaxOutput: 3000}))
	got = ok(t, command(tl, helper+"output"))
	if len(got) > 3000 || !strings.HasPrefix(got, "head\n") || !strings.HasSuffix(got, "tail\n") || !strings.Contains(got, "[output truncated:") {
		t.Fatalf("bounded output lost its contract: %q", got)
	}
}

func TestWindowsBashProcessTree(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"timeout", "cancel", "parent deadline", "normal exit", "normal exit with pipes"} {
		t.Run(mode, func(t *testing.T) {
			root := newRoot(t, nil)
			pidFile := filepath.Join(root.Dir(), "descendant.pid")
			release := filepath.Join(root.Dir(), "release")
			ctx, cancel := context.WithCancel(context.Background())
			if mode == "parent deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			}
			defer cancel()
			wait, helperMode, timeout := "wait", "tree", 30
			if strings.HasPrefix(mode, "normal exit") {
				wait = release
			}
			if mode == "normal exit with pipes" {
				helperMode = "tree-pipes"
			}
			if mode == "timeout" {
				timeout = 5
			}
			script := windowsBashCommand(executable) + helperMode + ` "` + pidFile + `" "` + wait + `"`
			args, err := json.Marshal(map[string]any{"command": script, "timeout": timeout})
			if err != nil {
				t.Fatal(err)
			}
			type outcome struct {
				result agentapi.ToolResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() {
				res, err := builtin.NewBash(root).Run(ctx, "tree", args)
				done <- outcome{res, err}
			}()
			// The leaf is a grandchild of the shell and does not inherit its
			// pipes except in the explicit WaitDelay case. A prompt return alone
			// therefore cannot make a broken shell-only kill pass this test.
			process := windowsBashDescendant(t, pidFile)
			if strings.HasPrefix(mode, "normal exit") {
				if err := os.WriteFile(release, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			} else if mode == "cancel" {
				cancel()
			}
			select {
			case got := <-done:
				if got.err != nil {
					t.Fatal(got.err)
				}
				switch mode {
				case "timeout":
					failed(t, got.result, "[timed out after 5s; process killed]")
				case "cancel", "parent deadline":
					failed(t, got.result, "[canceled]")
					if strings.Contains(got.result.Text(), "timed out") {
						t.Fatalf("caller cancellation reported as own timeout: %q", got.result.Text())
					}
				case "normal exit":
					ok(t, got.result)
				case "normal exit with pipes":
					if !strings.Contains(ok(t, got.result), "background child process was terminated") {
						t.Fatalf("missing background cleanup note: %q", got.result.Text())
					}
				}
			case <-time.After(15 * time.Second):
				t.Fatal("shell did not return after process-tree cleanup")
			}
			status, err := windows.WaitForSingleObject(process, 5000)
			if err != nil || status != windows.WAIT_OBJECT_0 {
				t.Fatalf("descendant survived: wait status %d, error %v", status, err)
			}
		})
	}
}

func windowsBashDescendant(t *testing.T, pidFile string) windows.Handle {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil && len(data) != 0 {
			pid, err := strconv.ParseUint(string(data), 10, 32)
			if err != nil {
				t.Fatal(err)
			}
			process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if status, err := windows.WaitForSingleObject(process, 0); err == nil && status == uint32(windows.WAIT_TIMEOUT) {
					if err := windows.TerminateProcess(process, 1); err != nil {
						t.Error(err)
					}
				}
				if err := windows.CloseHandle(process); err != nil {
					t.Error(err)
				}
			})
			return process
		}
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant did not report its PID")
	return 0
}
