// Proofspan CI module: `dagger call eval` is the gate.
//
// Runs the full CI chain in one alpine+go container (no services):
// test → corpus → fidelity → binary → migrate both sources → eval gate.
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/proofspan/internal/dagger"
)

// Proofspan builds and evaluates the Proofspan repo.
type Proofspan struct {
	// +default="1.27"
	GoVersion string
}

// New configures the module.
func New() *Proofspan {
	return &Proofspan{GoVersion: "1.27"}
}

// Eval runs the full CI gate: tests, corpus, fidelity, migration, eval,
// and a judge check that proves the builtin factual-consistency judge
// catches a planted defect and passes a clean trajectory.
// Returns the eval summary; a failed gate returns an error.
func (p *Proofspan) Eval(ctx context.Context, src *dagger.Directory) (string, error) {
	base := dag.Container().
		From(fmt.Sprintf("golang:%s-alpine", p.GoVersion)).
		WithWorkdir("/src").
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", src)

	// The planted-failure trajectory must FAIL the judge (exit 1) and the
	// clean trajectory must PASS (exit 0). Explicit if/else instead of
	// `; test $?` chains — GitHub Actions runs bash -e, where an unguarded
	// failing simple command aborts the script before the check runs.
	judgeCatch, err := base.
		WithExec([]string{"go", "test", "./..."}).
		WithExec([]string{"go", "run", "./cmd/corpusgen", "testdata/corpus"}).
		WithExec([]string{"go", "build", "-o", "/out/proofspan", "./cmd/proofspan"}).
		WithExec([]string{"/out/proofspan", "migrate", "--from=langsmith", "--db=/tmp/ps.sqlite", "testdata/corpus/langsmith/corpus.jsonl"}).
		WithExec([]string{"sh", "-c", `if /out/proofspan eval --db=/tmp/ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency > /dev/null 2>&1; then echo "planted trajectory unexpectedly passed"; exit 1; fi`}).
		WithExec([]string{"sh", "-c", `if ! /out/proofspan eval --db=/tmp/ps.sqlite --trajectory=trj_00003 --judges-run=factual-consistency > /dev/null 2>&1; then echo "clean trajectory failed the judge"; exit 1; fi`}).
		WithExec([]string{"echo", "judge gate: planted defect caught, clean trajectory passed"}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("judge gate failed: %w", err)
	}
	_ = judgeCatch

	out, err := base.
		WithExec([]string{"go", "test", "./..."}).
		WithExec([]string{"go", "run", "./cmd/corpusgen", "testdata/corpus"}).
		WithExec([]string{"go", "run", "./cmd/fidelitygen", "testdata/corpus", "docs/fidelity"}).
		WithExec([]string{"go", "build", "-o", "/out/proofspan", "./cmd/proofspan"}).
		WithExec([]string{"/out/proofspan", "migrate", "--from=langsmith", "--db=/tmp/ps2.sqlite", "testdata/corpus/langsmith/corpus.jsonl"}).
		WithExec([]string{"/out/proofspan", "migrate", "--from=honeyhive", "--db=/tmp/ps2.sqlite", "testdata/corpus/honeyhive/corpus.jsonl"}).
		WithExec([]string{"/out/proofspan", "eval", "--db=/tmp/ps2.sqlite"}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("eval gate failed: %w", err)
	}
	return strings.TrimSpace(out), nil
}
