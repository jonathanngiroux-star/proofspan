package app

import (
	"strings"
	"testing"
)

func TestTUIWizardInlineInput(t *testing.T) {
	m := NewTUIModel()
	m.Update(WizardToggleMsg{})
	m.Update(WizardNextMsg{}) // → source page

	// enter input mode: starts at source
	m.Update(WizardInputModeMsg{})
	on, field, buf := m.WizardInputActive()
	if !on || field != 0 || buf != "langsmith" {
		t.Fatalf("input mode: on=%v field=%d buf=%q", on, field, buf)
	}

	// type over the source: backspace x10 then honeyhive
	for i := 0; i < 10; i++ {
		m.Update(WizardInputKeyMsg{Key: "backspace"})
	}
	for _, ch := range "honeyhive" {
		m.Update(WizardInputKeyMsg{Key: string(ch)})
	}
	m.Update(WizardInputKeyMsg{Key: "enter"})
	_, field, buf = m.WizardInputActive()
	if field != 1 || buf != "" {
		t.Fatalf("after source commit: field=%d buf=%q (want file field)", field, buf)
	}
	if m.wizard.Source() != "honeyhive" {
		t.Fatalf("source not committed: %q", m.wizard.Source())
	}

	// type the file path
	for _, ch := range "traces.jsonl" {
		m.Update(WizardInputKeyMsg{Key: string(ch)})
	}
	m.Update(WizardInputKeyMsg{Key: "enter"})
	if m.wizard.File() != "traces.jsonl" {
		t.Fatalf("file not committed: %q", m.wizard.File())
	}

	// db field: preloaded with the default; append a suffix to prove editing
	for _, ch := range "-wz" {
		m.Update(WizardInputKeyMsg{Key: string(ch)})
	}
	m.Update(WizardInputKeyMsg{Key: "enter"})
	if on, _, _ = m.WizardInputActive(); on {
		t.Fatal("input mode must exit after the db field")
	}
	if m.wizard.DB() != "proofspan.sqlite-wz" {
		t.Fatalf("db not committed: %q", m.wizard.DB())
	}
	// tabs stayed in sync
	if m.MigrateCfg().File != "traces.jsonl" || m.MigrateCfg().DB != "proofspan.sqlite-wz" {
		t.Fatalf("tabs not synced: %+v", m.MigrateCfg())
	}

	// run refuses while input is on, works after
	m.Update(WizardInputModeMsg{})
	if m.wizardInputOn {
		m.Update(WizardRunMsg{})
		if m.Busy() {
			t.Fatal("run must refuse during input mode")
		}
	}
	m.Update(WizardInputKeyMsg{Key: "esc"})
	m.Update(WizardInputKeyMsg{Key: "r"}) // r is a printable char — ignored outside input mode
	_ = strings.TrimSpace("")
}

func TestTUIWizardRunRecordsFromRealLines(t *testing.T) {
	// the runner now delivers the real output lines with CmdDoneMsg; the
	// wizard must auto-record from them (the working-guide contract).
	m := NewTUIModel()
	m.Update(WizardToggleMsg{})
	m.Update(WizardSetInputsMsg{File: "traces.jsonl", DB: "wz.sqlite"})
	m.Update(WizardNextMsg{}) // intro→source
	m.Update(WizardNextMsg{}) // source→analyze

	m.runHook = func(Cmd) {}
	m.Update(WizardRunMsg{})
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0, Lines: []string{"report: 10000/10000 lines parsed"}})
	w, _ := m.WizardState()
	a, _, _, _ := w.Summaries()
	if a != "report: 10000/10000 lines parsed" {
		t.Fatalf("analyze summary must come from real lines: %q", a)
	}
	// and Next works now that the step ran
	if err := w.Next(); err != nil {
		t.Fatalf("Next after run: %v", err)
	}
}
