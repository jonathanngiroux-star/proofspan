package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/jonathanngiroux-star/proofspan/ui/internal/app"
)

// GUI state shared by all bound methods. Wails binds every exported method
// of the struct passed in main.go's Bind list.
type guiState struct {
	mu        sync.Mutex
	running   bool
	serveProc *app.ServeProcess
}

// runCmd executes one proofspan command in a goroutine, streaming each output
// line to the frontend as an "output" event. Guards against concurrent runs
// (same behavior as the previous Fyne GUI and the TUI). Workdir = the DB's
// directory so the CLI's repo-checkout auto-detection (judges/ + registry/)
// resolves the same way it does for a CLI user in that directory.
func (g *GUI) runCmd(label string, args []string, extraEnv map[string]string, dbPath string) error {
	g.state.mu.Lock()
	if g.state.running {
		g.state.mu.Unlock()
		return fmt.Errorf("a command is already running; wait for it to finish")
	}
	g.state.running = true
	g.state.mu.Unlock()

	emit(g.ctx, "status", map[string]any{"text": "Running: " + label + "…"})
	emit(g.ctx, "output", map[string]any{"line": echoCommand(args)})

	r := &app.Runner{Dir: app.DBDir(dbPath)}
	go func() {
		defer func() {
			g.state.mu.Lock()
			g.state.running = false
			g.state.mu.Unlock()
		}()
		code, err := r.Run(args, extraEnv, func(line string) {
			emit(g.ctx, "output", map[string]any{"line": line})
		})
		if err != nil {
			emit(g.ctx, "output", map[string]any{"line": "error: " + err.Error()})
			emit(g.ctx, "status", map[string]any{"text": label + ": failed to start"})
			return
		}
		emit(g.ctx, "output", map[string]any{"line": fmt.Sprintf("[exit %d]", code)})
		status := label + ": done ✓"
		if code != 0 {
			status = fmt.Sprintf("%s: exit %d (see output)", label, code)
		}
		emit(g.ctx, "status", map[string]any{"text": status})
		emit(g.ctx, "cmd:done", map[string]any{"label": label, "code": code})
	}()
	return nil
}

// emit sends an event to the frontend; no-ops on a nil ctx (tests that call
// bindings directly without a running Wails runtime).
func emit(ctx context.Context, name string, data any) {
	if ctx == nil {
		return
	}
	runtime.EventsEmit(ctx, name, data)
}

// echoCommand renders the argv for the log pane with secret values redacted.
// The judge API key travels as --judge-api-key=VALUE in argv (visible in ps
// for the child's lifetime) — the log must never repeat it. Values of every
// flag whose name contains "key" or "token" become ***.
func echoCommand(args []string) string {
	const redacted = "***"
	parts := make([]string, len(args))
	for i, a := range args {
		if eq := strings.IndexByte(a, '='); eq > 0 {
			name := a[:eq]
			if strings.Contains(name, "key") || strings.Contains(name, "token") {
				parts[i] = name + "=" + redacted
				continue
			}
		}
		parts[i] = a
	}
	return "$ proofspan " + strings.Join(parts, " ")
}

// capture runs the CLI and returns pure stdout (the machine-readable
// channel; status goes to stderr). Nonzero exits return stderr text so the
// frontend shows the real failure, not a generic error. Workdir follows the
// DB's directory, and eval gets --judges/--registry pinned from the
// auto-detected repo checkout (same resolution as the TUI), so eval works
// from any DB location.
func (g *GUI) capture(args []string, dbPath string) ([]byte, error) {
	bin := app.ResolveBinary()
	args = pinResources(args, dbPath)
	cmd := exec.Command(bin, args...)
	cmd.Dir = app.DBDir(dbPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = fmt.Sprintf("exit %d", ee.ExitCode())
			}
			return nil, fmt.Errorf("%s: %s", bin, msg)
		}
		return nil, fmt.Errorf("start %s: %w (is the binary on PATH? set PROOFSPAN_BIN)", bin, err)
	}
	return stdout.Bytes(), nil
}

