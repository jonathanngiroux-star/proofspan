package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/jonathanngiroux-star/proofspan/ui/internal/app"
)

// ---- selftest mode: the GUI tests ITSELF in the real webview -------------
//
// `proofspan-gui -selftest [report.json]` opens the real window and runs
// the frontend's own DOM test suite (frontend/dist/selftest.js): real
// clicks, real inputs, real assertions — including the computed-style
// check that pins the Skip-button CSS fix. The result is written to the
// report path (default: /tmp/proofspan-gui-selftest.json) and the process
// exits 0/1 accordingly, so it is CI-able.
//
// This exists because a webview is not drivable from outside (Wayland
// security) — so the window drives itself and reports through this file.

// selftestState collects the report.
var selftestState struct {
	mu       sync.Mutex
	results  []map[string]any
	done     bool
	exitCode int
}

// SelftestReport records one test result from the frontend.
func (g *GUI) SelftestReport(name string, passed bool, detail string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	selftestState.results = append(selftestState.results, map[string]any{
		"name":   name,
		"passed": passed,
		"detail": detail,
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// SelftestFinish seals the report, writes it to the report path, and
// closes the window so the process exits with the verdict (Wails exits
// when the last window closes; the main loop then exits with the code).
func (g *GUI) SelftestFinish(failed int) {
	g.mu.Lock()
	passed := 0
	for _, r := range selftestState.results {
		if b, _ := r["passed"].(bool); b {
			passed++
		}
	}
	selftestState.done = true
	selftestState.exitCode = 0
	if failed > 0 {
		selftestState.exitCode = 1
	}
	report := map[string]any{
		"passed":  passed,
		"failed":  failed,
		"total":   len(selftestState.results),
		"results": selftestState.results,
	}
	g.mu.Unlock()

	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return
	}
	path := g.selftestPath
	if path == "" {
		path = "/tmp/proofspan-gui-selftest.json"
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	_ = os.WriteFile(path, b, 0o644)
	fmt.Fprintf(os.Stderr, "selftest: %d passed, %d failed → %s\n", passed, failed, path)

	if g.ctx != nil {
		runtime.Quit(g.ctx) // close the window → wails.Run returns → exit(code)
	}
}

// SelftestMode reports whether this instance was launched with -selftest.
// The frontend asks FIRST — the suite must never run in a normal launch.
func (g *GUI) SelftestMode() bool {
	return g.selftestPath != ""
}

// SelftestReady hands the frontend its payload: the fixture corpus path
// (walked up from the working directory, or $PROOFSPAN_SELFTEST_CORPUS) and
// a fresh scratch DB. Called only after SelftestMode() == true.
func (g *GUI) SelftestReady() (map[string]string, error) {
	if g.selftestPath == "" {
		return nil, fmt.Errorf("not in selftest mode")
	}
	corpus := os.Getenv("PROOFSPAN_SELFTEST_CORPUS")
	if corpus == "" {
		dir, err := os.Getwd()
		if err == nil {
			for i := 0; i < 12; i++ {
				cand := filepath.Join(dir, "testdata", "corpus", "langsmith", "corpus.jsonl")
				if _, err := os.Stat(cand); err == nil {
					corpus = cand
					break
				}
				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}
				dir = parent
			}
		}
	}
	if corpus == "" {
		return nil, fmt.Errorf("selftest: corpus not found (run from the repo or set PROOFSPAN_SELFTEST_CORPUS)")
	}
	scratch := filepath.Join(os.TempDir(), "proofspan-selftest")
	_ = os.MkdirAll(scratch, 0o755)
	db := filepath.Join(scratch, "wizard.sqlite")
	_ = os.Remove(db) // fresh DB per run: migrate must be what creates it
	return map[string]string{
		"corpus": corpus,
		"db":     db,
		"bin":    app.ResolveBinary(),
	}, nil
}

// waitForSelftest blocks until the frontend sealed the report (or timeout).
// On timeout it prints every report line received so far — the ground-truth
// trail for diagnosing a stalled suite (webview console.log goes nowhere
// in a production Wails build).
func (g *GUI) waitForSelftest(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	lastSeen := -1
	for time.Now().Before(deadline) {
		g.mu.Lock()
		done := selftestState.done
		n := len(selftestState.results)
		g.mu.Unlock()
		if done {
			return true
		}
		if n > 0 && n != lastSeen {
			lastSeen = n
			fmt.Fprintf(os.Stderr, "selftest: %d results so far (suite alive)\n", n)
		}
		time.Sleep(100 * time.Millisecond)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	fmt.Fprintf(os.Stderr, "selftest: timed out with %d results\n", len(selftestState.results))
	for _, r := range selftestState.results {
		passed := false
		if b, _ := r["passed"].(bool); b {
			passed = true
		}
		name, _ := r["name"].(string)
		fmt.Fprintf(os.Stderr, "selftest:   %v %s\n", passed, name)
	}
	return false
}
