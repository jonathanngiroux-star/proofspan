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

// Eval runs the full CI gate: tests, corpus, fidelity, migration, eval.
// Returns the eval summary; a failed gate returns an error.
func (p *Proofspan) Eval(ctx context.Context, src *dagger.Directory) (string, error) {
	base := dag.Container().
		From(fmt.Sprintf("golang:%s-alpine", p.GoVersion)).
		WithWorkdir("/src").
		WithEnvVariable("CGO_ENABLED", "0").
		WithDirectory("/src", src)

	out, err := base.
		WithExec([]string{"go", "test", "./..."}).
		WithExec([]string{"go", "run", "./cmd/corpusgen", "testdata/corpus"}).
		WithExec([]string{"go", "run", "./cmd/fidelitygen", "testdata/corpus", "docs/fidelity"}).
		WithExec([]string{"go", "build", "-o", "/out/proofspan", "./cmd/proofspan"}).
		WithExec([]string{"/out/proofspan", "migrate", "--from=langsmith", "--db=/tmp/ps.sqlite", "testdata/corpus/langsmith/corpus.jsonl"}).
		WithExec([]string{"/out/proofspan", "migrate", "--from=honeyhive", "--db=/tmp/ps.sqlite", "testdata/corpus/honeyhive/corpus.jsonl"}).
		WithExec([]string{"/out/proofspan", "eval", "--db=/tmp/ps.sqlite"}).
		Stdout(ctx)
	if err != nil {
		return "", fmt.Errorf("eval gate failed: %w", err)
	}
	return strings.TrimSpace(out), nil
}
