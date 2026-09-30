package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTUIModelDrivesRealCLI proves the TUI's full execution path — the
// same Update(RunMsg) → startRun → Runner.Run sequence the binary's enter
// key triggers — against the real CLI on the real corpus. This is the
// testable equivalent of "press 2, then enter, and the migration runs".
func TestTUIModelDrivesRealCLI(t *testing.T) {
	home := repoRoot(t)
	bin := t.TempDir() + "/proofspan"
	if out, err := exec.Command("go", "build", "-C", home, "-o", bin, "./cmd/proofspan").CombinedOutput(); err != nil {
		t.Skipf("could not build CLI: %v: %s", err, out)
	}
	t.Setenv("PROOFSPAN_BIN", bin)

	db := t.TempDir() + "/tui.sqlite"
	m := NewTUIModel()
	// the binary auto-detects the checkout at startup; mirror that here
	// so eval pins --judges/--registry regardless of the test's cwd
	m.SetResourcesDir(home)

	// user flow: tab 2 (Migrate), set file + db, dry-run ON first
	m.Update(TabSelectMsg{Tab: TabMigrate})
	m.Update(FieldMsg{Field: FieldFile, Value: home + "/testdata/corpus/langsmith/corpus.jsonl"})
	m.Update(FieldMsg{Field: FieldDB, Value: db})
	m.Update(FieldMsg{Field: FieldDryRun, Value: "true"})

	// capture the command the model wants to run
	var want Cmd
	m.runHook = func(c Cmd) { want = c }
	m.Update(RunMsg{})

	// the model must have produced the real migrate argv
	joined := strings.Join(want.Args, " ")
	if !strings.Contains(joined, "--dry-run") || !strings.Contains(joined, "--db="+db) {
		t.Fatalf("model argv wrong: %v", want.Args)
	}

	// execute it through the real Runner, honoring the model's Dir contract
	// ("" = inherit cwd, exactly like the TUI binary does)
	r := &Runner{Binary: bin}
	var lines []string
	code, err := r.Run(want.Args, nil, func(l string) { lines = append(lines, l) })
	if err != nil || code != 0 {
		t.Fatalf("dry-run migrate: exit=%d err=%v lines=%v", code, err, lines)
	}
	// the runtime reports completion back to the model before the next run
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: code})

	// now the write path (dry-run off), then eval must report 400/400
	m.Update(FieldMsg{Field: FieldDryRun, Value: "false"})
	var migCmd Cmd
	m.runHook = func(c Cmd) { migCmd = c }
	m.Update(RunMsg{})
	code, err = r.Run(migCmd.Args, nil, func(l string) {})
	if err != nil || code != 0 {
		t.Fatalf("write migrate: exit=%d err=%v", code, err)
	}
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: code})

	m.Update(TabSelectMsg{Tab: TabEvaluate})
	var evalCmd Cmd
	m.runHook = func(c Cmd) { evalCmd = c }
	m.Update(RunMsg{})
	lines = nil
	code, err = r.Run(evalCmd.Args, nil, func(l string) { lines = append(lines, l) })
	if err != nil || code != 0 {
		t.Fatalf("eval: exit=%d err=%v lines=%v", code, err, lines)
	}
	m.Update(CmdDoneMsg{ID: m.PendingCmdID(), Code: code, Lines: lines})
	var summary string
	for _, l := range lines {
		if strings.HasPrefix(l, "eval: ") {
			summary = l
		}
	}
	if summary != "eval: 400/400 trajectories pass" {
		t.Fatalf("eval summary = %q", summary)
	}
	_ = filepath.Join
	_ = os.Getenv
}
