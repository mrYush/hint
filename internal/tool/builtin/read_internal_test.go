package builtin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestNumberLinesStopsWhenCanceledPastLimit covers the counting pass that
// runs when the requested window starts past the end of the file: the
// caller's cancellation must still end it.
func TestNumberLinesStopsWhenCanceledPastLimit(t *testing.T) {
	var file strings.Builder
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&file, "line %d\n", i)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, _, err := numberLines(ctx, strings.NewReader(file.String()), 5000, 100)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
