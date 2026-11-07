package project

import "context"

// LayoutSources fits already-authorized documents without filesystem discovery.
// The caller owns read limits and source authority; the existing outline ladder
// and shared byte budget are the same as for startup instruction documents.
func LayoutSources(ctx context.Context, sources []InstructionSource, budget int) ([]Instruction, []string) {
	return layoutInstructions(ctx, nil, nil, sources, budget, nil, nil)
}
