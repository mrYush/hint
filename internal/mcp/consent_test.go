package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestConsentStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consent.json")
	store := NewConsentStore(path)
	root := "/tmp/project"
	cfg := ServerConfig{
		Name:       "proj",
		Transport:  TransportStdio,
		Command:    "npx",
		Args:       []string{"server"},
		Permission: agentapi.ClassRead,
		Project:    true,
	}

	if ok, reason := store.Check(root, cfg); ok || !strings.Contains(reason, "consent is recorded") {
		t.Fatalf("Check before Enable = %v, %q; want disabled", ok, reason)
	}
	if err := store.Enable(root, cfg); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if ok, reason := store.Check(root, cfg); !ok {
		t.Fatalf("Check after Enable = false, %q", reason)
	}
	if ok, _ := store.Check("/tmp/other-project", cfg); ok {
		t.Error("a record for one project approved another")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("consent file mode = %o, want 600", got)
		}
	}

	changed := cfg
	changed.Args = []string{"server", "--evil"}
	if ok, reason := store.Check(root, changed); ok || !strings.Contains(reason, "consent changed (args)") {
		t.Fatalf("Check after an args change = %v, %q; want args named", ok, reason)
	}
	changed.Permission = agentapi.ClassExecute
	if ok, reason := store.Check(root, changed); ok || !strings.Contains(reason, "consent changed (args)") {
		t.Fatalf("Check after permission change = %v, %q", ok, reason)
	}
	if err := store.Enable(root, changed); err != nil {
		t.Fatalf("Enable after change: %v", err)
	}
	if ok, _ := store.Check(root, changed); !ok {
		t.Error("Check after re-enabling a changed server = false")
	}

	global := cfg
	global.Project = false
	if ok, _ := store.Check(root, global); !ok {
		t.Error("a global server should not need consent")
	}
}

func TestConsentStoreUnreadable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mcp-consent.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewConsentStore(path)
	ok, reason := store.Check("/tmp/project", ServerConfig{Name: "p", Project: true})
	if ok || !strings.Contains(reason, "consent record") {
		t.Errorf("Check on a corrupt file = %v, %q", ok, reason)
	}
	if err := store.Enable("/tmp/project", ServerConfig{Name: "p", Project: true}); err == nil {
		t.Error("Enable on a corrupt file returned no error")
	}

	empty := NewConsentStore("")
	if err := empty.Enable("/tmp/project", ServerConfig{Name: "p", Project: true}); err == nil {
		t.Error("Enable without a path returned no error")
	}
	if ok, _ := empty.Check("/tmp/project", ServerConfig{Name: "p", Project: true}); ok {
		t.Error("a store without a path approved a project server")
	}
}

func TestManagerProjectConsent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "mcp-consent.json")
	store := NewConsentStore(path)
	server := newFakeServer(t, echoTool())
	cfg := ServerConfig{Name: "proj", Transport: TransportStdio, Command: "unused", Project: true}

	m, err := NewManager([]ServerConfig{cfg}, Options{
		Dial:        newMemoryDial(server).dial,
		Consent:     store,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	if _, err := m.Tools(ctx); err != nil {
		t.Fatalf("Tools: %v", err)
	}
	if !hasWarning(m, "disabled until consent") {
		t.Errorf("warnings = %v; want the consent warning", m.Warnings())
	}

	if err := store.Enable(root, cfg); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	enabled, err := NewManager([]ServerConfig{cfg}, Options{
		Dial:        newMemoryDial(server).dial,
		Consent:     store,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("NewManager after Enable: %v", err)
	}
	t.Cleanup(func() { _ = enabled.Close() })
	tools, err := enabled.Tools(ctx)
	if err != nil {
		t.Fatalf("Tools after Enable: %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("tools = %v; want the project server", toolNames(tools))
	}

	changed := cfg
	changed.Command = "other"
	rejected, err := NewManager([]ServerConfig{changed}, Options{
		Dial:        newMemoryDial(server).dial,
		Consent:     store,
		ProjectRoot: root,
	})
	if err != nil {
		t.Fatalf("NewManager after change: %v", err)
	}
	t.Cleanup(func() { _ = rejected.Close() })
	if _, err := rejected.Tools(ctx); err != nil {
		t.Fatalf("Tools after change: %v", err)
	}
	if !hasWarning(rejected, "consent changed (command)") {
		t.Errorf("warnings = %v; want the changed field named", rejected.Warnings())
	}
}
