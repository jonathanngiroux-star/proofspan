package main

// In-app test bridge (the FogOS pattern, generalized for Go GUIs).
//
// When the app is launched with PROOFSPAN_GUI_TESTBRIDGE=1, a goroutine
// polls a command file (one JSON object per line) and forwards each
// command into the webview as a "test:cmd" event. The frontend's fixed
// interpreter (frontend/dist/testbridge.js) executes it against the real
// DOM — real handlers, real bindings, real CLI subprocesses — and returns
// the result through the TestResult binding, which appends
// `TEST-RESULT {json}` to a log the external Python driver reads.
//
// Why a bridge at all: a Wayland webview cannot be driven from outside.
// xdotool/X11 keystrokes never reach WebKitGTK; no browser driver can
// attach to the wails:// origin; ydotool absolute moves land wrong under
// KWin. The app tests itself — the driver only writes commands and reads
// results (offset-based, unique IDs: both were real stale-read bugs).
//
// Security: the interpreter is a fixed switch — no eval — so the page CSP
// holds. The bridge arms ONLY on the "test:bridge" event, which the Go
// side emits solely when the env var is set. Production launches never
// poll and never arm.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	bridgeDefaultCmdFile    = "/tmp/proofspan-gui-cmd.jsonl"
	bridgeDefaultResultFile = "/tmp/proofspan-gui-result.log"
)

type bridgeConfig struct {
	enabled    bool
	cmdFile    string
	resultFile string
}

var bridge struct {
	mu     sync.Mutex
	config bridgeConfig
}

func loadBridgeConfig() bridgeConfig {
	c := bridgeConfig{
		enabled:    os.Getenv("PROOFSPAN_GUI_TESTBRIDGE") == "1",
		cmdFile:    os.Getenv("PROOFSPAN_GUI_CMD_FILE"),
		resultFile: os.Getenv("PROOFSPAN_GUI_RESULT_FILE"),
	}
	if c.cmdFile == "" {
		c.cmdFile = bridgeDefaultCmdFile
	}
	if c.resultFile == "" {
		c.resultFile = bridgeDefaultResultFile
	}
	return c
}

// startTestBridge arms the bridge (from OnStartup) and begins polling the
// command file. No-op in a normal launch.
func (g *GUI) startTestBridge() {
	bridge.mu.Lock()
	bridge.config = loadBridgeConfig()
	c := bridge.config
	bridge.mu.Unlock()
	if !c.enabled {
		return
	}
	// stale commands from a previous run must never fire
	_ = os.Remove(c.cmdFile)
	if f, err := os.OpenFile(c.resultFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		f.Close()
	} else {
		return // result channel broken → bridge stays off
	}
	go g.pollBridgeCommands(c)
}

// pollBridgeCommands forwards command-file lines into the webview. The
// driver keeps ONE command in flight (it waits for each result), so
// read-all-then-truncate cannot drop a command.
func (g *GUI) pollBridgeCommands(c bridgeConfig) {
	for {
		time.Sleep(100 * time.Millisecond)
		if g.ctx == nil {
			continue
		}
		data, err := os.ReadFile(c.cmdFile)
		if err != nil || len(data) == 0 {
			continue
		}
		_ = os.Truncate(c.cmdFile, 0)
		sc := bufio.NewScanner(strings.NewReader(string(data)))
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			var cmd map[string]any
			if err := json.Unmarshal([]byte(line), &cmd); err != nil {
				g.appendBridgeResult(`{"id":"","ok":false,"error":"bad command json: ` + err.Error() + `"}`)
				continue
			}
			emit(g.ctx, "test:cmd", cmd)
		}
	}
}

// TestResult receives one interpreter result from the frontend and appends
// it to the result log as `TEST-RESULT {json}`. Bridge-only; silently
// ignored in a normal launch.
func (g *GUI) TestResult(resultJSON string) {
	bridge.mu.Lock()
	c := bridge.config
	bridge.mu.Unlock()
	if !c.enabled {
		return
	}
	f, err := os.OpenFile(c.resultFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "TEST-RESULT %s\n", resultJSON)
}

// appendBridgeResult is the Go-side error path (malformed command JSON).
func (g *GUI) appendBridgeResult(json string) {
	bridge.mu.Lock()
	c := bridge.config
	bridge.mu.Unlock()
	if !c.enabled {
		return
	}
	if f, err := os.OpenFile(c.resultFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintf(f, "TEST-RESULT %s\n", json)
		f.Close()
	}
}