// pinResources pins --judges/--registry on eval argv when a repo checkout is
// detected — first walking up from the DB's directory, then from the
// process working directory (where the GUI was launched). Mirrors the
// TUI's EvalCommandArgs so eval runs from any DB location.
func pinResources(args []string, dbPath string) []string {
	if len(args) == 0 || args[0] != "eval" {
		return args
	}
	for _, a := range args {
		if strings.HasPrefix(a, "--judges=") || strings.HasPrefix(a, "--registry=") {
			return args // caller pinned explicitly; never override
		}
	}
	root := app.DetectResourcesDir(absDir(dbPath))
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			root = app.DetectResourcesDir(cwd)
		}
	}
	if root == "" {
		return args
	}
	out := make([]string, len(args), len(args)+2)
	copy(out, args)
	out = append(out,
		"--judges="+filepath.Join(root, "judges", "manifest.json"),
		"--registry="+filepath.Join(root, "registry", "bin"),
	)
	return out
}

// absDir returns the absolute directory of dbPath — the walk-up in
// DetectResourcesDir must start from a real location, not "." or "".
func absDir(dbPath string) string {
	if dbPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			return cwd
		}
		return "."
	}
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return "."
	}
	return filepath.Dir(abs)
}

// ---- Analyze (report) ----------------------------------------------------

// ReportConfig mirrors app.ReportConfig for Wails binding.
type ReportConfig struct{ Source, File, OutDir string }

// RunReport drives `proofspan report` (read-only export analysis).
func (g *GUI) RunReport(c ReportConfig) error {
	args, err := app.ReportArgs(app.ReportConfig{Source: c.Source, File: c.File, OutDir: c.OutDir})
	if err != nil {
		return err
	}
	return g.runCmd("Analyze", args, nil, c.File)
}

// ---- Migrate ---------------------------------------------------------------

// MigrateConfig mirrors app.MigrateConfig for Wails binding.
type MigrateConfig struct {
	Source string
	File   string
	DB     string
	DryRun bool
}

// RunMigrate drives `proofspan migrate` (ingest; --dry-run plans only).
func (g *GUI) RunMigrate(c MigrateConfig) error {
	args, err := app.MigrateArgs(app.MigrateConfig{Source: c.Source, File: c.File, DB: c.DB, DryRun: c.DryRun})
	if err != nil {
		return err
	}
	return g.runCmd("Migrate", args, nil, c.DB)
}

// DryRunPlan is the parsed `migrate --dry-run` JSON (the DiffSkeleton shape
// both converters emit): counts, the full field-mapping table, the
// dropped-field census, and one fully converted sample span.
type DryRunPlan struct {
	DryRun        bool            `json:"dry_run"`
	Source        string          `json:"source"`
	TargetVersion string          `json:"target_version"`
	InputFile     string          `json:"input_file"`
	Trajectories  int             `json:"trajectories"`
	RunsRead      int             `json:"runs_read"`
	SpansPlanned  int             `json:"spans_planned"`
	FieldMappings []FieldMapping  `json:"field_mappings"`
	DroppedFields []DroppedField  `json:"dropped_fields"`
	SampleSpan    json.RawMessage `json:"sample_span,omitempty"`
}

// FieldMapping is one row of the converter's field-mapping table.
type FieldMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
}

// DroppedField is one row of the unmapped-key census.
type DroppedField struct {
	Field string `json:"field"`
	Note  string `json:"note"`
}

// PlanMigration runs `migrate --dry-run` and returns the parsed plan — the
// machine-readable diff before any write. Read-only: no ingests.
func (g *GUI) PlanMigration(c MigrateConfig) (*DryRunPlan, error) {
	args, err := app.MigrateArgs(app.MigrateConfig{Source: c.Source, File: c.File, DB: c.DB, DryRun: true})
	if err != nil {
		return nil, err
	}
	out, err := g.capture(args, c.File)
	if err != nil {
		return nil, err
	}
	var plan DryRunPlan
	if err := json.Unmarshal(out, &plan); err != nil {
		return nil, fmt.Errorf("parse dry-run plan: %w", err)
	}
	return &plan, nil
}

