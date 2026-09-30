// Command proofspan is the single binary: version, migrate, eval, serve.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanngiroux-star/proofspan/internal/assert/wasm"
	"github.com/jonathanngiroux-star/proofspan/internal/corpus"
	evalpkg "github.com/jonathanngiroux-star/proofspan/internal/eval"
	"github.com/jonathanngiroux-star/proofspan/internal/judges"
	"github.com/jonathanngiroux-star/proofspan/internal/migrate/honeyhive"
	"github.com/jonathanngiroux-star/proofspan/internal/migrate/langsmith"
	"github.com/jonathanngiroux-star/proofspan/internal/schema"
	servepkg "github.com/jonathanngiroux-star/proofspan/internal/serve"
	"github.com/jonathanngiroux-star/proofspan/internal/store"
)

const versionString = "0.2.0"

func main() {
	// Single entry point: `proofspan` (interactive) → TUI, `proofspan desktop`
	// → GUI, anything else → the CLI.
	if target, _, err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "proofspan:", err)
		os.Exit(1)
	} else if target == uiTUI {
		if err := launchFrontEnd("tui"); err != nil {
			fmt.Fprintln(os.Stderr, "proofspan:", err)
			os.Exit(1)
		}
	} else if target == uiGUI {
		if err := launchFrontEnd("desktop"); err != nil {
			fmt.Fprintln(os.Stderr, "proofspan:", err)
			os.Exit(1)
		}
	}
	if len(os.Args) < 2 {
		// non-interactive bare invocation: show usage (scripts, CI)
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Printf("proofspan %s\nschema %s\n", versionString, schema.ATFVersion)
	case "migrate":
		if err := runMigrate(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "migrate:", err)
			os.Exit(1)
		}
	case "eval":
		if err := runEval(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "eval:", err)
			os.Exit(1)
		}
	case "report":
		if err := runReport(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "report:", err)
			os.Exit(1)
		}
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "serve:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: proofspan [command]

no command (interactive terminal): launch the TUI
  desktop                          launch the GUI
  tui                              launch the TUI (even when piped)

commands:
  version                                             print binary and schema version
  migrate --from=SOURCE [--dry-run] [--db=DB] FILE   convert a SOURCE export (langsmith|honeyhive)
                                                     --dry-run prints a JSON plan, no writes
  report --from=SOURCE [--out=DIR] FILE              analyze a real export pre-migration:
                                                     parse rate, field coverage, unknown-key census
  eval [--db=DB] [--trajectory=ID] [--judges-run=a,b] run assertions/judges over stored trajectories
  serve [--db=DB] [--addr=:7400] [--scim]             local server (SCIM persists to SQLite)
