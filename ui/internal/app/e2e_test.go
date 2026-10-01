package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestGUIEndToEndAgainstRealCLI drives the exact code path the GUI buttons
// call — Runner + Args builders — against the real proofspan binary, and
// verifies exit codes, streamed output, and the serve lifecycle including a
// live HTTP probe of the started server.
//
// The CLI binary is built fresh into a temp dir (works on CI and locally);
// skips when Go can't build it.
func TestGUIEndToEndAgainstRealCLI(t *testing.T) {
	home := repoRoot(t)
	// build a fresh CLI binary so the e2e runs against current source,
	// independent of any committed/stale artifact
	bin := t.TempDir() + "/proofspan"
	if out, err := exec.Command("go", "build", "-C", home, "-o", bin, "./cmd/proofspan").CombinedOutput(); err != nil {
		t.Skipf("could not build CLI binary: %v: %s", err, out)
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
	// correct token → 200 (the path an IdP actually takes)
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:7455/scim/v2/Users", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer gui-test-token")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Errorf("SCIM with correct token: got %d want 200", resp2.StatusCode)
	}
	if err := proc.Stop(); err != nil {
		t.Fatalf("serve stop: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := http.Get("http://127.0.0.1:7455/healthz"); err == nil {
		t.Error("server still answering after Stop()")
	}
}

// TestEvalStdoutIsPureJSON pins the CLI stdout contract: machine-readable
// JSON is the ONLY thing on stdout (jq-consumable); human status lines
// ("eval: 400/400 trajectories pass", per-judge verdicts) go to stderr.
func TestEvalStdoutIsPureJSON(t *testing.T) {
	home := repoRoot(t)
	bin := t.TempDir() + "/proofspan"
	if out, err := exec.Command("go", "build", "-C", home, "-o", bin, "./cmd/proofspan").CombinedOutput(); err != nil {
		t.Skipf("could not build CLI binary: %v: %s", err, out)
	}
	db := t.TempDir() + "/stdout.sqlite"

	migrate := exec.Command(bin, "migrate", "--from=langsmith", "--db="+db, home+"/testdata/corpus/langsmith/corpus.jsonl")
	if out, err := migrate.CombinedOutput(); err != nil {
		t.Fatalf("migrate: %v: %s", err, out)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(bin, "eval", "--db="+db)
	cmd.Dir = home // repo-relative defaults: judges/manifest.json, registry/bin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("eval: %v (stderr: %s)", err, stderr.String())
	}
	var reports []any
	if err := json.Unmarshal(stdout.Bytes(), &reports); err != nil {
		t.Errorf("stdout must be pure JSON (jq-consumable), got decode error %v; stdout head: %q", err, stdout.String()[:min(80, stdout.Len())])
	}
	if !strings.Contains(stderr.String(), "eval: 400/400 trajectories pass") {
		t.Errorf("human summary line belongs on stderr; stderr = %q", stderr.String())
	}
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

// repoRoot walks up from the test's working directory to the directory
// containing cmd/proofspan — the repo root, wherever it's checked out
// (local dev machine or CI runner).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "proofspan")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("repo root (cmd/proofspan) not found above the test directory")
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
