package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"

	"golang.org/x/term"
)

// uiTarget identifies what a bare/front-end invocation launches.
type uiTarget int

const (
	uiCLI uiTarget = iota
	uiTUI
	uiGUI
)

// knownCommands are the CLI subcommands — anything in this list (or any
// unrecognized word) falls through to the normal CLI. Only `desktop` and
// `tui` are dispatcher words.
var knownCommands = map[string]bool{
	"version": true, "migrate": true, "eval": true,
	"report": true, "serve": true,
}

// dispatch decides what a proofspan invocation runs:
//   - no args + interactive terminal → TUI (the "type proofspan" experience)
//   - no args + NOT interactive       → CLI usage (scripts, CI; never hang)
//   - "desktop"                        → GUI
//   - "tui"                            → TUI even over a pipe
//   - anything else                    → CLI
func dispatch(argv []string) (uiTarget, string, error) {
	if len(argv) == 0 {
		if isInteractive(os.Stdin) {
			return uiTUI, "", nil
		}
		return uiCLI, "", nil
	}
	switch argv[0] {
	case "desktop":
		return uiGUI, "", nil
	case "tui":
		return uiTUI, "", nil
	default:
		return uiCLI, argv[0], nil
	}
}

// binaryName resolves the front-end binary: env override, else the
// conventional name found on PATH.
func binaryName(kind string) string {
	switch kind {
	case "desktop":
		if v := os.Getenv("PROOFSPAN_GUI_BIN"); v != "" {
			return v
		}
		return "proofspan-gui"
	default: // tui
		if v := os.Getenv("PROOFSPAN_TUI_BIN"); v != "" {
			return v
		}
		return "proofspan-tui"
	}
}

// launchFrontEnd replaces this process with the front-end binary. Exit
// codes propagate, so `proofspan` in a script behaves like the TUI itself.
func launchFrontEnd(kind string) error {
	bin := binaryName(kind)
	path, err := exec.LookPath(bin)
	if err != nil {
		return missingFrontEndError(kind, bin)
	}
	// pass through any extra args beyond the dispatcher word; a bare
	// invocation has none (os.Args == [proofspan])
	var rest []string
	if len(os.Args) > 2 {
		rest = os.Args[2:]
	}
	args := append([]string{path}, rest...)
	return execFrontEnd(path, args, os.Environ())
}

func missingFrontEndError(kind, bin string) error {
	how := "go install github.com/jonathanngiroux-star/proofspan/ui/cmd/proofspan-" + map[bool]string{true: "gui", false: "tui"}[kind == "desktop"] + "@latest"
	return fmt.Errorf("%s not found on PATH (looked for %q).\nInstall it: %s\nOr point at it directly: set %s", bin, bin, how,
		map[bool]string{true: "PROOFSPAN_GUI_BIN", false: "PROOFSPAN_TUI_BIN"}[kind == "desktop"])
}

// isInteractive reports whether r is an interactive terminal, portably:
// golang.org/x/term performs the platform-correct check (ioctl TCGETS on
// unix; GetConsoleMode on Windows). /dev/null and empty pipes — both char
// devices or seek-edge-cases on some platforms — are correctly rejected.
// This is the anti-hang guarantee: `proofspan` piped into a script, cron,
// or CI stays a CLI and never blocks on the TUI.
func isInteractive(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}
