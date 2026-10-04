// Command proofspan-gui is a Wails v2 desktop front-end over the proofspan CLI.
//
// Scope (AGENTS.md / 03-gui.md): the GUI drives the existing commands —
// report, migrate, eval, serve — with the same flags, plus read-only views
// over the dry-run plan, judge manifest, stored trajectories, and the
// donation addresses. No product logic lives here; the CLI remains the
// product. Bound methods call the shared ui/internal/app package (the same
// contracts the e2e suite pins) — never a forked data model.
//
// Build:  wails build
// Dev:    wails dev
// Env:    PROOFSPAN_BIN overrides the binary path (default: "proofspan" on PATH)
package main

import (
	"context"
	"embed"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

// GUI is the struct whose exported methods Wails binds into the frontend.
// All mutable state (running guard, serve process) lives on guiState so the
// bound-method receiver never carries a mutex Wails has to copy.
type GUI struct {
	ctx   context.Context
	state guiState

	mu           sync.Mutex
	selftestPath string
	domReady     bool
}

func main() {
	g := &GUI{}

	// -selftest [report.json]: run the frontend's DOM suite in the real
	// webview, write the report, exit 0/1. Wayland windows can't be driven
	// from outside — the window drives itself.
	selftest := false
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		if args[i] == "-selftest" {
			selftest = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				g.selftestPath = args[i+1]
			}
		}
	}
	if selftest && g.selftestPath == "" {
		g.selftestPath = "/tmp/proofspan-gui-selftest.json"
	}

	err := wails.Run(&options.App{
		Title:            "Proofspan",
		Width:            1020,
		Height:           720,
		MinWidth:         760,
		MinHeight:        520,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 22, A: 255},
		OnStartup: func(ctx context.Context) {
			g.ctx = ctx
			g.startTestBridge() // no-op unless PROOFSPAN_GUI_TESTBRIDGE=1
		},
		OnDomReady: func(ctx context.Context) {
			// DOM ready = the Wails bridge is injected. Only NOW tell the
			// frontend to boot the selftest — booting on script-load raced
			// the bridge and hung silently (0 results, process asleep).
			if selftest {
				g.domReady = true
				runtime.EventsEmit(ctx, "selftest:go")
			}
		},
		OnBeforeClose: func(ctx context.Context) bool { g.shutdown(); return false },
		Bind: []interface{}{
			g,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofspan-gui:", err)
		os.Exit(1)
	}
	if selftest {
		if !g.waitForSelftest(120 * time.Second) {
			fmt.Fprintln(os.Stderr, "selftest: timed out")
			os.Exit(2)
		}
		os.Exit(g.selftestExit())
	}
}

// selftestExit returns the sealed verdict (0 pass, 1 fail).
func (g *GUI) selftestExit() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return selftestState.exitCode
}
