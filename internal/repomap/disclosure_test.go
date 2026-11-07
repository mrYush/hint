package repomap

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mrYush/hint/internal/tool"
)

func TestToolDisclosureUsesEffectiveOutputLimit(t *testing.T) {
	m, space, _ := queryFixture(t)
	if err := m.SetMode(ModeOn); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []int{1, 64, 512, 1023, 1024, 2048, 0} {
		t.Run(fmt.Sprint(cap), func(t *testing.T) {
			tools := m.ToolsWithLimits(space, tool.Limits{MaxOutput: cap})
			low := cap > 0 && cap < MinOutputBytes
			want := 2
			if low {
				want = 1
			}
			if len(tools) != want {
				t.Fatalf("disclosed %d tools at %d", len(tools), cap)
			}
			list := tools[0]
			if strings.Contains(list.Description(), "budget below 1024") != low {
				t.Fatal(list.Description())
			}
			result, err := list.Run(t.Context(), "status", json.RawMessage(`{"status":true}`))
			if err != nil {
				t.Fatal(err)
			}
			if low && cap >= 64 && (result.IsError || !strings.Contains(result.Text(), "output_budget_below_1024")) {
				t.Fatalf("status: %s", result.Text())
			}
			if !low {
				runMap(t, tools[1], `{}`, MaxOutputBytes)
			}
		})
	}
}
