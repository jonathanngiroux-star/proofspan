package app

import "fmt"

// Wizard is the shared first-run walkthrough model (03-gui.md: a wizard
// guides the user step by step through the wedge: analyze an export →
// dry-run the migration → ingest → eval → see the verdict). Pure logic —
// the GUI renders it as a modal, the TUI as a mode; both drive the exact
// same argv builders as the normal tabs, so the wizard cannot drift from
// the product.
//
// The wizard is a WORKING guide, not prose: every runnable step must be
// actually run (Record*) before Next() will advance past it. Users who
// don't want the walkthrough at all use the front-end's skip control.
type Wizard struct {
	step   WizardStep
	source string // langsmith | honeyhive
	file   string
	db     string

	// completed steps (for the progress display and Next() gating)
	ranAnalyze bool
	ranPlan    bool
	ranMigrate bool
	ranEval    bool

	// last results
	reportSummary string
	planSummary   string
	migratedSum   string
	evalSummary   string
}

// WizardStep identifies one page of the walkthrough.
type WizardStep int

const (
	StepIntro WizardStep = iota
	StepSource
	StepAnalyze
	StepPlan
	StepMigrate
	StepEval
	StepDone
)

// WizardSteps is the ordered list, for progress rendering.
var WizardSteps = []WizardStep{StepIntro, StepSource, StepAnalyze, StepPlan, StepMigrate, StepEval, StepDone}

// NewWizard returns a walkthrough at the intro page with defaults set.
func NewWizard() *Wizard {
	return &Wizard{step: StepIntro, source: "langsmith", db: "proofspan.sqlite"}
}

// Step returns the current page.
func (w *Wizard) Step() WizardStep { return w.step }

// Source/File/DB expose the wizard's current inputs.
func (w *Wizard) Source() string { return w.source }
func (w *Wizard) File() string   { return w.file }
func (w *Wizard) DB() string     { return w.db }

// SetSource validates and sets the converter source.
func (w *Wizard) SetSource(s string) error {
	if s != "langsmith" && s != "honeyhive" {
		return fmt.Errorf("unknown source %q (langsmith|honeyhive)", s)
	}
	w.source = s
	return nil
}

// SetFile sets the export path (required before Analyze/Plan can run).
func (w *Wizard) SetFile(f string) error {
	if f == "" {
		return fmt.Errorf("export file required")
	}
	w.file = f
	return nil
}

// SetDB sets the database path (required; defaults to proofspan.sqlite).
func (w *Wizard) SetDB(db string) error {
	if db == "" {
		return fmt.Errorf("database path required")
	}
	w.db = db
	return nil
}

// StepTitle returns the page heading the front-end renders.
func (w *Wizard) StepTitle() string {
	switch w.step {
	case StepIntro:
		return "Welcome to Proofspan"
	case StepSource:
		return "Step 1 — Pick your export"
	case StepAnalyze:
		return "Step 2 — Analyze the export (read-only)"
	case StepPlan:
		return "Step 3 — Preview the dry-run plan"
	case StepMigrate:
		return "Step 4 — Ingest into SQLite"
	case StepEval:
		return "Step 5 — Run the eval gate"
	case StepDone:
		return "Done"
	}
	return ""
}

// StepBody returns the page's guidance text.
func (w *Wizard) StepBody() string {
	switch w.step {
	case StepIntro:
		return "This walkthrough migrates one LangSmith or HoneyHive export into SQLite and runs the eval gate on it — the whole wedge in five steps. Nothing is written until step 4, and you can cancel at any point (Esc). Your normal tabs stay untouched."
	case StepSource:
		return "Where do your traces come from today? Both converters ship at 100% field parity on the shared corpus (CI-enforced). You will also pick the export file — one line per run/event, as produced by the platform's export."
	case StepAnalyze:
		return "First, the read-only pass: parse rate, which mapped fields the export actually uses, and a census of any keys the converter does not map. This runs `proofspan report` — it touches nothing."
	case StepPlan:
		return "Now the dry run: counts, the full field-mapping table, dropped-field notes, and one fully converted sample span — as JSON, before anything is written. This is the diff you would forward to a teammate."
	case StepMigrate:
		return "Satisfied with the plan? This step ingests the export into SQLite (idempotent — re-running upserts, never duplicates)."
	case StepEval:
		return "The gate: versioned assertions over every ingested trajectory, nonzero exit on failure. On the fixture this is span-correlation@1.0.0; judges are optional."
	case StepDone:
		return "That's the loop: report → dry-run → migrate → eval. Everything you just did is also one CLI command each — the GUI only drives the same binary. The wizard won't bother you again."
	}
	return ""
}