// ---- Evaluate --------------------------------------------------------

// EvalConfig mirrors app.EvalConfig for Wails binding.
type EvalConfig struct {
	DB            string
	Trajectory    string
	Assertions    string
	JudgesRun     string
	JudgeEndpoint string
	JudgeAPIKey   string
}

// RunEval drives `proofspan eval` with streamed output (the pass/fail gate
// is reported via events and the output stream, exit code included).
func (g *GUI) RunEval(c EvalConfig) error {
	args, err := app.EvalArgs(app.EvalConfig{
		DB:            c.DB,
		Trajectory:    c.Trajectory,
		Assertions:    c.Assertions,
		JudgesRun:     c.JudgesRun,
		JudgeEndpoint: c.JudgeEndpoint,
		JudgeAPIKey:   c.JudgeAPIKey,
	})
	if err != nil {
		return err
	}
	return g.runCmd("Evaluate", args, nil, c.DB)
}

// TrajectoryReportView mirrors internal/eval.TrajectoryReport (eval stdout
// shape). A failing gate is data, not an error — the frontend renders
// pass/fail per trajectory from these.
type TrajectoryReportView struct {
	TrajectoryID string           `json:"trajectory_id"`
	Results      []EvalResultView `json:"results"`
	Total        int              `json:"total"`
	Passed       int              `json:"passed"`
	Failed       int              `json:"failed"`
	Errored      int              `json:"errored"`
}

// EvalResultView mirrors internal/assert/wasm.Result.
type EvalResultView struct {
	AssertionID      string `json:"assertion_id"`
	AssertionVersion string `json:"assertion_version"`
	Status           string `json:"status"`
	Detail           string `json:"detail,omitempty"`
}

// RunEvalGetReports runs eval and returns parsed per-trajectory reports.
// Exit≠0 (a failing gate) is returned as reports, not an error, so the
// frontend can render pass/fail; exec failures still error.
func (g *GUI) RunEvalGetReports(c EvalConfig) ([]TrajectoryReportView, error) {
	args, err := app.EvalArgs(app.EvalConfig{
		DB:            c.DB,
		Trajectory:    c.Trajectory,
		Assertions:    c.Assertions,
		JudgesRun:     c.JudgesRun,
		JudgeEndpoint: c.JudgeEndpoint,
		JudgeAPIKey:   c.JudgeAPIKey,
	})
	if err != nil {
		return nil, err
	}
	out, err := g.capture(args, c.DB)
	if err != nil {
		return nil, err
	}
	var reports []TrajectoryReportView
	if err := json.Unmarshal(out, &reports); err != nil {
		return nil, fmt.Errorf("parse eval reports: %w", err)
	}
	return reports, nil
}

// ---- Serve ------------------------------------------------------------

// ServeConfig mirrors app.ServeConfig for Wails binding.
type ServeConfig struct {
	DB        string
	Addr      string
	SCIM      bool
	SCIMToken string
}

// StartServe launches `proofspan serve` as a managed process. SCIM token →
// environment only, never argv (it would be visible in ps output).
func (g *GUI) StartServe(c ServeConfig) error {
	g.state.mu.Lock()
	if g.state.serveProc != nil && g.state.serveProc.Running() {
		g.state.mu.Unlock()
		return fmt.Errorf("the server is already up")
	}
	g.state.mu.Unlock()

	args, env, err := app.ServeArgs(app.ServeConfig{DB: c.DB, Addr: c.Addr, SCIM: c.SCIM, SCIMToken: c.SCIMToken})
	if err != nil {
		return err
	}
	pretty := "$ proofspan " + strings.Join(args, " ")
	if c.SCIM && c.SCIMToken != "" {
		pretty += "  (PROOFSPAN_SCIM_TOKEN=***)"
	}
	emit(g.ctx, "output", map[string]any{"line": pretty})
	proc, err := app.StartServe(args, env, app.DBDir(c.DB), func(line string) {
		emit(g.ctx, "output", map[string]any{"line": line})
	})
	if err != nil {
		return err
	}
	g.state.mu.Lock()
	g.state.serveProc = proc
	g.state.mu.Unlock()
	emit(g.ctx, "status", map[string]any{"text": "Serve: running at " + c.Addr})
	emit(g.ctx, "serve:state", map[string]any{"running": true, "addr": c.Addr})
	return nil
}

