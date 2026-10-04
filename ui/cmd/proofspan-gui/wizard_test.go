package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWizardWalksEveryStepEndToEnd pins the product requirement: the wizard
// is a WORKING guide. Every step must carry what the user needs — the
// source step shows its inputs, every runnable step RUNS the real command
// and reports a real summary with a real output tail — and the flow must
// actually produce the migrated DB and pass the eval gate, end to end,
// against the real corpus.
func TestWizardWalksEveryStepEndToEnd(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	corpus := filepath.Join(home, "testdata", "corpus", "langsmith", "corpus.jsonl")
	db := t.TempDir() + "/wizard.sqlite"

	// Start: intro page, no inputs, not runnable
	v := g.WizardStart()
	if v.Step != 0 || v.Runnable {
		t.Fatalf("intro must be step 0 and not runnable: %+v", v)
	}

	// Intro → Source: inputs must be visible on this step
	v, err := g.WizardNext()
	if err != nil {
		t.Fatal(err)
	}
	if v.Step != 1 {
		t.Fatalf("want source step, got %d", v.Step)
	}
	if !v.NeedsFile {
		t.Fatal("source step must show the file/source/db inputs — the bug the user hit")
	}
	if v.Runnable {
		t.Error("source step has nothing to run")
	}

	// Next without a file must refuse (backend validation)
	if _, err := g.WizardNext(); err == nil {
		t.Fatal("Next without a file must refuse")
	}

	// Set the inputs (as Browse + typing would)
	if _, err := g.WizardSetInputs("langsmith", corpus, db); err != nil {
		t.Fatal(err)
	}

	// Analyze step: runnable, refuses to advance until run, real summary
	if v, err = g.WizardNext(); err != nil {
		t.Fatal(err)
	}
	if v.Step != 2 || !v.Runnable {
		t.Fatalf("analyze must be runnable: %+v", v)
	}
	if _, err := g.WizardNext(); err == nil {
		t.Fatal("runnable step must refuse Next before the step ran")
	}
	v, err = g.WizardRunStep()
	if err != nil {
		t.Fatalf("run analyze: %v", err)
	}
	if !strings.Contains(v.Summary, "10000/10000 lines parsed") {
		t.Errorf("analyze summary must carry real numbers: %q", v.Summary)
	}
	if len(v.Tail) == 0 {
		t.Error("analyze step must show an output tail inside the modal")
	}

	// Plan step: dry-run, real counts, and NO db file written
	if v, err = g.WizardNext(); err != nil {
		t.Fatal(err)
	}
	if v.Step != 3 || !v.Runnable {
		t.Fatalf("plan must be runnable: %+v", v)
	}
	v, err = g.WizardRunStep()
	if err != nil {
		t.Fatalf("run plan: %v", err)
	}
	if !strings.Contains(v.Summary, "400 trajectories / 10000 spans") {
		t.Errorf("plan summary must carry real counts: %q", v.Summary)
	}
	if _, statErr := os.Stat(db); !os.IsNotExist(statErr) {
		t.Error("dry-run step must not write the DB")
	}

	// Migrate step: writes the DB, summary scraped from the real line
	if v, err = g.WizardNext(); err != nil {
		t.Fatal(err)
	}
	if v.Step != 4 || !v.Runnable {
		t.Fatalf("migrate must be runnable: %+v", v)
	}
	v, err = g.WizardRunStep()
	if err != nil {
		t.Fatalf("run migrate: %v", err)
	}
	if !strings.Contains(v.Summary, "migrated 400 trajectories") {
		t.Errorf("migrate summary must be the real line: %q", v.Summary)
	}
	if _, statErr := os.Stat(db); statErr != nil {
		t.Fatalf("migrate must write the DB: %v", statErr)
	}

	// Eval step: the gate, real pass count
	if v, err = g.WizardNext(); err != nil {
		t.Fatal(err)
	}
	if v.Step != 5 || !v.Runnable {
		t.Fatalf("eval must be runnable: %+v", v)
	}
	v, err = g.WizardRunStep()
	if err != nil {
		t.Fatalf("run eval: %v", err)
	}
	if !strings.Contains(v.Summary, "400/400 trajectories pass") {
		t.Errorf("eval summary must carry the real verdict: %q", v.Summary)
	}

	// Done: checklist with all four recorded summaries
	if v, err = g.WizardNext(); err != nil {
		t.Fatal(err)
	}
	if v.Step != 6 {
		t.Fatalf("want done, got %d", v.Step)
	}
	done := g.WizardGetDone()
	for _, k := range []string{"analyzed", "planned", "migrated", "eval"} {
		if done[k] == "" {
			t.Errorf("done checklist missing %s", k)
		}
	}

	// the DB the wizard built must actually serve trajectories (product truth)
	ids, err := g.ListTrajectories(db)
	if err != nil {
		t.Fatalf("ListTrajectories on wizard DB: %v", err)
	}
	if len(ids) != 400 {
		t.Errorf("wizard DB must hold 400 trajectories, got %d", len(ids))
	}
}

// TestWizardSeenMarkerLifecycle pins the Skip fix: the backend marker file
// (not webview localStorage, which WebKitGTK can block) is the source of
// truth for "the user dismissed the walkthrough".
func TestWizardSeenMarkerLifecycle(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "config"))
	g := newGUIForTest()

	if g.WizardHasSeen() {
		t.Fatal("fresh config must not be seen")
	}
	g.WizardMarkSeen()
	if !g.WizardHasSeen() {
		t.Fatal("MarkSeen must create the marker file")
	}
	marker := filepath.Join(dir, "config", "proofspan", "wizard-seen")
	if b, err := os.ReadFile(marker); err != nil || len(b) == 0 {
		t.Errorf("marker file must exist and be non-empty: %v", err)
	}
}

// TestWizardStepFieldsSerializeForFrontend pins the JS contract that broke
// the first launch: the Go field NeedsFile must serialize as needsFile so
// the frontend's v.needsFile read works.
func TestWizardStepFieldsSerializeForFrontend(t *testing.T) {
	g := newGUIForTest()
	b, err := json.Marshal(g.WizardStart())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"needsFile"`, `"runnable"`, `"ranStep"`, `"running"`, `"summary"`, `"tail"`, `"step"`, `"title"`, `"body"`} {
		if !strings.Contains(s, want) {
			t.Errorf("serialized view missing %s: %s", want, s)
		}
	}
}
