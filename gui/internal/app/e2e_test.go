package app

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGUIEndToEndAgainstRealCLI drives the exact code path the GUI buttons
// call — Runner + Args builders — against the real proofspan binary, and
// verifies exit codes, streamed output, and the serve lifecycle including a
// live HTTP probe of the started server.
//
// Skips when the CLI binary is not resolvable (CI containers without the
// built binary) or no DISPLAY is present (irrelevant here — no window is
// created, only the exec layer).
func TestGUIEndToEndAgainstRealCLI(t *testing.T) {
	home := "/run/media/thoth/project-backup/github_top_10/proofspan"
	bin := home + "/proofspan"
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("CLI binary not built at %s — build it first (go build -o proofspan ./cmd/proofspan)", bin)
	}
	t.Setenv("PROOFSPAN_BIN", bin)

	db := t.TempDir() + "/gui-e2e.sqlite"

	// 1. report (read-only analyze)
	args, err := ReportArgs(ReportConfig{Source: "langsmith", File: home + "/testdata/corpus/langsmith/corpus.jsonl"})
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	code, err := (&Runner{}).Run(args, nil, func(l string) { lines = append(lines, l) })
	if err != nil || code != 0 {
		t.Fatalf("report: exit=%d err=%v", code, err)
	}
	if len(lines) == 0 {
		t.Fatal("report streamed no output")
	}

	// 2. migrate (write path)
	args, err = MigrateArgs(MigrateConfig{Source: "langsmith", File: home + "/testdata/corpus/langsmith/corpus.jsonl", DB: db})
	if err != nil {
		t.Fatal(err)
	}
	lines = nil
	code, err = (&Runner{}).Run(args, nil, func(l string) { lines = append(lines, l) })
	if err != nil || code != 0 {
		t.Fatalf("migrate: exit=%d err=%v", code, err)
	}
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l, "migrated ") {
			found = true
		}
	}
	if !found {
		t.Errorf("migrate stream missed the summary line: %v", lines[:min(3, len(lines))])
	}

	// 3. eval over the migrated db — first line must be the pass summary.
	// Runs with the REPO ROOT as workdir: that's where judges/manifest.json
	// and registry/bin live, matching the GUI's Dir=db-directory behavior
	// for a user who keeps everything in one directory.
	args, err = EvalArgs(EvalConfig{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	lines = nil
	code, err = (&Runner{Dir: home}).Run(args, nil, func(l string) { lines = append(lines, l) })
	if err != nil || code != 0 {
		t.Fatalf("eval: exit=%d err=%v (lines: %v)", code, err, firstN(lines, 2))
	}
	var summary string
	for _, l := range lines {
		if strings.HasPrefix(l, "eval: ") {
			summary = l
		}
	}
	if summary != "eval: 400/400 trajectories pass" {
		t.Errorf("eval summary = %q", summary)
	}

	// 4. serve lifecycle: start with SCIM + token, probe healthz and the
	// auth wall over HTTP, then stop and confirm the port died.
	sargs, env, err := ServeArgs(ServeConfig{DB: db, Addr: "127.0.0.1:7455", SCIM: true, SCIMToken: "gui-test-token"})
	if err != nil {
		t.Fatal(err)
	}
	proc, err := StartServe(sargs, env, home, func(l string) {})
	if err != nil {
		t.Fatalf("serve start: %v", err)
	}
	if !proc.Running() {
		t.Fatal("serve not running after start")
	}
	// wait for the listener
	deadline := time.Now().Add(5 * time.Second)
	ok := false
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://127.0.0.1:7455/healthz")
		if err == nil {
			resp.Body.Close()
			ok = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ok {
		t.Fatal("server never answered /healthz within 5s")
	}
	// auth wall: no token → 401
	resp, err := http.Get("http://127.0.0.1:7455/scim/v2/Users")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("SCIM without token: got %d want 401", resp.StatusCode)
	}
	resp, err = http.Get("http://127.0.0.1:7455/scim/v2/Users")
	_ = resp
	_ = err
	if err := proc.Stop(); err != nil {
		t.Fatalf("serve stop: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := http.Get("http://127.0.0.1:7455/healthz"); err == nil {
		t.Error("server still answering after Stop()")
	}
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = fmt.Sprintf
