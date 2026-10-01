package app

import (
	"reflect"
	"testing"
)

// The GUI builds CLI invocations; these tests pin the exact argv the GUI
// produces for every command. If the CLI surface changes, these fail first.

func TestReportArgs(t *testing.T) {
	got, err := ReportArgs(ReportConfig{Source: "langsmith", File: "traces.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"report", "--from=langsmith", "traces.jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("basic report: got %v want %v", got, want)
	}

	got, err = ReportArgs(ReportConfig{Source: "honeyhive", File: "hh.jsonl", OutDir: "docs/corpus"})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"report", "--from=honeyhive", "--out=docs/corpus", "hh.jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("report with out dir: got %v want %v", got, want)
	}
}

func TestReportArgsValidation(t *testing.T) {
	if _, err := ReportArgs(ReportConfig{Source: "bogus", File: "x"}); err == nil {
		t.Error("unknown source must be rejected")
	}
	if _, err := ReportArgs(ReportConfig{Source: "langsmith", File: ""}); err == nil {
		t.Error("missing file must be rejected")
	}
}

func TestMigrateArgs(t *testing.T) {
	got, err := MigrateArgs(MigrateConfig{Source: "langsmith", File: "t.jsonl", DB: "ps.sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"migrate", "--from=langsmith", "--db=ps.sqlite", "t.jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("write path: got %v want %v", got, want)
	}

	got, err = MigrateArgs(MigrateConfig{Source: "langsmith", File: "t.jsonl", DB: "ps.sqlite", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"migrate", "--from=langsmith", "--db=ps.sqlite", "--dry-run", "t.jsonl"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("dry-run: got %v want %v", got, want)
	}
}

func TestEvalArgs(t *testing.T) {
	// defaults: all trajectories, default assertion pin, no judges
	got, err := EvalArgs(EvalConfig{DB: "ps.sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"eval", "--db=ps.sqlite"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("bare eval: got %v want %v", got, want)
	}

	got, err = EvalArgs(EvalConfig{
		DB: "ps.sqlite", Trajectory: "trj_00001",
		JudgesRun:     "factual-consistency,semantic-consistency",
		JudgeEndpoint: "http://localhost:8000/v1", JudgeAPIKey: "sk-x",
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"eval", "--db=ps.sqlite", "--trajectory=trj_00001",
		"--judges-run=factual-consistency,semantic-consistency",
		"--judge-endpoint=http://localhost:8000/v1", "--judge-api-key=sk-x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("full eval: got %v want %v", got, want)
	}
}

func TestServeArgs(t *testing.T) {
	got, env, err := ServeArgs(ServeConfig{DB: "ps.sqlite", Addr: "127.0.0.1:7400"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"serve", "--db=ps.sqlite", "--addr=127.0.0.1:7400"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serve no scim: got %v want %v", got, want)
	}
	if env["PROOFSPAN_SCIM_TOKEN"] != "" {
		t.Errorf("no token: env must be empty, got %v", env)
	}

	got, env, err = ServeArgs(ServeConfig{DB: "ps.sqlite", Addr: ":7400", SCIM: true, SCIMToken: "tok-1"})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"serve", "--db=ps.sqlite", "--addr=:7400", "--scim"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("serve scim: got %v want %v", got, want)
	}
	if env["PROOFSPAN_SCIM_TOKEN"] != "tok-1" {
		t.Errorf("scim token must go to env (never argv): got %v", env)
	}
}

func TestResolveBinary(t *testing.T) {
	t.Setenv("PROOFSPAN_BIN", "/opt/custom/proofspan")
	if got := ResolveBinary(); got != "/opt/custom/proofspan" {
		t.Errorf("PROOFSPAN_BIN override ignored: %q", got)
	}
	t.Setenv("PROOFSPAN_BIN", "")
	// falls back to a name resolvable via PATH at exec time
	if got := ResolveBinary(); got != "proofspan" {
		t.Errorf("default binary name: %q", got)
	}
}
