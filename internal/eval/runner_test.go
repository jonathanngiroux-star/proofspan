package eval

import (
	"context"
	"testing"

	"proofspan/internal/assert/wasm"
	"proofspan/internal/schema"
)

// fake registry for runner logic tests — WASM execution is covered in
// the wasm package's own suite against the real module.
type fakeRegistry struct{ failID string }

func (f *fakeRegistry) Run(_ context.Context, id, version string, _ []schema.Span) wasm.Result {
	if id == f.failID {
		return wasm.Result{AssertionID: id, AssertionVersion: version, Status: "fail", Detail: "injected failure"}
	}
	if id == "missing-assertion" {
		// mirror the real registry: unknown id → error, never silent pass
		return wasm.Result{AssertionID: id, AssertionVersion: version, Status: "error", Detail: "unknown assertion"}
	}
	return wasm.Result{AssertionID: id, AssertionVersion: version, Status: "pass"}
}

func TestRunnerGreenTrajectoryPasses(t *testing.T) {
	r := NewRunner(&fakeRegistry{})
	r.Pinned("span-correlation", "1.0.0")
	res := r.EvalTrajectory(context.Background(), []schema.Span{{SpanID: "s1", TrajectoryID: "t1"}})
	if !res.Pass() {
		t.Errorf("green trajectory must pass: %+v", res)
	}
	if res.Total != 1 || res.Passed != 1 || res.Failed != 0 {
		t.Errorf("counts wrong: %+v", res)
	}
}

func TestRunnerFailingAssertionFailsEval(t *testing.T) {
	r := NewRunner(&fakeRegistry{failID: "span-correlation"})
	r.Pinned("span-correlation", "1.0.0")
	res := r.EvalTrajectory(context.Background(), []schema.Span{{SpanID: "s1"}})
	if res.Pass() {
		t.Error("failing assertion must fail the eval")
	}
	if res.Failed != 1 {
		t.Errorf("failed count = %d", res.Failed)
	}
}

func TestRunnerNoAssertionsIsError(t *testing.T) {
	r := NewRunner(&fakeRegistry{})
	res := r.EvalTrajectory(context.Background(), []schema.Span{{SpanID: "s1"}})
	if res.Pass() {
		t.Error("no pinned assertions must not pass silently")
	}
	if res.Status() != "error" {
		t.Errorf("status = %s, want error", res.Status())
	}
}

func TestRunnerErrorAssertionFails(t *testing.T) {
	fr := &fakeRegistry{}
	r := NewRunner(fr)
	r.Pinned("missing-assertion", "1.0.0") // registry has no such id → error
	res := r.EvalTrajectory(context.Background(), []schema.Span{{SpanID: "s1"}})
	if res.Pass() {
		t.Error("assertion error must not pass")
	}
}

func TestRunnerMultipleAssertionsAllCounted(t *testing.T) {
	fr := &fakeRegistry{failID: "b"}
	r := NewRunner(fr)
	r.Pinned("a", "1.0.0")
	r.Pinned("b", "1.0.0")
	r.Pinned("c", "1.0.0")
	res := r.EvalTrajectory(context.Background(), []schema.Span{{SpanID: "s1"}})
	if res.Total != 3 || res.Passed != 2 || res.Failed != 1 {
		t.Errorf("counts wrong: total=%d passed=%d failed=%d", res.Total, res.Passed, res.Failed)
	}
}
