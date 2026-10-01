package wasm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

// The registry test compiles the span-correlation assertion from
// registry/span-correlation to WASM (wasip1, //go:wasmexport), loads it
// through the runtime, and executes it against fixture spans.
// This mirrors exactly what `dagger call eval` will do in CI.

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	// copy the assertion source into the temp module dir
	src := filepath.Join("..", "..", "..", "registry", "span-correlation")
	bin := filepath.Join(dir, "span-correlation.wasm")
	cmd := exec.Command("go", "build", "-C", src, "-buildmode=c-shared", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wasm build failed: %s\n%s", err, out)
	}
	reg := NewRegistry(dir)
	if err := reg.Register("span-correlation", "1.0.0", bin); err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestParseName disambiguates the two filename contracts: <id>@<ver>.wasm
// splits on @; <id>-<ver>.wasm splits on the LAST hyphen (an id may contain
// hyphens: span-correlation-2@1.0.0.wasm is id "span-correlation-2").
// A bare <id>.wasm with no separator must error, not silently register.
func TestParseName(t *testing.T) {
	for _, tc := range []struct {
		stem    string
		wantID  string
		wantVer string
		wantErr bool
	}{
		{"span-correlation@1.0.0", "span-correlation", "1.0.0", false},
		{"span-correlation-1.0.0", "span-correlation", "1.0.0", false},
		{"span-correlation-2@1.0.0", "span-correlation-2", "1.0.0", false},
		{"span-correlation-2-1.0.0", "span-correlation-2", "1.0.0", false},
		{"bare", "", "", true},
	} {
		id, ver, err := parseName(tc.stem)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: want error, got (%q, %q)", tc.stem, id, ver)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tc.stem, err)
			continue
		}
		if id != tc.wantID || ver != tc.wantVer {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", tc.stem, id, ver, tc.wantID, tc.wantVer)
		}
	}
}

func TestSpanCorrelationPassesCorrelatedTrajectory(t *testing.T) {
	reg := newTestRegistry(t)
	spans := []schema.Span{
		{SpanID: "s1", TrajectoryID: "t1", Kind: "tool", Name: "search", StartedAtUnixMs: 100, EndedAtUnixMs: 200},
		{SpanID: "s2", TrajectoryID: "t1", Kind: "llm", Name: "answer", ParentSpanID: "s1", StartedAtUnixMs: 210, EndedAtUnixMs: 400},
	}
	res := reg.Run(t.Context(), "span-correlation", "1.0.0", spans)
	if res.Status != "pass" {
		t.Errorf("correlated trajectory must pass, got %s: %s", res.Status, res.Detail)
	}
}

func TestSpanCorrelationFailsOrphan(t *testing.T) {
	reg := newTestRegistry(t)
	spans := []schema.Span{
		{SpanID: "s1", TrajectoryID: "t1", Kind: "tool", Name: "search", StartedAtUnixMs: 100, EndedAtUnixMs: 200},
		{SpanID: "s2", TrajectoryID: "t1", Kind: "llm", Name: "answer", ParentSpanID: "nonexistent", StartedAtUnixMs: 210, EndedAtUnixMs: 400},
	}
	res := reg.Run(t.Context(), "span-correlation", "1.0.0", spans)
	if res.Status != "fail" {
		t.Errorf("orphan span must fail, got %s: %s", res.Status, res.Detail)
	}
}

func TestSpanCorrelationFailsOverlap(t *testing.T) {
	reg := newTestRegistry(t)
	spans := []schema.Span{
		{SpanID: "s1", TrajectoryID: "t1", Kind: "tool", StartedAtUnixMs: 100, EndedAtUnixMs: 300},
		{SpanID: "s2", TrajectoryID: "t1", Kind: "tool", ParentSpanID: "s1", StartedAtUnixMs: 150, EndedAtUnixMs: 400},
	}
	res := reg.Run(t.Context(), "span-correlation", "1.0.0", spans)
	if res.Status != "fail" {
		t.Errorf("child starting before parent ends must fail, got %s: %s", res.Status, res.Detail)
	}
}

func TestSpanCorrelationEmptyTrajectoryPasses(t *testing.T) {
	reg := newTestRegistry(t)
	res := reg.Run(t.Context(), "span-correlation", "1.0.0", nil)
	if res.Status != "pass" {
		t.Errorf("empty trajectory is trivially correlated, got %s", res.Status)
	}
}

func TestRegistryRejectsUnknownAssertion(t *testing.T) {
	reg := newTestRegistry(t)
	res := reg.Run(t.Context(), "no-such", "9.9.9", nil)
	if res.Status != "error" {
		t.Errorf("unknown assertion must error, got %s", res.Status)
	}
}

func TestRegistryRejectsVersionMismatch(t *testing.T) {
	reg := newTestRegistry(t)
	res := reg.Run(t.Context(), "span-correlation", "9.9.9", nil)
	if res.Status != "error" {
		t.Errorf("wrong version pin must error, got %s: %s", res.Status, res.Detail)
	}
}

func TestRegisteredVersionIsReported(t *testing.T) {
	reg := newTestRegistry(t)
	res := reg.Run(t.Context(), "span-correlation", "1.0.0", nil)
	if res.AssertionID != "span-correlation" || res.AssertionVersion != "1.0.0" {
		t.Errorf("result must carry id/version: %+v", res)
	}
}
