package app

import (
	"reflect"
	"strings"
	"testing"
)

func TestWizardSequentialFlow(t *testing.T) {
	w := NewWizard()
	if w.Step() != StepIntro {
		t.Fatalf("wizard must start at intro, got %d", w.Step())
	}
	if w.Source() != "langsmith" || w.DB() != "proofspan.sqlite" {
		t.Errorf("defaults: source=%q db=%q", w.Source(), w.DB())
	}

	// intro → source: free
	if err := w.Next(); err != nil {
		t.Fatal(err)
	}
	if w.Step() != StepSource {
		t.Fatalf("want StepSource, got %d", w.Step())
	}

	// source → analyze requires a file
	if err := w.Next(); err == nil {
		t.Fatal("Next without a file must refuse")
	}
	if err := w.SetFile("traces.jsonl"); err != nil {
		t.Fatal(err)
	}
	if err := w.SetSource("honeyhive"); err != nil {
		t.Fatal(err)
	}
	if err := w.Next(); err != nil {
		t.Fatal(err)
	}
	if w.Step() != StepAnalyze {
		t.Fatalf("want StepAnalyze, got %d", w.Step())
	}

	// walk to the end — every runnable step must be RUN before Next
	// advances: the wizard is a working guide, not a skipping aid.
	// We are ON StepAnalyze entering this loop.
	steps := []struct {
		run func() // mark the CURRENT step as run, like a front-end would
	}{
		{func() { w.RecordAnalyzed("report: ok") }},   // Analyze → Plan
		{func() { w.RecordPlanned("plan: ok") }},      // Plan → Migrate
		{func() { w.RecordMigrated("migrated: ok") }}, // Migrate → Eval
		{func() { w.RecordEval("eval: ok") }},         // Eval → Done
	}
	for _, s := range steps {
		if err := w.Next(); err == nil {
			t.Fatalf("Next from step %d before run must refuse", w.Step())
		}
		s.run()
		if err := w.Next(); err != nil {
			t.Fatalf("Next from step %d after run: %v", w.Step(), err)
		}
	}
	if !w.Finished() {
		t.Error("Finished must be true at StepDone")
	}
	// terminal: Next stays
	if err := w.Next(); err != nil || w.Step() != StepDone {
		t.Errorf("Next at Done must be a no-op, got %d err=%v", w.Step(), err)
	}

	// back walks back one page at a time
	w.Back()
	if w.Step() != StepEval {
		t.Errorf("Back from Done: got %d", w.Step())
	}
	w.Restart()
	if w.Step() != StepIntro {
		t.Errorf("Restart: got %d", w.Step())
	}
	// inputs survive a restart (a redo is cheap)
	if w.File() != "traces.jsonl" || w.Source() != "honeyhive" {
		t.Errorf("restart cleared inputs: file=%q source=%q", w.File(), w.Source())
	}
}

func TestWizardInputValidation(t *testing.T) {
	w := NewWizard()
	if err := w.SetSource("openllmetry"); err == nil {
		t.Error("bad source must error")
	}
	if err := w.SetFile(""); err == nil {
		t.Error("empty file must error")
	}
	if err := w.SetDB(""); err == nil {
		t.Error("empty db must error")
	}
}

func TestWizardCommandArgs(t *testing.T) {
	w := NewWizard()
	_ = w.SetFile("/tmp/traces.jsonl")
	_ = w.SetDB("/tmp/ps.sqlite")

	args, err := w.ReportCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(args, []string{"report", "--from=langsmith", "/tmp/traces.jsonl"}) {
		t.Errorf("report argv = %v", args)
	}

	args, err = w.PlanCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"migrate", "--from=langsmith", "--db=/tmp/ps.sqlite", "--dry-run", "/tmp/traces.jsonl"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("plan argv = %v, want %v", args, want)
	}
	if !strings.Contains(strings.Join(args, " "), "--dry-run") {
		t.Error("plan step must always be dry-run")
	}

	args, err = w.MigrateCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "--dry-run") {
		t.Error("migrate step must not be dry-run")
	}

	args, err = w.EvalCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	if args[0] != "eval" || !strings.Contains(strings.Join(args, " "), "--db=") {
		t.Errorf("eval argv = %v", args)
	}
}

func TestWizardSummaries(t *testing.T) {
	w := NewWizard()
	w.RecordAnalyzed("report: 10000/10000 parsed")
	w.RecordPlanned("plan: 400 trj / 10000 spans / 0 dropped")
	w.RecordMigrated("migrated 400 trajectories")
	w.RecordEval("eval: 400/400 pass")
	a, p, m, e := w.Summaries()
	if a == "" || p == "" || m == "" || e == "" {
		t.Errorf("summaries must all record: %q %q %q %q", a, p, m, e)
	}
}
