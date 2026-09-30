package main

import (
	"os"
	"testing"
)

// The dispatcher turns `proofspan` into a single entry point:
//   bare `proofspan`      → exec proofspan-tui (interactive terminal only)
//   `proofspan tui`       → force TUI
//   `proofspan desktop`   → exec proofspan-gui
//   `proofspan <cmd>`     → existing CLI commands (migrate, eval, ...)
// These tests pin the resolution logic; the exec itself is a thin wrapper.

func TestDispatchBareProofspanLaunchesTUIWhenInteractive(t *testing.T) {
	// A real terminal is not available in test harnesses; the contract to
	// pin here is the SAFETY property: bare proofspan with piped stdin
	// (scripts, CI, tests) must fall through to the CLI and never hang
	// waiting on an interactive TUI.
	//
	// The interactive branch (uiTUI) is exercised by main() at runtime —
	// its correctness follows from isInteractive returning true only for
	// terminals, which TestInteractiveTerminalDetection pins from the
	// other side.
	ui, sub, err := dispatch([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if isInteractive(os.Stdin) {
		if ui != uiTUI || sub != "" {
			t.Fatalf("bare+interactive: ui=%v sub=%q", ui, sub)
		}
		return
	}
	if ui != uiCLI {
		t.Fatalf("bare+piped-stdin must stay on the CLI (anti-hang), got ui=%v", ui)
	}
}

func TestDispatchDesktopLaunchesGUI(t *testing.T) {
	ui, sub, err := dispatch([]string{"desktop"})
	if err != nil {
		t.Fatal(err)
	}
	if ui != uiGUI || sub != "" {
		t.Fatalf("desktop: ui=%v sub=%q", ui, sub)
	}
}

func TestDispatchTUIForcesTUI(t *testing.T) {
	ui, _, err := dispatch([]string{"tui"})
	if err != nil {
		t.Fatal(err)
	}
	if ui != uiTUI {
		t.Fatalf("tui: ui=%v", ui)
	}
}

func TestDispatchKnownCommandsFallThroughToCLI(t *testing.T) {
	for _, cmd := range []string{"version", "migrate", "eval", "report", "serve"} {
		ui, _, err := dispatch([]string{cmd})
		if err != nil {
			t.Fatal(err)
		}
		if ui != uiCLI {
			t.Fatalf("%s must fall through to the CLI, got ui=%v", cmd, ui)
		}
	}
}

func TestDispatchUnknownCommandIsCLIErrorNotTUI(t *testing.T) {
	ui, _, err := dispatch([]string{"frobnicate"})
	if err != nil {
		t.Fatal("unknown commands are the CLI's to reject, not the dispatcher's")
	}
	if ui != uiCLI {
		t.Fatalf("unknown: ui=%v", ui)
	}
}

func TestInteractiveTerminalDetection(t *testing.T) {
	// The anti-hang property: files, pipes, and /dev/null are NEVER
	// interactive — so `proofspan` under redirection or in scripts/CI
	// always stays a CLI. (Test-harness stdin varies by environment and
	// is deliberately not asserted here.)
	f, err := os.CreateTemp(t.TempDir(), "notatty")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isInteractive(f) {
		t.Fatal("a regular file must never be interactive")
	}
	if isInteractive(os.NewFile(0, "/dev/null")) {
		t.Fatal("/dev/null must never be interactive")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	pw.Close() // empty pipe — the exact case that fooled the Seek heuristic
	if isInteractive(pr) {
		t.Fatal("an empty pipe must never be interactive")
	}
}

func TestBinaryNameResolution(t *testing.T) {
	t.Setenv("PROOFSPAN_TUI_BIN", "/opt/tui")
	if got := binaryName("tui"); got != "/opt/tui" {
		t.Errorf("PROOFSPAN_TUI_BIN ignored: %q", got)
	}
	t.Setenv("PROOFSPAN_TUI_BIN", "")
	if got := binaryName("tui"); got != "proofspan-tui" {
		t.Errorf("default tui name: %q", got)
	}
	t.Setenv("PROOFSPAN_GUI_BIN", "/opt/gui")
	if got := binaryName("desktop"); got != "/opt/gui" {
		t.Errorf("PROOFSPAN_GUI_BIN ignored: %q", got)
	}
}
