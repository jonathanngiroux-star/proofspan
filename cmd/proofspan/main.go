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

	"proofspan/internal/assert/wasm"
	evalpkg "proofspan/internal/eval"
	"proofspan/internal/judges"
	"proofspan/internal/migrate/honeyhive"
	"proofspan/internal/migrate/langsmith"
	"proofspan/internal/schema"
	servepkg "proofspan/internal/serve"
	"proofspan/internal/store"
)

const versionString = "0.1.0"

func main() {
	if len(os.Args) < 2 {
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
	fmt.Fprint(os.Stderr, `usage: proofspan <command> [args]

commands:
  version                                             print binary and schema version
  migrate --from=SOURCE [--dry-run] [--db=DB] FILE   convert a SOURCE export (langsmith|honeyhive)
                                                     --dry-run prints a JSON plan, no writes
  eval [--db=DB] --trajectory=ID                     run assertions over a stored trajectory
  serve [--db=DB] [--addr=:7400] [--scim]             local server (SCIM flag-gated)
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	// judge manifest is validated BEFORE any assertion runs (brief: required, not later)
	manifest, err := judges.Load(*judgesPath)
	if err != nil {
		return err
	}
	_ = manifest // fingerprints are recorded per-run once judge assertions exist in v0.2
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
		reports = append(reports, rep)
	}
	return printEvalReport(reports)
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
	srv := servepkg.NewServer(st, *scimOn)
	fmt.Printf("proofspan serve listening on %s (scim=%v)\n", *addr, *scimOn)
	return http.ListenAndServe(*addr, srv.Handler())
}
