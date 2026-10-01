// Command fidelitygen runs both converters against the shared corpus
// and writes docs/fidelity/*.md plus a machine-readable summary JSON.
// CI reads the summary and fails below 95% parity / drift thresholds.
//
// Usage: fidelitygen <corpus-dir> <docs-dir>
//
// Expects <corpus-dir>/{atf,langsmith,honeyhive}/corpus.jsonl.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jonathanngiroux-star/proofspan/internal/fidelity"
	"github.com/jonathanngiroux-star/proofspan/internal/migrate/honeyhive"
	"github.com/jonathanngiroux-star/proofspan/internal/migrate/langsmith"
	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

type summary struct {
	Source        string  `json:"source"`
	SpansExpected int     `json:"spans_expected"`
	SpansGot      int     `json:"spans_gotten"`
	FieldParity   float64 `json:"field_parity"`
	MADDrift      float64 `json:"mad_drift"`
	CostDrift     float64 `json:"cost_drift"`
	Pass          bool    `json:"pass"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: fidelitygen <corpus-dir> <docs-dir>")
		os.Exit(2)
	}
	corpusDir, docsDir := os.Args[1], os.Args[2]

	want := loadATFSpans(filepath.Join(corpusDir, "atf", "corpus.jsonl"))
	if len(want) == 0 {
		fatal(fmt.Errorf("atf corpus empty"))
	}

	lsRes, err := langsmith.ConvertFile(filepath.Join(corpusDir, "langsmith", "corpus.jsonl"))
	if err != nil {
		fatal(err)
	}
	hhRes, err := honeyhive.ConvertFile(filepath.Join(corpusDir, "honeyhive", "corpus.jsonl"))
	if err != nil {
		fatal(err)
	}

	lsSpans := flattenLS(lsRes)
	hhSpans := flattenHH(hhRes)

	lsReport := fidelity.CompareSpans(want, lsSpans)
	lsReport.Source = "langsmith"
	lsMAD := fidelity.MADDrift(want, lsSpans)
	lsCost := fidelity.CostDrift(want, lsSpans)

	hhReport := fidelity.CompareSpans(want, hhSpans)
	hhReport.Source = "honeyhive"
	hhMAD := fidelity.MADDrift(want, hhSpans)
	hhCost := fidelity.CostDrift(want, hhSpans)

	if err := writeReport(docsDir, "langsmith.md", lsReport, lsMAD, lsCost); err != nil {
		fatal(err)
	}
	if err := writeReport(docsDir, "honeyhive.md", hhReport, hhMAD, hhCost); err != nil {
		fatal(err)
	}

	sums := []summary{
		{"langsmith", lsReport.SpansExpected, lsReport.SpansGot, lsReport.MatchRate(), lsMAD, lsCost, gate(lsReport, lsMAD, lsCost)},
		{"honeyhive", hhReport.SpansExpected, hhReport.SpansGot, hhReport.MatchRate(), hhMAD, hhCost, gate(hhReport, hhMAD, hhCost)},
	}

	f, err := os.Create(filepath.Join(docsDir, "summary.json"))
	if err != nil {
		fatal(err)
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(sums); err != nil {
		fatal(err)
	}

	failed := false
	for _, s := range sums {
		fmt.Printf("%s: parity=%.2f%% mad=%.4f cost=%.5f pass=%v\n",
			s.Source, s.FieldParity*100, s.MADDrift, s.CostDrift, s.Pass)
		if !s.Pass {
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}

func gate(r *fidelity.ParityReport, mad, cost float64) bool {
	return r.MatchRate() >= fidelity.ParityThreshold &&
		mad <= fidelity.MADDriftThreshold &&
		cost <= fidelity.CostDriftThreshold
}

func flattenLS(res *langsmith.Result) []schema.Span {
	// sort by span id: SpansByTrajectory is a map, and CostDrift sums
	// float64s — a varying order churns summary.json's cost_drift on
	// every regen with last-bit noise.
	var out []schema.Span
	for _, spans := range res.SpansByTrajectory {
		out = append(out, spans...)
	}
	sortSpansByID(out)
	return out
}

func flattenHH(res *honeyhive.Result) []schema.Span {
	// same determinism rule as flattenLS
	var out []schema.Span
	for _, spans := range res.SpansByTrajectory {
		out = append(out, spans...)
	}
	sortSpansByID(out)
	return out
}

func sortSpansByID(spans []schema.Span) {
	sort.Slice(spans, func(i, j int) bool { return spans[i].SpanID < spans[j].SpanID })
}

func writeReport(docsDir, name string, r *fidelity.ParityReport, mad, cost float64) error {
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(docsDir, name), []byte(fullMarkdown(r, mad, cost)), 0o644)
}

// fullMarkdown splices MAD and cost rows into the metrics table.
func fullMarkdown(r *fidelity.ParityReport, mad, cost float64) string {
	md := r.Markdown(r.Source)
	extra := fmt.Sprintf(
		"| MAD assertion drift | %.4f | <0.02 | %s |\n"+
			"| Cost drift | %.5f | <0.005 | %s |\n",
		mad, mark(mad <= fidelity.MADDriftThreshold),
		cost, mark(cost <= fidelity.CostDriftThreshold))
	idx := strings.Index(md, "\n\n## Mismatches")
	if idx < 0 {
		return md + "\n" + extra
	}
	return md[:idx] + "\n" + extra + md[idx:]
}

func mark(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

// loadATFSpans reads the native corpus and returns spans only.
func loadATFSpans(path string) []schema.Span {
	f, err := os.Open(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var spans []schema.Span
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			fatal(fmt.Errorf("%s: %w", path, err))
		}
		if probe.Type != "span" {
			continue
		}
		var s schema.Span
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			fatal(fmt.Errorf("%s: %w", path, err))
		}
		spans = append(spans, s)
	}
	return spans
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fidelitygen:", err)
	os.Exit(1)
}
