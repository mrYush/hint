package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/rpc"
)

// The optional capability is implemented only by this test engine extension;
// existing engines remain valid RPC engines without it.
func (e *fakeEngine) Repos(_ context.Context, req rpc.RepoRequest) (rpc.RepoResult, error) {
	return rpc.RepoResult{Text: "Workspace revision 1\nprimary", Revision: 1}, nil
}

func TestReposCommandAndBusyGuard(t *testing.T) {
	h := newHarness(t, &fakeEngine{}, Options{})
	h.run(h.submit("/repos"))
	if !strings.Contains(h.view(), "Workspace revision 1") {
		t.Fatalf("missing workspace output: %s", h.view())
	}
	h.m.busy = true
	if cmd := h.submit("/repos add /tmp/new"); cmd != nil {
		t.Fatal("workspace command ran during turn")
	}
	if !strings.Contains(h.m.status, "not available") {
		t.Fatalf("busy status: %s", h.m.status)
	}
	if !strings.Contains(helpText(), "/repos") {
		t.Fatal("help omits /repos")
	}
}