// StopServe kills the whole process group so the server cannot outlive the GUI.
func (g *GUI) StopServe() error {
	g.state.mu.Lock()
	defer g.state.mu.Unlock()
	if g.state.serveProc == nil || !g.state.serveProc.Running() {
		return fmt.Errorf("no server to stop")
	}
	if err := g.state.serveProc.Stop(); err != nil {
		return err
	}
	g.state.serveProc = nil
	emit(g.ctx, "status", map[string]any{"text": "Serve: stopped"})
	emit(g.ctx, "output", map[string]any{"line": "[serve stopped]"})
	emit(g.ctx, "serve:state", map[string]any{"running": false})
	return nil
}

// ---- trajectories viewer (public serve API, no data-model fork) --------

// TrajectoryBundle is the /v1/trajectories/{id} payload.
type TrajectoryBundle struct {
	Trajectory json.RawMessage `json:"trajectory"`
	Spans      []SpanView      `json:"spans"`
}

// SpanView is one span for the viewer (schema fields + raw JSON details).
type SpanView struct {
	SpanID       string          `json:"span_id"`
	TrajectoryID string          `json:"trajectory_id"`
	Name         string          `json:"name"`
	Kind         string          `json:"kind"`
	StartedAt    int64           `json:"started_at_unix_ms"`
	EndedAt      int64           `json:"ended_at_unix_ms"`
	Input        string          `json:"input"`
	Output       string          `json:"output"`
	Raw          json.RawMessage `json:"raw,omitempty"`
}

