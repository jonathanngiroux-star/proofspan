// Package eval runs pinned assertions over trajectories and aggregates
// the verdict. The eval gate: all pinned assertions pass or the eval fails.
package eval

import (
	"context"
	"time"

	"proofspan/internal/assert/wasm"
	"proofspan/internal/schema"
)

// AssertionRegistry is the subset of the WASM registry the runner needs.
type AssertionRegistry interface {
	Run(ctx context.Context, id, version string, spans []schema.Span) wasm.Result
}

// pin is one pinned assertion (id + version).
type pin struct {
	id      string
	version string
}

// Runner evaluates trajectories against pinned assertions.
type Runner struct {
	reg   AssertionRegistry
	pins  []pin
}

// NewRunner wires a registry; pin assertions via Pinned().
func NewRunner(reg AssertionRegistry) *Runner {
	return &Runner{reg: reg}
}

// Pinned registers an assertion to run on every eval.
func (r *Runner) Pinned(id, version string) {
	r.pins = append(r.pins, pin{id, version})
}

// TrajectoryReport is the eval outcome for one trajectory.
type TrajectoryReport struct {
	TrajectoryID string
	Results      []wasm.Result
	Total        int
	Passed       int
	Failed       int
	Errored      int
}

// Pass is true iff every assertion passed.
func (rep *TrajectoryReport) Pass() bool {
	return rep.Total > 0 && rep.Failed == 0 && rep.Errored == 0
}

// Status returns pass|fail|error for the whole trajectory.
func (rep *TrajectoryReport) Status() string {
	switch {
	case rep.Total == 0 || rep.Errored > 0:
		return "error"
	case rep.Failed > 0:
		return "fail"
	default:
		return "pass"
	}
}

// EvalTrajectory runs all pinned assertions over the spans, in pin order.
func (r *Runner) EvalTrajectory(ctx context.Context, spans []schema.Span) *TrajectoryReport {
	rep := &TrajectoryReport{}
	if len(spans) > 0 {
		rep.TrajectoryID = spans[0].TrajectoryID
	}
	for _, p := range r.pins {
		res := r.reg.Run(ctx, p.id, p.version, spans)
		rep.Results = append(rep.Results, res)
		rep.Total++
		switch res.Status {
		case "pass":
			rep.Passed++
		case "fail":
			rep.Failed++
		default:
			rep.Errored++
		}
	}
	return rep
}

// EvalAll evaluates every trajectory in the map and returns per-trajectory
// reports plus an overall verdict.
func (r *Runner) EvalAll(ctx context.Context, byTrajectory map[string][]schema.Span) ([]*TrajectoryReport, bool) {
	reports := make([]*TrajectoryReport, 0, len(byTrajectory))
	allPass := true
	for id, spans := range byTrajectory {
		rep := r.EvalTrajectory(ctx, spans)
		if rep.TrajectoryID == "" {
			rep.TrajectoryID = id
		}
		if !rep.Pass() {
			allPass = false
		}
		reports = append(reports, rep)
	}
	return reports, allPass
}

// NowMs returns the current epoch-ms (test seams override).
var NowMs = func() int64 { return time.Now().UnixMilli() }