// Next/Back advance the walkthrough. Next validates the current step's
// inputs and REFUSES to advance past a runnable step until that step's
// command has actually run (Record*) — the guide walks, it doesn't skip.
// Back never un-records a run.
func (w *Wizard) Next() error {
	switch w.step {
	case StepIntro:
		w.step = StepSource
	case StepSource:
		if w.file == "" {
			return fmt.Errorf("pick an export file first")
		}
		w.step = StepAnalyze
	case StepAnalyze:
		if !w.ranAnalyze {
			return fmt.Errorf("run the analyze step first (it is read-only)")
		}
		w.step = StepPlan
	case StepPlan:
		if !w.ranPlan {
			return fmt.Errorf("run the dry-run step first — read the plan before ingesting")
		}
		w.step = StepMigrate
	case StepMigrate:
		if !w.ranMigrate {
			return fmt.Errorf("run the migrate step first")
		}
		w.step = StepEval
	case StepEval:
		if !w.ranEval {
			return fmt.Errorf("run the eval step first — that is the gate the wizard exists for")
		}
		w.step = StepDone
	case StepDone:
		w.step = StepDone // terminal
	}
	return nil
}

func (w *Wizard) Back() {
	if w.step > StepIntro {
		w.step--
	}
}

// Restart returns to the intro page (keeps inputs — a redo is cheap).
func (w *Wizard) Restart() { w.step = StepIntro }

// Finished reports whether the walkthrough reached its last page.
func (w *Wizard) Finished() bool { return w.step == StepDone }

// ---- command wiring ----------------------------------------------------

// ReportCommandArgs returns the argv for the Analyze step.
func (w *Wizard) ReportCommandArgs() ([]string, error) {
	return ReportArgs(ReportConfig{Source: w.source, File: w.file})
}

// PlanCommandArgs returns the argv for the dry-run step (always --dry-run).
func (w *Wizard) PlanCommandArgs() ([]string, error) {
	return MigrateArgs(MigrateConfig{Source: w.source, File: w.file, DB: w.db, DryRun: true})
}

// MigrateCommandArgs returns the argv for the ingest step.
func (w *Wizard) MigrateCommandArgs() ([]string, error) {
	return MigrateArgs(MigrateConfig{Source: w.source, File: w.file, DB: w.db})
}

// EvalCommandArgs returns the argv for the gate step, pinning
// --judges/--registry from the detected repo checkout like the TUI.
func (w *Wizard) EvalCommandArgs() ([]string, error) {
	args, err := EvalArgs(EvalConfig{DB: w.db})
	if err != nil {
		return nil, err
	}
	if root := DetectResourcesDir(absDirHelper(w.db)); root != "" {
		args = append(args,
			"--judges="+root+"/judges/manifest.json",
			"--registry="+root+"/registry/bin",
		)
	}
	return args, nil
}

func absDirHelper(dbPath string) string {
	if dbPath == "" {
		return "."
	}
	return DBDir(dbPath)
}

// RecordAnalyzed/RecordPlanned/RecordMigrated/RecordEval store each step's
// one-line result and mark the step as RUN — Next() will not advance past
// an unrun step. Front-ends call these from the step's completion handler
// with the real command's numbers, never invented text.
func (w *Wizard) RecordAnalyzed(summary string) { w.ranAnalyze = true; w.reportSummary = summary }
func (w *Wizard) RecordPlanned(summary string)  { w.ranPlan = true; w.planSummary = summary }
func (w *Wizard) RecordMigrated(summary string) { w.ranMigrate = true; w.migratedSum = summary }
func (w *Wizard) RecordEval(summary string)     { w.ranEval = true; w.evalSummary = summary }

// StepRan reports whether the current step's command has been run.
func (w *Wizard) StepRan() bool {
	switch w.step {
	case StepAnalyze:
		return w.ranAnalyze
	case StepPlan:
		return w.ranPlan
	case StepMigrate:
		return w.ranMigrate
	case StepEval:
		return w.ranEval
	}
	return false
}

// Runnable reports whether the current step carries a command.
func (w *Wizard) Runnable() bool {
	switch w.step {
	case StepAnalyze, StepPlan, StepMigrate, StepEval:
		return true
	}
	return false
}

// Summaries returns the recorded one-liners for the Done page.
func (w *Wizard) Summaries() (analyzed, planned, migrated, eval string) {
	return w.reportSummary, w.planSummary, w.migratedSum, w.evalSummary
}
