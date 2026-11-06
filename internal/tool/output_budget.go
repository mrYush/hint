package tool

import "context"

type outputBudgetKey struct{}

// OutputBudget returns the active outer text limit, or zero when unlimited.
// Structured tools use it to build complete records before generic truncation.
func OutputBudget(ctx context.Context) int {
	n, _ := ctx.Value(outputBudgetKey{}).(int)
	return n
}
