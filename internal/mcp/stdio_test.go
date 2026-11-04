package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mrYush/hint/pkg/agentapi"
)

// TestMain doubles as the stdio server of TestStdioServerLifecycle. The
// test binary is started again as a child with HINT_MCP_TEST_SERVER=1;
// that process speaks MCP on stdin and stdout instead of running tests.
func TestMain(m *testing.M) {
	if os.Getenv("HINT_MCP_TEST_SERVER") == "1" {
		runTestServer()
		return
	}
	os.Exit(m.Run())
}

func runTestServer() {
	s := sdk.NewServer(&sdk.Implementation{Name: "stdio-helper", Version: "v0"}, nil)
	add := func(name, description string, handler func(context.Context, json.RawMessage) (*sdk.CallToolResult, error)) {
		s.AddTool(&sdk.Tool{Name: name, Description: description, InputSchema: objectSchema()}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return handler(ctx, req.Params.Arguments)
		})
	}
	add("echo", "Echo the arguments.", func(_ context.Context, args json.RawMessage) (*sdk.CallToolResult, error) {
		return textResult(string(args)), nil
	})
	add("env", "Show the declared and inherited environment.", func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) {
		return textResult(os.Getenv("DECLARED") + "|" + os.Getenv("SECRET")), nil
	})
	add("sleep", "Sleep for a while.", func(ctx context.Context, _ json.RawMessage) (*sdk.CallToolResult, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return textResult("done"), nil
		}
	})
	add("big", "Return a large text.", func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) {
		return textResult(strings.Repeat("x", 60_000)), nil
	})
	add("crash", "Exit the process.", func(context.Context, json.RawMessage) (*sdk.CallToolResult, error) {
		os.Exit(7)
		return nil, nil
	})
	_ = s.Run(context.Background(), &sdk.StdioTransport{})
}

func stdioConfig(timeout time.Duration) ServerConfig {
	return ServerConfig{
		Name:       "child",
		Transport:  TransportStdio,
		Command:    os.Args[0],
		Permission: agentapi.ClassRead,
		Timeout:    timeout,
	}
}

func newStdioManager(t *testing.T, cfg ServerConfig, environ []string) *Manager {
	t.Helper()
	m, err := NewManager([]ServerConfig{cfg}, Options{
		Environ: environ,
		// The child is this test binary; it becomes the server when the
		// variable is set.
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func toolByName(tools []agentapi.Tool, name string) agentapi.Tool {
	for _, t := range tools {
		if t.Name() == name {
			return t
		}
	}
	return nil
}

func TestStdioServerLifecycle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base := []string{"PATH=" + os.Getenv("PATH"), "SECRET=base-secret"}
	cfg := stdioConfig(5 * time.Second)
	cfg.Permission = agentapi.ClassExecute
	cfg.Env = map[string]string{"DECLARED": "declared", "HINT_MCP_TEST_SERVER": "1"}
	m := newStdioManager(t, cfg, base)
	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if len(tools) != 5 {
		t.Fatalf("tools = %v", toolNames(tools))
	}
	echo := toolByName(tools, "child.echo")
	if echo == nil {
		t.Fatal("child.echo is not registered")
	}
	res, err := echo.Run(ctx, "call-1", json.RawMessage(`{"text":"hi"}`))
	if err != nil {
		t.Fatalf("Run echo: %v", err)
	}
	if res.IsError || res.Text() != `{"text":"hi"}` {
		t.Errorf("echo result = %+v", res)
	}

	env := toolByName(tools, "child.env")
	res, err = env.Run(ctx, "call-2", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run env: %v", err)
	}
	if got := res.Text(); got != "declared|base-secret" {
		t.Errorf("env = %q; want the declared and inherited values", got)
	}

	// Crash the child, then reconnect and call a read tool. The execute
	// call that killed it is not retried and reports the ambiguity.
	crash := toolByName(tools, "child.crash")
	res, err = crash.Run(ctx, "call-3", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run crash: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "may have reached") {
		t.Errorf("crash result = %+v; want the ambiguity error", res)
	}
	res, err = echo.Run(ctx, "call-4", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run echo after crash: %v", err)
	}
	if res.IsError {
		t.Errorf("echo after crash = %+v", res)
	}
}

func TestStdioTimeoutAndTruncation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := stdioConfig(30 * time.Millisecond)
	cfg.Env = map[string]string{"HINT_MCP_TEST_SERVER": "1"}
	m := newStdioManager(t, cfg, []string{"PATH=" + os.Getenv("PATH")})
	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	sleep := toolByName(tools, "child.sleep")
	res, err := sleep.Run(ctx, "call-1", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run sleep: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "timed out") {
		t.Errorf("sleep result = %+v; want a timeout", res)
	}

	big := toolByName(tools, "child.big")
	res, err = big.Run(ctx, "call-2", json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Run big: %v", err)
	}
	if res.IsError {
		t.Errorf("big result is an error: %+v", res)
	}
	if got := len(res.Text()); got >= 60_000 {
		t.Errorf("big result length = %d; want the default output cap", got)
	}
}

func TestProjectEnviron(t *testing.T) {
	base := []string{
		"PATH=/bin", "HOME=/home/u", "LC_ALL=C", "LANG=en",
		"SECRET=one", "GITHUB_TOKEN=two", "AWS_SECRET_ACCESS_KEY=three",
	}
	got := projectEnviron(base)
	joined := strings.Join(got, "\n")
	for _, want := range []string{"PATH=/bin", "HOME=/home/u", "LC_ALL=C", "LANG=en"} {
		if !strings.Contains(joined, want) {
			t.Errorf("projectEnviron = %v; missing %q", got, want)
		}
	}
	for _, secret := range []string{"SECRET", "GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY"} {
		if strings.Contains(joined, secret) {
			t.Errorf("projectEnviron = %v; leaked %s", got, secret)
		}
	}
}

func TestMergeEnvOverrides(t *testing.T) {
	got := mergeEnv([]string{"A=1", "B=2"}, map[string]string{"B": "3", "C": "4"})
	joined := strings.Join(got, "\n")
	for _, want := range []string{"A=1", "B=3", "C=4"} {
		if !strings.Contains(joined, want) {
			t.Errorf("mergeEnv = %v; missing %q", got, want)
		}
	}
	if strings.Contains(joined, "B=2") {
		t.Errorf("mergeEnv = %v; the base B was not overridden", got)
	}
}