`)
}

// shared flag: database path
func dbFlag(fs *flag.FlagSet) *string {
	return fs.String("db", "proofspan.sqlite", "SQLite database path")
}

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	from := fs.String("from", "", "source system: langsmith|honeyhive")
	dryRun := fs.Bool("dry-run", false, "print planned diff instead of writing output")
	format := fs.String("format", "json", "output format: json")
	dbPath := dbFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("--from is required (langsmith|honeyhive)")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("exactly one input FILE required")
	}
	if *format != "json" {
		return fmt.Errorf("unsupported format %q (only json)", *format)
	}
	path := fs.Arg(0)
	if *dryRun {
		switch *from {
		case "langsmith":
			return langsmith.MigrateDryRun(os.Stdout, path)
		case "honeyhive":
			return honeyhive.MigrateDryRun(os.Stdout, path)
		default:
			return fmt.Errorf("unknown source %q", *from)
		}
	}
	// write path: convert, then ingest into SQLite
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	switch *from {
	case "langsmith":
		conv, err := langsmith.ConvertFile(path)
		if err != nil {
			return err
		}
		return ingest(st, conv.Trajectories, conv.SpansByTrajectory, *dbPath)
	case "honeyhive":
		conv, err := honeyhive.ConvertFile(path)
		if err != nil {
			return err
		}
		return ingest(st, conv.Trajectories, conv.SpansByTrajectory, *dbPath)
	default:
		return fmt.Errorf("unknown source %q", *from)
	}
}

func ingest(st *store.Store, trajectories []schema.Trajectory, spansByTrajectory map[string][]schema.Span, dbPath string) error {
	written := 0
	for _, tr := range trajectories {
		spans := spansByTrajectory[tr.TrajectoryID]
		if err := st.SaveTrajectory(tr, spans); err != nil {
			return err
		}
		written += len(spans)
	}
	fmt.Printf("migrated %d trajectories, %d spans into %s\n", len(trajectories), written, dbPath)
	return nil
}

func runEval(args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	dbPath := dbFlag(fs)
	traj := fs.String("trajectory", "", "trajectory ID to evaluate (default: all)")
	regDir := fs.String("registry", "registry/bin", "directory of compiled assertion .wasm modules")
	judgesPath := fs.String("judges", "judges/manifest.json", "judge manifest (validated before assertions run)")
	assertions := fs.String("assertions", "", "comma-separated id@version pins to run (default: span-correlation@1.0.0)")
	runJudges := fs.String("judges-run", "", "comma-separated judge IDs to execute after assertions (e.g. factual-consistency)")
	judgeAPIKey := fs.String("judge-api-key", "", "API key for HTTP judges (overrides the provider's api_key_env; prefer env vars)")
	judgeEndpoint := fs.String("judge-endpoint", "", "endpoint override for HTTP judges, e.g. http://localhost:8000/v1 (beats the manifest)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// judge manifest is validated BEFORE any assertion runs (brief: required, not later)
	manifest, err := judges.Load(*judgesPath)
	if err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	reg, err := loadRegistry(*regDir)
	if err != nil {
		return err
	}
	ctx := context.Background()
	// Judge execution: providers are declared in the manifest (endpoint +
	// the env var holding the key); --judge-api-key/--judge-endpoint are
	// explicit overrides for ad-hoc providers. The key never persists.
	eng := judges.NewEngine(manifest)
	if *judgeAPIKey != "" {
		eng = eng.WithAPIKey(*judgeAPIKey)
	}
	if *judgeEndpoint != "" {
		eng = eng.WithEndpoint(*judgeEndpoint)
	}
	judgeIDs := splitCSV(*runJudges)
	runner := evalpkg.NewRunner(reg)
	pins := parsePins(*assertions)
	if len(pins) == 0 {
		pins = []struct{ id, ver string }{{"span-correlation", "1.0.0"}}
	}
	for _, p := range pins {
		runner.Pinned(p.id, p.ver)
	}
	if *traj != "" {
		_, spans, err := st.GetTrajectory(*traj)
		if err != nil {
			return err
		}
		rep := runner.EvalTrajectory(ctx, spans)
		if err := runJudgesOver(ctx, eng, st, judgeIDs, *traj, spans, rep); err != nil {
			return err
		}
		return printEvalReport([]*evalpkg.TrajectoryReport{rep})
	}
	ids, err := st.ListTrajectoryIDs()
	if err != nil {
		return err
	}
	var reports []*evalpkg.TrajectoryReport
	for _, id := range ids {
		_, spans, err := st.GetTrajectory(id)
		if err != nil {
			return err
		}
		rep := runner.EvalTrajectory(ctx, spans)
		if rep.TrajectoryID == "" {
			rep.TrajectoryID = id
		}
		if err := runJudgesOver(ctx, eng, st, judgeIDs, id, spans, rep); err != nil {
			return err
		}
		reports = append(reports, rep)
	}
	return printEvalReport(reports)
}

// runJudgesOver executes requested judges over one trajectory and folds
// their verdicts into the report + eval_runs persistence. No judges
// requested → no-op. Judge errors mark the trajectory errored, never pass.
func runJudgesOver(ctx context.Context, eng *judges.Engine, st *store.Store, judgeIDs []string, trajID string, spans []schema.Span, rep *evalpkg.TrajectoryReport) error {
	for _, jid := range judgeIDs {
		v, err := eng.Judge(ctx, jid, trajID, spans)
		if err != nil {
			rep.Errored++
			fmt.Printf("judge %s: error: %v\n", jid, err)
			continue
		}
		rep.Total++
		switch v.Status {
		case "pass":
			rep.Passed++
		case "fail":
			rep.Failed++
		default:
			rep.Errored++
		}
		fmt.Printf("judge %s: %s (%s) %s\n", jid, v.Status, v.JudgeFingerprint, v.Detail)
		detail := map[string]any{"detail": v.Detail, "score": v.Score}
		if len(v.Raw) > 0 {
			detail["raw"] = json.RawMessage(v.Raw)
		}
		if err := st.SaveEvalRun(store.EvalRun{
			ID:               "judge-" + jid + "-" + trajID,
			TrajectoryID:     trajID,
			AssertionID:      "judge:" + jid,
			AssertionVersion: "v1.1.0",
			JudgeID:          jid,
			JudgeFingerprint: v.JudgeFingerprint,
			Status:           v.Status,
			Detail:           mustJSON(detail),
			CreatedAtUnixMs:  time.Now().UnixMilli(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runReport analyzes a real vendor export without mutating it: parse rate,
// known-field coverage, unknown-key census, markdown + JSON output.
// This is the design-partner pre-migration report.
func runReport(args []string) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	from := fs.String("from", "", "source system: langsmith|honeyhive")
	out := fs.String("out", "", "output dir for corpus report markdown (default: stdout only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" {
		return fmt.Errorf("--from is required (langsmith|honeyhive)")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("exactly one input FILE required")
	}
	path := fs.Arg(0)
	rep, err := corpus.Analyze(path, *from, corpus.KnownKeys(*from))
	if err != nil {
		return err
	}
	// machine-readable report on stdout ONLY (pipeable); status on stderr
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(rep); err != nil {
		return err
	}
	if *out != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return err
		}
		md := rep.Markdown(*from, path)
		name := filepath.Join(*out, *from+".md")
		if err := os.WriteFile(name, []byte(md), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "corpus report written to %s\n", name)
	}
	return nil
}

func loadRegistry(dir string) (*wasm.Registry, error) {
	reg := wasm.NewRegistry(dir)
	// compile assertion modules from source if the bin dir is absent (dev path)
	if _, err := os.Stat(dir); err != nil {
		if err := buildRegistryFromSource(dir); err != nil {
			return nil, err
		}
	}
	if err := reg.LoadDir(dir); err != nil {
		return nil, err
	}
	return reg, nil
}

// buildRegistryFromSource compiles registry/*/main.go → registry/bin/<id>@<version>.wasm
func buildRegistryFromSource(binDir string) error {
	absBin, err := filepath.Abs(binDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(absBin, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir("registry")
	if err != nil {
		return fmt.Errorf("registry source not found: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		src := filepath.Join("registry", e.Name())
		if _, err := os.Stat(filepath.Join(src, "main.go")); err != nil {
			continue
		}
		out := filepath.Join(absBin, e.Name()+"@1.0.0.wasm")
		cmd := exec.Command("go", "build", "-C", src, "-buildmode=c-shared", "-o", out, ".")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out2, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %s", src, out2)
		}
	}
	return nil
}

func parsePins(s string) []struct{ id, ver string } {
	if s == "" {
		return nil
	}
	var pins []struct{ id, ver string }
	for _, part := range strings.Split(s, ",") {
		id, ver, ok := strings.Cut(strings.TrimSpace(part), "@")
		if ok && id != "" && ver != "" {
			pins = append(pins, struct{ id, ver string }{id, ver})
		}
	}
	return pins
}

func printEvalReport(reports []*evalpkg.TrajectoryReport) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	totalPass := 0
	for _, rep := range reports {
		if rep.Pass() {
			totalPass++
		}
	}
	fmt.Printf("eval: %d/%d trajectories pass\n", totalPass, len(reports))
	if err := enc.Encode(reports); err != nil {
		return err
	}
	for _, rep := range reports {
		if !rep.Pass() {
			return fmt.Errorf("eval gate failed: trajectory %s", rep.TrajectoryID)
		}
	}
	return nil
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	dbPath := dbFlag(fs)
	addr := fs.String("addr", "127.0.0.1:7400", "listen address")
	scimOn := fs.Bool("scim", false, "mount the SCIM minimal provider (feature-flagged)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer st.Close()
	scimToken := os.Getenv("PROOFSPAN_SCIM_TOKEN")
	srv, err := servepkg.NewServer(st, *scimOn, scimToken)
	if err != nil {
		return err
	}
	authNote := "open (dev mode: set PROOFSPAN_SCIM_TOKEN to require bearer auth)"
	if scimToken != "" {
		authNote = "bearer auth enforced"
	}
	fmt.Printf("proofspan serve listening on %s (scim=%v, auth=%s, db=%s)\n", *addr, *scimOn, authNote, *dbPath)
	fmt.Printf("note: HTTP only — terminate TLS at your reverse proxy before exposing beyond localhost\n")
	return http.ListenAndServe(*addr, srv.Handler())
}
