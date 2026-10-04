package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/jonathanngiroux-star/proofspan/ui/internal/app"
)

// ---- first-run wizard (shared model in ui/internal/app) ----------------
//
// The wizard is a WORKING guide: every step carries the fields and actions
// needed to complete it — pick/browse the export, run the read-only report,
// preview the dry-run plan, ingest, run the gate — with real summaries
// parsed from each command's machine-readable output. Nothing is prose-only.

// WizardStepView is one page of the walkthrough, rendered by the frontend.
type WizardStepView struct {
	Step      int    `json:"step"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Source    string `json:"source"`
	File      string `json:"file"`
	DB        string `json:"db"`
	NeedsFile bool   `json:"needsFile"` // show source/file/db inputs (source page)
	Runnable  bool   `json:"runnable"`  // this page has a command to run
	RanStep   bool   `json:"ranStep"`   // the current step's command ran
	Running   bool   `json:"running"`   // a step command is in flight
	Summary   string `json:"summary"`   // current step's one-line result
	// Tail is the last lines of the step's raw output, shown inside the
	// modal (the main log pane is hidden behind the overlay).
	Tail []string          `json:"tail"`
	Done map[string]string `json:"done,omitempty"` // step 6 checklist
}

// wizardState guards the single wizard instance plus the output tail.
var wizardState struct {
	w       *app.Wizard
	running bool
	tail    []string
}

// WizardStart returns the walkthrough at its intro page (and resets any
// previous run). Called when the user opens the wizard.
func (g *GUI) WizardStart() WizardStepView {
	wizardState.w = app.NewWizard()
	wizardState.tail = nil
	return wizardView()
}

// WizardNext validates and advances; returns the refreshed page or an
// error the frontend shows inline. Migrate refuses to advance until the
// ingest actually ran — the guide walks, it doesn't skip.
func (g *GUI) WizardNext() (WizardStepView, error) {
	w := wizardState.w
	if w == nil {
		w = app.NewWizard()
		wizardState.w = w
	}
	if err := w.Next(); err != nil {
		return wizardView(), err
	}
	return wizardView(), nil
}

// WizardBack returns to the previous page.
func (g *GUI) WizardBack() WizardStepView {
	if wizardState.w == nil {
		wizardState.w = app.NewWizard()
	}
	wizardState.w.Back()
	return wizardView()
}

// WizardRestart returns to the intro page, keeping the inputs.
func (g *GUI) WizardRestart() WizardStepView {
	if wizardState.w == nil {
		wizardState.w = app.NewWizard()
	}
	wizardState.w.Restart()
	return wizardView()
}

// WizardSetInputs updates the source/file/db fields. Empty strings are
// ignored so the frontend can send only what its inputs contain.
func (g *GUI) WizardSetInputs(source, file, db string) (WizardStepView, error) {
	w := wizardState.w
	if w == nil {
		w = app.NewWizard()
		wizardState.w = w
	}
	if source != "" {
		if err := w.SetSource(source); err != nil {
			return wizardView(), err
		}
	}
	if file != "" {
		if err := w.SetFile(file); err != nil {
			return wizardView(), err
		}
	}
	if db != "" {
		if err := w.SetDB(db); err != nil {
			return wizardView(), err
		}
	}
	return wizardView(), nil
}

// WizardBrowseFile opens the native file dialog and returns the chosen
// path ("" when cancelled). The picked path is NOT auto-applied — the
// frontend fills the input and calls WizardSetInputs, keeping the visible
// field the single source of truth.
func (g *GUI) WizardBrowseFile() (string, error) {
	if g.ctx == nil {
		return "", fmt.Errorf("no window context")
	}
	path, err := runtime.OpenFileDialog(g.ctx, runtime.OpenDialogOptions{
		Title: "Pick a LangSmith or HoneyHive export (.jsonl / .json)",
		Filters: []runtime.FileFilter{
			{DisplayName: "Trace exports (*.jsonl, *.json)", Pattern: "*.jsonl;*.json"},
			{DisplayName: "All files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// WizardRunStep executes the current page's real command — the exact argv
// the normal tabs produce — captures BOTH streams (stdout is the
// machine-readable channel; status lines live on stderr), records a real
// one-line summary, and keeps an output tail for the modal.
func (g *GUI) WizardRunStep() (WizardStepView, error) {
	w := wizardState.w
	if w == nil {
		return wizardView(), fmt.Errorf("wizard not started")
	}
	if wizardState.running {
		return wizardView(), fmt.Errorf("a wizard step is already running")
	}

	var (
		args  []string
		label string
	)
	switch w.Step() {
	case app.StepAnalyze:
		a, err := w.ReportCommandArgs()
		if err != nil {
			return wizardView(), err
		}
		args, label = a, "Analyze"
	case app.StepPlan:
		a, err := w.PlanCommandArgs()
		if err != nil {
			return wizardView(), err
		}
		args, label = a, "Plan"
	case app.StepMigrate:
		a, err := w.MigrateCommandArgs()
		if err != nil {
			return wizardView(), err
		}
		args, label = a, "Migrate"
	case app.StepEval:
		a, err := w.EvalCommandArgs()
		if err != nil {
			return wizardView(), err
		}
		args, label = a, "Evaluate"
	case app.StepIntro, app.StepSource, app.StepDone:
		return wizardView(), fmt.Errorf("this page has no command — set the inputs and press Next")
	default:
		return wizardView(), fmt.Errorf("unknown wizard step")
	}

	wizardState.running = true
	defer func() { wizardState.running = false }()

	stdoutStr, stderrStr, err := g.captureBoth(args, wizardDBPath(args))
	if err != nil {
		wizardState.tail = splitLines(stderrStr)
		return wizardView(), err
	}
	wizardState.tail = splitLines(stdoutStr + "\n" + stderrStr)

	switch label {
	case "Analyze":
		var rep struct {
			LinesParsed  int `json:"lines_parsed"`
			LinesTotal   int `json:"lines_total"`
			Trajectories int `json:"trajectories"`
		}
		_ = json.Unmarshal([]byte(stdoutStr), &rep)
		w.RecordAnalyzed(fmt.Sprintf("report: %d/%d lines parsed, %d trajectories, census in output",
			rep.LinesParsed, rep.LinesTotal, rep.Trajectories))
	case "Plan":
		var plan DryRunPlan
		if err := json.Unmarshal([]byte(stdoutStr), &plan); err == nil {
			w.RecordPlanned(fmt.Sprintf("plan: %d trajectories / %d spans / %d dropped fields",
				plan.Trajectories, plan.SpansPlanned, len(plan.DroppedFields)))
		} else {
			w.RecordPlanned("plan rendered in output pane")
		}
	case "Migrate":
		// migrate's human summary ("migrated N trajectories, M spans into …")
		// lives on the merged stream; scrape it for the real counts.
		line := ""
		for _, l := range wizardState.tail {
			if strings.HasPrefix(l, "migrated ") {
				line = l
			}
		}
		if line == "" {
			line = "migrate: done — see output"
		}
		w.RecordMigrated(line)
	case "Evaluate":
		var reports []TrajectoryReportView
		if err := json.Unmarshal([]byte(stdoutStr), &reports); err == nil && len(reports) > 0 {
			pass := 0
			for _, r := range reports {
				if r.Failed == 0 && r.Errored == 0 && r.Total > 0 {
					pass++
				}
			}
			w.RecordEval(fmt.Sprintf("eval: %d/%d trajectories pass", pass, len(reports)))
		} else {
			w.RecordEval("eval: verdicts in output pane")
		}
	}
	return wizardView(), nil
}

// captureBoth runs the CLI and returns stdout and stderr separately —
// stdout is the machine-readable channel, stderr carries the human status
// lines. Nonzero exits return the stderr text so the modal shows the real
// failure.
func (g *GUI) captureBoth(args []string, dbPath string) (stdoutStr, stderrStr string, err error) {
	bin := app.ResolveBinary()
	args = pinResources(args, dbPath)
	cmd := exec.Command(bin, args...)
	cmd.Dir = app.DBDir(dbPath)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(errBuf.String())
			if msg == "" {
				msg = fmt.Sprintf("exit %d", ee.ExitCode())
			}
			return outBuf.String(), errBuf.String(), fmt.Errorf("%s: %s", bin, msg)
		}
		return outBuf.String(), errBuf.String(), fmt.Errorf("start %s: %w (is the binary on PATH? set PROOFSPAN_BIN)", bin, err)
	}
	return outBuf.String(), errBuf.String(), nil
}

// splitLines splits a stream into lines, dropping empties, capped at 14
// lines (head first) for the modal tail.
func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	if len(out) > 14 {
		out = out[:14]
	}
	return out
}

// wizardDBPath extracts --db= from argv for the runner's workdir.
func wizardDBPath(args []string) string {
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--db="); ok {
			return v
		}
	}
	return "proofspan.sqlite"
}

// WizardGetDone returns the recorded summaries for the Done page.
func (g *GUI) WizardGetDone() map[string]string {
	w := wizardState.w
	if w == nil {
		return map[string]string{}
	}
	a, p, m, e := w.Summaries()
	return map[string]string{
		"analyzed": a,
		"planned":  p,
		"migrated": m,
		"eval":     e,
	}
}

func wizardView() WizardStepView {
	w := wizardState.w
	if w == nil {
		w = app.NewWizard()
		wizardState.w = w
	}
	step := 0
	switch w.Step() {
	case app.StepIntro:
		step = 0
	case app.StepSource:
		step = 1
	case app.StepAnalyze:
		step = 2
	case app.StepPlan:
		step = 3
	case app.StepMigrate:
		step = 4
	case app.StepEval:
		step = 5
	case app.StepDone:
		step = 6
	}
	a, p, m, e := w.Summaries()
	summary := ""
	switch w.Step() {
	case app.StepAnalyze:
		summary = a
	case app.StepPlan:
		summary = p
	case app.StepMigrate:
		summary = m
	case app.StepEval:
		summary = e
	}
	var done map[string]string
	if w.Step() == app.StepDone {
		done = map[string]string{"analyzed": a, "planned": p, "migrated": m, "eval": e}
	}
	return WizardStepView{
		Step:      step,
		Title:     w.StepTitle(),
		Body:      w.StepBody(),
		Source:    w.Source(),
		File:      w.File(),
		DB:        w.DB(),
		NeedsFile: w.Step() == app.StepSource,
		Runnable:  w.Step() == app.StepAnalyze || w.Step() == app.StepPlan || w.Step() == app.StepMigrate || w.Step() == app.StepEval,
		RanStep:   summary != "",
		Running:   wizardState.running,
		Summary:   summary,
		Tail:      wizardState.tail,
		Done:      done,
	}
}

// wizardMigrateLine is a scrape seam for the migrate summary (kept honest:
// the human line is matched, never invented).