// ListTrajectories returns stored trajectory IDs via the ephemeral-serve
// pattern: start serve on a free port against the DB, GET /v1/trajectories
// over HTTP (the same public API an external tool uses), stop the server.
// No direct SQLite access from the UI layer.
func (g *GUI) ListTrajectories(db string) ([]string, error) {
	var ids []string
	err := g.withEphemeralServe(db, func(base string) error {
		body, err := httpGet(base + "/v1/trajectories")
		if err != nil {
			return err
		}
		var v struct {
			Trajectories []string `json:"trajectories"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return fmt.Errorf("parse trajectory list: %w", err)
		}
		ids = v.Trajectories
		return nil
	})
	return ids, err
}

// GetTrajectory returns one trajectory + its spans, same pattern.
func (g *GUI) GetTrajectory(db, id string) (*TrajectoryBundle, error) {
	var bundle *TrajectoryBundle
	err := g.withEphemeralServe(db, func(base string) error {
		body, err := httpGet(base + "/v1/trajectories/" + id)
		if err != nil {
			return err
		}
		var raw struct {
			Trajectory json.RawMessage `json:"trajectory"`
			Spans      []SpanView      `json:"spans"`
		}
		if err := json.Unmarshal(body, &raw); err != nil {
			return fmt.Errorf("parse trajectory: %w", err)
		}
		bundle = &TrajectoryBundle{Trajectory: raw.Trajectory, Spans: raw.Spans}
		return nil
	})
	return bundle, err
}

// withEphemeralServe starts `proofspan serve` on a free port against db,
// waits for /healthz, runs fn(base), then stops the server (process-group
// kill). The server is never left running by a read.
func (g *GUI) withEphemeralServe(db string, fn func(base string) error) error {
	if db == "" {
		return fmt.Errorf("database path required")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("find free port: %w", err)
	}
	addr := l.Addr().String()
	l.Close()
	args, env, err := app.ServeArgs(app.ServeConfig{DB: db, Addr: addr})
	if err != nil {
		return err
	}
	proc, err := app.StartServe(args, env, app.DBDir(db), func(line string) {})
	if err != nil {
		return fmt.Errorf("start serve: %w (is the binary on PATH? set PROOFSPAN_BIN)", err)
	}
	defer proc.Stop()
	base := "http://" + addr
	if err := waitForHealthz(base, 5*time.Second); err != nil {
		return err
	}
	return fn(base)
}

func waitForHealthz(base string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/healthz")
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("server did not answer /healthz within %s", timeout)
}

func httpGet(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s: %d %s", url, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(resp.Body)
}

// ---- judge manifest viewer (read-only) --------------------------------

// JudgeManifestView is the judge manifest for the viewer.
type JudgeManifestView struct {
	Namespace string                  `json:"namespace"`
	Version   string                  `json:"version"`
	Providers map[string]ProviderView `json:"providers"`
	Judges    []JudgeView             `json:"judges"`
}

// ProviderView shows a provider's endpoint and the NAME of the env var its
// key is read from — never the key itself.
type ProviderView struct {
	Endpoint  string `json:"endpoint"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
}

// JudgeView is one judge entry (id, fingerprint pin, description).
type JudgeView struct {
	ID               string `json:"id"`
	ModelFingerprint string `json:"model_fingerprint"`
	Description      string `json:"description"`
	Provider         string `json:"provider,omitempty"`
}

// GetJudgeManifest returns the manifest as structured JSON. Resolution (same
// order as the CLI): $PROOFSPAN_MANIFEST, else the repo checkout
// auto-detected by walking up from the DB's directory, else from the
// process working directory (same fallback ladder as pinResources — a DB
// outside the checkout must still resolve when the GUI runs inside one).
func (g *GUI) GetJudgeManifest(db string) (*JudgeManifestView, error) {
	manifestPath := os.Getenv("PROOFSPAN_MANIFEST")
	if manifestPath == "" {
		// DBDir of a bare filename is "." — the walk-up in DetectResourcesDir
		// terminates immediately from "." (filepath.Dir(".") == "."), so
		// absolutize first, exactly like pinResources does (bindings.go absDir).
		root := app.DetectResourcesDir(absDir(db))
		if root == "" {
			if cwd, err := os.Getwd(); err == nil {
				root = app.DetectResourcesDir(cwd)
			}
		}
		if root == "" {
			return nil, fmt.Errorf("judge manifest not found: no repo checkout above the DB or the working directory (set PROOFSPAN_MANIFEST)")
		}
		manifestPath = filepath.Join(root, "judges", "manifest.json")
	}
	b, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w (set PROOFSPAN_MANIFEST or point the DB inside a repo checkout)", err)
	}
	var view JudgeManifestView
	if err := json.Unmarshal(b, &view); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &view, nil
}

// ---- donate ---------------------------------------------------------------

// DonateAddresses is the exact pair from the README/GOVERNANCE.
type DonateAddresses struct {
	Ethereum string `json:"ethereum"`
	Bitcoin  string `json:"bitcoin"`
}

// GetDonateAddresses returns the donation addresses. Optional support only;
// no payment forms, no gating — never.
func (g *GUI) GetDonateAddresses() DonateAddresses {
	return DonateAddresses{
		Ethereum: "0x85ee7E71f762d772599cbF1EC20E651B30657521",
		Bitcoin:  "bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg",
	}
}

// CopyToClipboard puts text on the system clipboard via the native Wails
// bridge. The webview's wails:// origin is NOT a secure context, so
// navigator.clipboard is undefined there and the JS copy buttons fall back
// to this binding.
func (g *GUI) CopyToClipboard(text string) error {
	return runtime.ClipboardSetText(g.ctx, text)
}

// ---- lifecycle ---------------------------------------------------------

// shutdown stops the serve process on window close so the server cannot
// outlive the GUI.
func (g *GUI) shutdown() {
	g.state.mu.Lock()
	proc := g.state.serveProc
	g.state.mu.Unlock()
	if proc != nil {
		_ = proc.Stop()
	}
}
