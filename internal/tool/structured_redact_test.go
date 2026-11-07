package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
	"github.com/mrYush/hint/pkg/agentapi"
)

type structuredEcho struct{ echoTool }

func (structuredEcho) StructuredOutput() bool { return true }

func (structuredEcho) Run(_ context.Context, id string, args json.RawMessage) (agentapi.ToolResult, error) {
	return agentapi.TextResult(id, "echo", string(args)), nil
}

func TestStructuredRedactionPreservesJSONAndBudget(t *testing.T) {
	secret := "key-\"\\-界-secret"
	data, err := json.Marshal(map[string]any{
		"number": json.Number("18446744073709551615"),
		"values": []any{"before " + secret + " after", map[string]any{"nested": secret}},
	})
	if err != nil {
		t.Fatal(err)
	}
	wrapped := tool.Redacting(tool.WithLimits(structuredEcho{}, tool.Limits{MaxOutput: len(data)}), func() []string { return []string{secret} })
	result, err := wrapped.Run(context.Background(), "id", data)
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].Text
	if !json.Valid([]byte(text)) || len(text) > len(data) {
		t.Fatalf("broken JSON/budget: %s", text)
	}
	if strings.Contains(text, "secret") || !strings.Contains(text, "***") || !strings.Contains(text, "18446744073709551615") {
		t.Fatalf("redaction lost precision or leaked: %s", text)
	}
}

type budgetTool struct{ echoTool }

func (budgetTool) Run(ctx context.Context, id string, _ json.RawMessage) (agentapi.ToolResult, error) {
	data, err := json.Marshal(tool.OutputBudget(ctx))
	if err != nil {
		return agentapi.ToolResult{}, err
	}
	return agentapi.TextResult(id, "echo", string(data)), nil
}

func TestNestedOutputBudgetsUseSmallestPositiveLimit(t *testing.T) {
	for _, limits := range [][2]int{{1024, 8192}, {8192, 1024}, {0, 1024}, {1024, 0}, {0, 0}} {
		wrapped := tool.WithLimits(tool.WithLimits(budgetTool{}, tool.Limits{MaxOutput: limits[0]}), tool.Limits{MaxOutput: limits[1]})
		result, err := wrapped.Run(context.Background(), "id", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "1024"
		if limits[0] == 0 && limits[1] == 0 {
			want = "0"
		}
		if result.Content[0].Text != want {
			t.Fatalf("limits %v: %q", limits, result.Content[0].Text)
		}
	}
}
