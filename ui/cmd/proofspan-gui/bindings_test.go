package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/ui/internal/app"
)

// The GUI smoke suite (04-gui-audit.md): drives every bound method against
// the real proofspan binary over the shared corpus — the same path the
// frontend takes. Skips when the CLI can't be built (no toolchain).
//
// Covers the audit checks that don't need a display:
//   - PlanMigration: dry-run plan parses, counts match, mapping rows exist
//   - RunMigrate + ListTrajectories + GetTrajectory: fixture → spans visible
//   - RunEvalGetReports: pass/fail visible per trajectory
//   - Judge manifest: pins present; missing manifest → surfaced error
//   - Donate addresses: byte-for-byte
//   - No secrets in the echoed argv
func buildCLI(t *testing.T) (bin, home string) {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home = ""
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "proofspan")); err == nil {
			home = dir
			break
		}
		dir = filepath.Dir(dir)
	}
	if home == "" {
		t.Fatal("repo root (cmd/proofspan) not found above the test directory")
	}
	bin = t.TempDir() + "/proofspan"
	out, err := exec.Command("go", "build", "-C", home, "-o", bin, "./cmd/proofspan").CombinedOutput()
	if err != nil {
		t.Skipf("could not build CLI binary: %v: %s", err, out)
	}
	t.Setenv("PROOFSPAN_BIN", bin)
	return bin, home
}

func newGUIForTest() *GUI { return &GUI{} } // nil ctx → emit no-ops; headless

