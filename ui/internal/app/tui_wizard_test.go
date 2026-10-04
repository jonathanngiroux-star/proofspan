package app

import (
	"strings"
	"testing"
)

func TestTUIWizardToggleAndPages(t *testing.T) {
	m := NewTUIModel()
	if _, on := m.WizardState(); on {
		t.Fatal("wizard must start off")
	}

	m.Update(WizardToggleMsg{})
	w, on := m.WizardState()
	if !on || w == nil {
		t.Fatal("wizard must be on after toggle")
	}
	if w.Step() != StepIntro {
		t.Errorf("wizard opens at intro, got %d", w.Step())
	}
	if !strings.Contains(m.Status(), "wizard") {
		t.Errorf("status should mention the wizard: %q", m.Status())
	}

	// page through with validation
	m.Update(WizardNextMsg{}) // intro → source
	if w.Step() != StepSource {
		t.Fatalf("want StepSource, got %d", w.Step())
	}
	m.Update(WizardNextMsg{}) // no file yet → refuse
	if w.Step() != StepSource {
		t.Error("must refuse to advance without a file")
	}
	if !strings.Contains(m.Status(), "pick an export file") {
		t.Errorf("refusal must explain: %q", m.Status())
	}

	m.Update(WizardSetInputsMsg{Source: "honeyhive", File: "traces.jsonl", DB: "wz.sqlite"})
	if w.File() != "traces.jsonl" || w.Source() != "honeyhive" {
		t.Errorf("inputs not recorded: %+v", w)
	}
	// tabs stay in sync with what the wizard learned
	if m.MigrateCfg().Source != "honeyhive" || m.MigrateCfg().File != "traces.jsonl" || m.MigrateCfg().DB != "wz.sqlite" {
		t.Errorf("tab configs not synced from wizard: %+v", m.MigrateCfg())
	}

	// walk to Done: every runnable step must RUN (and its command complete)
	// before Next advances — the working-guide contract.
	m.runHook = func(Cmd) {} // consume commands without executing
	for i := 0; i < 6; i++ {
		if w.Runnable() {
			m.Update(WizardRunMsg{})
			m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0, Lines: []string{"eval: done"}})
		}
		if err := w.Next(); err != nil {
			t.Fatalf("Next at step %d: %v", w.Step(), err)
		}
	}
	if !w.Finished() {
		t.Errorf("should reach Done, at %d", w.Step())
	}

	m.Update(WizardToggleMsg{})
	if _, on = m.WizardState(); on {
		t.Error("second toggle must close the wizard")
	}
}

func TestTUIWizardRunStepBuildsRealCommand(t *testing.T) {
	m := NewTUIModel()
	m.Update(WizardToggleMsg{})
	m.Update(WizardSetInputsMsg{File: "traces.jsonl", DB: "wz.sqlite"})
	m.Update(WizardNextMsg{}) // intro → source
	m.Update(WizardNextMsg{}) // source → analyze (file set, passes validation)

	var got Cmd
	m.runHook = func(c Cmd) { got = c }

	m.Update(WizardRunMsg{})
	if !m.Busy() {
		t.Fatal("wizard run must mark busy like a normal run")
	}
	if got.Label != "report" {
		t.Errorf("label = %q", got.Label)
	}
	want := []string{"report", "--from=langsmith", "traces.jsonl"}
	if strings.Join(got.Args, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want %v", got.Args, want)
	}

	// finish it, then the dry-run page
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0})
	m.Update(WizardNextMsg{}) // → Plan
	m.Update(WizardRunMsg{})
	if !strings.Contains(strings.Join(got.Args, " "), "--dry-run") {
		t.Errorf("plan step must be dry-run: %v", got.Args)
	}
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0})

	m.Update(WizardNextMsg{}) // → Migrate
	m.Update(WizardRunMsg{})
	if strings.Contains(strings.Join(got.Args, " "), "--dry-run") {
		t.Errorf("migrate step must not be dry-run: %v", got.Args)
	}
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0})

	m.Update(WizardNextMsg{}) // → Eval
	m.Update(WizardRunMsg{})
	if got.Args[0] != "eval" {
		t.Errorf("eval step argv = %v", got.Args)
	}
}

func TestTUIWizardRunIgnoredWhenOff(t *testing.T) {
	m := NewTUIModel()
	m.Update(WizardRunMsg{}) // wizard off — must be a no-op
	if m.Busy() {
		t.Error("wizard run must not fire when the wizard is off")
	}
}
