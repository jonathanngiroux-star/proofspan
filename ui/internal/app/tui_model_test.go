package app

import (
	"strings"
	"testing"
)

// The TUI model is pure state + Update(msg) — no terminal IO. These tests
// pin the interaction contract before any rendering exists (Elm-style).

func newTestModel() *TUIModel {
	return NewTUIModel()
}

func TestModelStartsOnAnalyzeTab(t *testing.T) {
	m := NewTUIModel()
	if m.ActiveTab() != TabAnalyze {
		t.Fatalf("initial tab = %v, want Analyze", m.ActiveTab())
	}
	if m.Finished() {
		t.Fatal("fresh model must not be finished")
	}
}

func TestTabSwitchByNumber(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabMigrate})
	if m.ActiveTab() != TabMigrate {
		t.Fatalf("after switch: %v", m.ActiveTab())
	}
	m.Update(TabSelectMsg{Tab: TabServe})
	if m.ActiveTab() != TabServe {
		t.Fatalf("after second switch: %v", m.ActiveTab())
	}
}

func TestFieldEditUpdatesConfig(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabMigrate})
	m.Update(FieldMsg{Field: FieldFile, Value: "/data/traces.jsonl"})
	m.Update(FieldMsg{Field: FieldDB, Value: "/data/ps.sqlite"})
	c := m.MigrateCfg()
	if c.File != "/data/traces.jsonl" || c.DB != "/data/ps.sqlite" {
		t.Fatalf("config not applied: %+v", c)
	}
	if c.DryRun {
		t.Fatal("dry-run must default off")
	}
	m.Update(FieldMsg{Field: FieldDryRun, Value: "true"})
	if !m.MigrateCfg().DryRun {
		t.Fatal("dry-run toggle lost")
	}
}

func TestRunRequestBuildsCorrectCommand(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabMigrate})
	m.Update(FieldMsg{Field: FieldFile, Value: "t.jsonl"})
	m.Update(FieldMsg{Field: FieldDB, Value: "db.sqlite"})

	var gotCmd Cmd
	m.runHook = func(c Cmd) { gotCmd = c }
	m.Update(RunMsg{})
	if gotCmd.Label != "migrate" {
		t.Fatalf("label = %q", gotCmd.Label)
	}
	want := []string{"migrate", "--from=langsmith", "--db=db.sqlite", "t.jsonl"}
	if strings.Join(gotCmd.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("args = %v want %v", gotCmd.Args, want)
	}
	if !m.Busy() {
		t.Fatal("model must be busy while a command runs")
	}
	// runtime delivers the completion
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 0})
	if m.Busy() {
		t.Fatal("busy must clear on done")
	}
}

func TestDoneClearsBusyAndReportsExit(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabMigrate})
	m.Update(FieldMsg{Field: FieldFile, Value: "t.jsonl"})
	m.Update(FieldMsg{Field: FieldDB, Value: "db.sqlite"})
	m.Update(RunMsg{})
	if !m.Busy() {
		t.Fatal("busy before done")
	}
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: 1, Lines: []string{"eval gate failed"}})
	if m.Busy() {
		t.Fatal("busy must clear on done")
	}
	if !strings.Contains(m.Status(), "exit 1") {
		t.Fatalf("status must carry the exit code: %q", m.Status())
	}
	// stale completion delivered after the real one must be ignored:
	// no command is pending, and ID 0 is never a valid completion.
	m.Update(CmdDoneMsg{ID: 999, Code: 0})
	m.Update(CmdDoneMsg{ID: 0, Code: 0})
	if !strings.Contains(m.Status(), "exit 1") {
		t.Fatalf("stale done overwrote status: %q", m.Status())
	}
}

func TestServeStartStopLifecycle(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabServe})
	m.Update(FieldMsg{Field: FieldAddr, Value: "127.0.0.1:7500"})
	m.Update(FieldMsg{Field: FieldSCIM, Value: "true"})
	m.Update(FieldMsg{Field: FieldSCIMToken, Value: "secret"})

	var started ServeConfig
	var stopped bool
	m.serveStartHook = func(c ServeConfig) error {
		started = c
		return nil
	}
	m.serveStopHook = func() error { stopped = true; return nil }

	m.Update(ServeStartMsg{})
	if !started.SCIM || started.SCIMToken != "secret" || started.Addr != "127.0.0.1:7500" {
		t.Fatalf("serve config wrong: %+v", started)
	}
	if !m.ServeRunning() {
		t.Fatal("model must report serve running after start")
	}
	m.Update(ServeStopMsg{})
	if !stopped {
		t.Fatal("stop must invoke the stop hook")
	}
	if m.ServeRunning() {
		t.Fatal("serve must be down after stop")
	}
}

func TestSCIMTokenGoesToEnvNotArgv(t *testing.T) {
	m := newTestModel()
	m.Update(TabSelectMsg{Tab: TabServe})
	m.Update(FieldMsg{Field: FieldSCIMToken, Value: "hunter2"})
	m.Update(FieldMsg{Field: FieldSCIM, Value: "true"})
	args, env, err := m.ServeCommand()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "hunter2") {
		t.Fatalf("token leaked into argv: %v", args)
	}
	if env["PROOFSPAN_SCIM_TOKEN"] != "hunter2" {
		t.Fatalf("token must go to env: %v", env)
	}
}

// TestEvalArgsIncludeResourceDirsWhenPresent: when a repo checkout with
// judges/ + registry/ is found (walk up from cwd), eval argv must carry
// --judges/--registry so the command works from ANY working directory.
func TestEvalArgsIncludeResourceDirsWhenPresent(t *testing.T) {
	m := newTestModel()
	m.Update(FieldMsg{Field: FieldDB, Value: "/tmp/x.sqlite"})

	m.SetResourcesDir("/repo") // simulated checkout root
	args, err := m.EvalCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--judges=/repo/judges/manifest.json") {
		t.Fatalf("missing --judges pin: %v", args)
	}
	if !strings.Contains(joined, "--registry=/repo/registry/bin") {
		t.Fatalf("missing --registry pin: %v", args)
	}

	// without a detected checkout: bare eval, cwd semantics
	m.SetResourcesDir("")
	args, err = m.EvalCommandArgs()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "--judges=") {
		t.Fatalf("no checkout → no --judges flag: %v", args)
	}
}
