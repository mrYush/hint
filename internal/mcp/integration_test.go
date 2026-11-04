package mcp

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/mrYush/hint/pkg/agentapi"
)

// TestReferenceEverything exercises the reference server without allowing npx
// to download it. The probe distinguishes an unavailable local runtime from a
// failure after a real connection, which must fail the test.
func TestReferenceEverything(t *testing.T) {
	npx, err := exec.LookPath("npx")
	if err != nil {
		t.Skip("the everything reference server needs npx")
	}
	cfg := ServerConfig{
		Name: "everything", Transport: TransportStdio,
		Command:    npx,
		Args:       []string{"--no-install", "@modelcontextprotocol/server-everything"},
		Env:        map[string]string{"npm_config_offline": "true"},
		Permission: agentapi.ClassRead,
		Timeout:    15 * time.Second,
		Resources:  []string{"test://static/resource/1"},
	}
	environ := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 15*time.Second)
	probe, err := (&connector{info: ClientInfo{Name: "hint-test", Version: "v0"}, environ: environ}).dial(probeCtx, cfg)
	cancelProbe()
	if err != nil {
		t.Skipf("the everything reference server is not available locally without network access: %v", err)
	}
	_ = probe.Close()

	m, err := NewManager([]ServerConfig{cfg}, Options{
		Environ: environ, ConnectTimeout: 15 * time.Second, ListTimeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	ctx := context.Background()

	tools, err := m.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools: %v", err)
	}
	var sum agentapi.Tool
	for _, tl := range tools {
		if tl.Name() == "everything.get-sum" {
			sum = tl
		}
	}
	if sum == nil {
		t.Fatalf("everything.get-sum not registered: %v", toolNames(tools))
	}
	result, err := sum.Run(ctx, "sum", json.RawMessage(`{"a":2,"b":3}`))
	if err != nil || result.IsError || !strings.Contains(result.Text(), "5") {
		t.Errorf("get-sum = %+v, %v", result, err)
	}

	resources, err := m.ConfiguredResources(ctx)
	if err != nil {
		t.Fatalf("ConfiguredResources: %v", err)
	}
	if len(resources) != 1 || strings.TrimSpace(resources[0].Text) == "" {
		t.Errorf("configured resources = %+v", resources)
	}

	prompts, err := m.Prompts(ctx)
	if err != nil {
		t.Fatalf("Prompts: %v", err)
	}
	found := false
	for _, prompt := range prompts {
		if prompt.Name == "simple_prompt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("simple_prompt not listed: %+v", prompts)
	}
	text, err := m.ExpandPrompt(ctx, "everything", "simple_prompt", nil)
	if err != nil || strings.TrimSpace(text) == "" {
		t.Errorf("simple_prompt = %q, %v", text, err)
	}
}
