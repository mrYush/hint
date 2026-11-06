package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/mrYush/hint/pkg/agentapi"
)

func TestSingleRootKeepsOriginalToolCatalog(t *testing.T) {
	as, _, _, _ := testAssembly(t, &fakeChat{}, "")
	eng, err := newEngine(context.Background(), as)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.closeMCP)
	s := eng.newSession(nil, "catalog")
	baseline, err := json.Marshal(eng.registry.Schemas())
	if err != nil {
		t.Fatal(err)
	}
	var schemas []agentapi.ToolSchema
	for _, tool := range s.sessionTools() {
		schemas = append(schemas, agentapi.SchemaOf(tool))
	}
	current, err := json.Marshal(schemas)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, baseline) {
		t.Fatal("single-root workspace enlarged the original catalog")
	}
	t.Logf("baseline catalog: %d bytes, approximately %d tokens; startup prompt: %d bytes", len(baseline), (len(baseline)+3)/4, len(s.pre.initial()[0].Text()))
	if _, err := s.workspace.AttachLocal(t.TempDir(), "library"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range s.sessionTools() {
		found = found || tool.Name() == "repo_prepare"
	}
	if !found {
		t.Fatal("secondary instruction preparation was not disclosed after attachment")
	}
}