// migrateFixture ingests the langsmith corpus into a fresh DB via the exact
// argv the bound method produces.
func migrateFixture(t *testing.T, home, db string) {
	t.Helper()
	ls := filepath.Join(home, "testdata", "corpus", "langsmith", "corpus.jsonl")
	args, err := app.MigrateArgs(app.MigrateConfig{Source: "langsmith", File: ls, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	r := &app.Runner{Dir: home}
	var code int
	code, err = r.Run(args, nil, func(string) {})
	if err != nil || code != 0 {
		t.Fatalf("migrate fixture: code=%d err=%v", code, err)
	}
}

func TestDonateAddressesExact(t *testing.T) {
	d := newGUIForTest().GetDonateAddresses()
	if d.Ethereum != "0x85ee7E71f762d772599cbF1EC20E651B30657521" {
		t.Errorf("ethereum address drift: %q", d.Ethereum)
	}
	if d.Bitcoin != "bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg" {
		t.Errorf("bitcoin address drift: %q", d.Bitcoin)
	}
}

// The TUI hardcodes the same pair (app.DonateETH/DonateBTC) — the two
// front-ends must never drift apart.
func TestDonateAddressesMatchTUI(t *testing.T) {
	d := newGUIForTest().GetDonateAddresses()
	if d.Ethereum != app.DonateETH {
		t.Errorf("GUI/TUI ETH drift: %q vs %q", d.Ethereum, app.DonateETH)
	}
	if d.Bitcoin != app.DonateBTC {
		t.Errorf("GUI/TUI BTC drift: %q vs %q", d.Bitcoin, app.DonateBTC)
	}
}

func TestPlanMigrationParsesAndCounts(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	ls := filepath.Join(home, "testdata", "corpus", "langsmith", "corpus.jsonl")
	db := t.TempDir() + "/plan.sqlite"
	plan, err := g.PlanMigration(MigrateConfig{Source: "langsmith", File: ls, DB: db, DryRun: true})
	if err != nil {
		t.Fatalf("PlanMigration: %v", err)
	}
	if !plan.DryRun {
		t.Error("plan.dry_run must be true")
	}
	if plan.TargetVersion != "atf/v0.1.1" {
		t.Errorf("target_version = %q", plan.TargetVersion)
	}
	if plan.Trajectories != 400 || plan.SpansPlanned != 10000 {
		t.Errorf("counts: trj=%d spans=%d, want 400/10000", plan.Trajectories, plan.SpansPlanned)
	}
	if len(plan.FieldMappings) == 0 {
		t.Error("no field mappings in plan — the parity block is the product")
	}
	if plan.SampleSpan == nil {
		t.Error("no sample span in plan")
	}
	// read-only: a dry run must not create the DB file
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Errorf("dry run wrote the DB file (%v) — dry-run must be read-only", err)
	}
}

func TestPlanMigrationRejectsBadInput(t *testing.T) {
	g := newGUIForTest()
	if _, err := g.PlanMigration(MigrateConfig{Source: "openllmetry", File: "x.jsonl", DB: "db.sqlite", DryRun: true}); err == nil {
		t.Error("unknown source must error")
	}
	if _, err := g.PlanMigration(MigrateConfig{Source: "langsmith", File: "", DB: "db.sqlite", DryRun: true}); err == nil {
		t.Error("empty file must error")
	}
}

func TestTrajectoryViewerShowsSpansFromFixture(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	db := t.TempDir() + "/viewer.sqlite"
	migrateFixture(t, home, db)

	ids, err := g.ListTrajectories(db)
	if err != nil {
		t.Fatalf("ListTrajectories: %v", err)
	}
	if len(ids) != 400 {
		t.Fatalf("trajectory count = %d, want 400", len(ids))
	}
	bundle, err := g.GetTrajectory(db, ids[0])
	if err != nil {
		t.Fatalf("GetTrajectory: %v", err)
	}
	if len(bundle.Spans) == 0 {
		t.Fatal("span viewer shows no spans for a migrated fixture trajectory")
	}
	s := bundle.Spans[0]
	if s.SpanID == "" || s.Kind == "" || s.Name == "" {
		t.Errorf("span missing basic fields: %+v", s)
	}
}

func TestTrajectoryViewerOnEmptyDBShowsZero(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	db := t.TempDir() + "/empty.sqlite"
	// create the DB by migrating an empty file — the serve layer needs a
	// valid DB; an empty corpus yields zero trajectories
	emptyFile := t.TempDir() + "/empty.jsonl"
	if err := os.WriteFile(emptyFile, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	args, err := app.MigrateArgs(app.MigrateConfig{Source: "langsmith", File: emptyFile, DB: db})
	if err != nil {
		t.Fatal(err)
	}
	r := &app.Runner{Dir: home}
	if code, err := r.Run(args, nil, func(string) {}); err != nil || code != 0 {
		t.Fatalf("migrate empty: code=%d err=%v", code, err)
	}
	ids, err := g.ListTrajectories(db)
	if err != nil {
		t.Fatalf("ListTrajectories on empty: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("empty DB must list zero trajectories, got %d", len(ids))
	}
	_ = home
}

func TestEvalReportsShowPassFail(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	db := t.TempDir() + "/eval.sqlite"
	migrateFixture(t, home, db)

	t.Chdir(home) // repo root: judges/ + registry/ resolve like the e2e suite
	reports, err := g.RunEvalGetReports(EvalConfig{DB: db})
	if err != nil {
		t.Fatalf("RunEvalGetReports: %v", err)
	}
	if len(reports) != 400 {
		t.Fatalf("report count = %d, want 400", len(reports))
	}
	pass := 0
	for _, r := range reports {
		if r.Failed == 0 && r.Errored == 0 && r.Total > 0 {
			pass++
		}
	}
	if pass != 400 {
		t.Errorf("fixture corpus must be all-pass: pass=%d fail=%d", pass, 400-pass)
	}
	if len(reports[0].Results) == 0 {
		t.Error("report rows missing assertion results")
	}
}

func TestEvalReportsSingleTrajectory(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	db := t.TempDir() + "/single.sqlite"
	migrateFixture(t, home, db)
	t.Chdir(home)

	reports, err := g.RunEvalGetReports(EvalConfig{DB: db, Trajectory: "trj_00001"})
	if err != nil {
		t.Fatalf("RunEvalGetReports(trj_00001): %v", err)
	}
	if len(reports) != 1 || reports[0].TrajectoryID != "trj_00001" {
		t.Fatalf("want single trj_00001 report, got %d reports", len(reports))
	}
}

func TestJudgeManifestViewer(t *testing.T) {
	_, home := buildCLI(t)
	g := newGUIForTest()
	// DB path inside the repo → DetectResourcesDir finds the checkout
	m, err := g.GetJudgeManifest(filepath.Join(home, "proofspan.sqlite"))
	if err != nil {
		t.Fatalf("GetJudgeManifest: %v", err)
	}
	if m.Namespace != "proofspan/llm-judge-registry" {
		t.Errorf("namespace = %q", m.Namespace)
	}
	if len(m.Judges) == 0 {
		t.Fatal("no judges in manifest")
	}
	for _, j := range m.Judges {
		if j.ModelFingerprint == "" {
			t.Errorf("judge %s has no fingerprint pin — the viewer must surface pins", j.ID)
		}
	}
	for name, p := range m.Providers {
		if strings.Contains(strings.ToLower(p.Endpoint), "nvapi-") {
			t.Errorf("provider %s endpoint leaks a key", name)
		}
		if strings.Contains(p.APIKeyEnv, "nvapi-") {
			t.Errorf("provider %s api_key_env leaks a key", name)
		}
	}
}

func TestJudgeManifestMissingSurfacesError(t *testing.T) {
	g := newGUIForTest()
	t.Setenv("PROOFSPAN_MANIFEST", "/nonexistent/manifest.json")
	if _, err := g.GetJudgeManifest("db.sqlite"); err == nil {
		t.Error("missing manifest must surface an error the frontend can show")
	}
}

func TestNoSecretsInEchoedArgv(t *testing.T) {
	// The judge API key travels as --judge-api-key in argv; the echoed
	// command line the GUI logs must redact it (04-gui-audit: no secrets in
	// the window).
	args, err := app.EvalArgs(app.EvalConfig{DB: "d.sqlite", JudgeAPIKey: "nvapi-SECRET-KEY"})
	if err != nil {
		t.Fatal(err)
	}
	echoed := echoCommand(args)
	if strings.Contains(echoed, "nvapi-SECRET-KEY") {
		t.Errorf("echoed argv leaks the judge API key: %s", echoed)
	}
	if !strings.Contains(echoed, "--judge-api-key=***") {
		t.Errorf("echoed argv must show the redacted flag: %s", echoed)
	}
}
