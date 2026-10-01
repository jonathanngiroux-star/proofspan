package main

import (
	"reflect"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/migrate/langsmith"
	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

// flattenLS/flattenHH must emit spans in a DETERMINISTIC order (sorted by
// span id) no matter the Go map iteration order: fidelitygen feeds the
// flattened slice into CostDrift, whose float64 summation is not
// associative — a varying order churns the committed
// docs/fidelity/summary.json on every regen with last-bit noise that
// looks like real cost drift in review.
func TestFlattenLSSortedDeterministic(t *testing.T) {
	res := &langsmith.Result{SpansByTrajectory: map[string][]schema.Span{
		"trj_b": {
			{SpanID: "spn_2", TrajectoryID: "trj_b", CostUSD: 0.1},
			{SpanID: "spn_1", TrajectoryID: "trj_b", CostUSD: 0.2},
		},
		"trj_a": {
			{SpanID: "spn_0", TrajectoryID: "trj_a", CostUSD: 0.3},
		},
	}}
	got := flattenLS(res)
	want := []string{"spn_0", "spn_1", "spn_2"}
	gotIDs := make([]string, len(got))
	for i, s := range got {
		gotIDs[i] = s.SpanID
	}
	if !reflect.DeepEqual(gotIDs, want) {
		t.Fatalf("flatten order must be sorted by span id (deterministic), got %v", gotIDs)
	}
}
