package builtin

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGlobWindowMatchesFullSortOracle(t *testing.T) {
	var all []globHit
	for i := range 10000 {
		all = append(all, globHit{path: fmt.Sprintf("dir/%05d.go", i), mod: time.Unix(int64(i%97), 0)})
	}
	want := append([]globHit(nil), all...)
	oracle := func(a, b globHit) int {
		if n := b.mod.Compare(a.mod); n != 0 {
			return n
		}
		return strings.Compare(a.path, b.path)
	}
	slices.SortFunc(want, oracle)
	want = want[:maxGlobResults+1]
	for _, order := range []string{"ascending", "descending", "shuffled"} {
		t.Run(order, func(t *testing.T) {
			input := append([]globHit(nil), all...)
			switch order {
			case "descending":
				slices.Reverse(input)
			case "shuffled":
				rand.New(rand.NewPCG(1, 2)).Shuffle(len(input), func(i, j int) { input[i], input[j] = input[j], input[i] })
			}
			var got globHits
			for _, hit := range input {
				retainGlobHit(&got, hit)
				if len(got) > maxGlobResults+1 {
					t.Fatal("retained window grew with match count")
				}
			}
			slices.SortFunc(got, oracle)
			if !slices.Equal(got, want) {
				t.Fatalf("bounded window differs from full newest-first sort for %s", order)
			}
		})
	}
}

func TestGlobWindowPreservesShortResultsAndTruncationBoundary(t *testing.T) {
	for _, count := range []int{0, 1, 199, 200, 201, 202} {
		var got globHits
		for i := range count {
			retainGlobHit(&got, globHit{path: fmt.Sprintf("%04d", i), mod: time.Unix(0, 0)})
		}
		if len(got) != min(count, maxGlobResults+1) {
			t.Fatalf("count %d: retained %d", count, len(got))
		}
	}
}
