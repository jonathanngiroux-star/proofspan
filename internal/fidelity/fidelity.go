// Package fidelity measures converter output against the native ATF
// corpus: field parity, MAD assertion drift, cost drift. These numbers
// are the product — CI fails below threshold.
package fidelity

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"proofspan/internal/schema"
)

// ParityThreshold is the CI gate: converters must match ≥95% of fields.
const ParityThreshold = 0.95

// MADDriftThreshold: median absolute drift of token counts must stay <2%.
const MADDriftThreshold = 0.02

// CostDriftThreshold: relative cost drift must stay <0.5%.
const CostDriftThreshold = 0.005

// FieldResult is the parity outcome for one field over one span.
type FieldResult struct {
	SpanID string
	Field  string
	Want   string
	Got    string
}

// ParityReport aggregates field parity across a converted corpus.
type ParityReport struct {
	Source           string        `json:"source"`
	SpansExpected    int           `json:"spans_expected"`
	SpansGot         int           `json:"spans_gotten"`
	TotalFields      int           `json:"total_fields"`
	TotalMismatches  int           `json:"total_mismatches"`
	MismatchedFields []FieldResult `json:"mismatched_fields,omitempty"`
}

// MatchRate returns the fraction of fields that matched.
func (r *ParityReport) MatchRate() float64 {
	if r.TotalFields == 0 {
		return 0
	}
	return 1.0 - float64(r.TotalMismatches)/float64(r.TotalFields)
}

// Pass reports whether the report clears the CI threshold.
func (r *ParityReport) Pass() bool { return r.MatchRate() >= ParityThreshold }

// comparedFields lists the span fields the parity check covers.
func comparedFields(s schema.Span) []FieldResult {
	fs := []FieldResult{
		{s.SpanID, "span_id", s.SpanID, ""},
		{s.SpanID, "trajectory_id", s.TrajectoryID, ""},
		{s.SpanID, "parent_span_id", s.ParentSpanID, ""},
		{s.SpanID, "name", s.Name, ""},
		{s.SpanID, "kind", s.Kind, ""},
		{s.SpanID, "started_at_unix_ms", fmt.Sprintf("%d", s.StartedAtUnixMs), ""},
		{s.SpanID, "ended_at_unix_ms", fmt.Sprintf("%d", s.EndedAtUnixMs), ""},
		{s.SpanID, "model", s.Model, ""},
		{s.SpanID, "input", s.Input, ""},
		{s.SpanID, "output", s.Output, ""},
		{s.SpanID, "tokens_prompt", fmt.Sprintf("%d", s.TokensPrompt), ""},
		{s.SpanID, "tokens_completion", fmt.Sprintf("%d", s.TokensCompletion), ""},
		{s.SpanID, "cost_usd", fmt.Sprintf("%.6f", s.CostUSD), ""},
		{s.SpanID, "error", s.Error, ""},
	}
	for k, v := range s.Attributes {
		fs = append(fs, FieldResult{s.SpanID, "attributes." + k, k + "=" + v, ""})
	}
	return fs
}

// CompareSpans checks every expected span field against the converted
// output, matched by span ID. Missing spans count all their fields as
// mismatches.
func CompareSpans(want, got []schema.Span) *ParityReport {
	rep := &ParityReport{SpansExpected: len(want), SpansGot: len(got)}
	gotByID := map[string]schema.Span{}
	for _, s := range got {
		gotByID[s.SpanID] = s
	}
	for _, w := range want {
		g, ok := gotByID[w.SpanID]
		if !ok {
			// entire span missing: all fields mismatch
			for _, f := range comparedFields(w) {
				rep.TotalFields++
				rep.TotalMismatches++
				rep.MismatchedFields = append(rep.MismatchedFields, FieldResult{w.SpanID, f.Field, f.Want, "<missing>"})
			}
			continue
		}
		for _, wf := range comparedFields(w) {
			rep.TotalFields++
			var gotVal string
			switch wf.Field {
			case "span_id":
				gotVal = g.SpanID
			case "trajectory_id":
				gotVal = g.TrajectoryID
			case "parent_span_id":
				gotVal = g.ParentSpanID
			case "name":
				gotVal = g.Name
			case "kind":
				gotVal = g.Kind
			case "started_at_unix_ms":
				gotVal = fmt.Sprintf("%d", g.StartedAtUnixMs)
			case "ended_at_unix_ms":
				gotVal = fmt.Sprintf("%d", g.EndedAtUnixMs)
			case "model":
				gotVal = g.Model
			case "input":
				gotVal = g.Input
			case "output":
				gotVal = g.Output
			case "tokens_prompt":
				gotVal = fmt.Sprintf("%d", g.TokensPrompt)
			case "tokens_completion":
				gotVal = fmt.Sprintf("%d", g.TokensCompletion)
			case "cost_usd":
				gotVal = fmt.Sprintf("%.6f", g.CostUSD)
			case "error":
				gotVal = g.Error
			default:
				if strings.HasPrefix(wf.Field, "attributes.") {
					gotVal = g.Attributes[strings.TrimPrefix(wf.Field, "attributes.")] // key presence differs; value compare below
					gotVal = strings.TrimPrefix(wf.Field, "attributes.") + "=" + gotVal
				}
			}
			if wf.Want != gotVal {
				rep.TotalMismatches++
				rep.MismatchedFields = append(rep.MismatchedFields, FieldResult{w.SpanID, wf.Field, wf.Want, gotVal})
			}
		}
	}
	return rep
}

// MADDrift returns the median absolute drift between token counts of
// matched spans, as a fraction of the want value. Zero on identical.
func MADDrift(want, got []schema.Span) float64 {
	drifts := []float64{}
	gotByID := map[string]schema.Span{}
	for _, s := range got {
		gotByID[s.SpanID] = s
	}
	for _, w := range want {
		g, ok := gotByID[w.SpanID]
		if !ok {
			drifts = append(drifts, 1.0) // missing span = 100% drift
			continue
		}
		base := float64(w.TokensPrompt + w.TokensCompletion)
		if base == 0 {
			continue // no tokens on this span; nothing to drift
		}
		diff := math.Abs(float64(g.TokensPrompt+g.TokensCompletion) - base)
		drifts = append(drifts, diff/base)
	}
	if len(drifts) == 0 {
		return 0
	}
	sort.Float64s(drifts)
	mid := len(drifts) / 2
	if len(drifts)%2 == 1 {
		return drifts[mid]
	}
	return (drifts[mid-1] + drifts[mid]) / 2
}

// CostDrift returns the relative drift of total cost between corpora.
func CostDrift(want, got []schema.Span) float64 {
	sum := func(spans []schema.Span) float64 {
		t := 0.0
		for _, s := range spans {
			t += s.CostUSD
		}
		return t
	}
	w, g := sum(want), sum(got)
	if w == 0 {
		return 0
	}
	return math.Abs(g-w) / w
}

// Markdown renders the parity report as the fidelity markdown doc.
func (r *ParityReport) Markdown(source string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Fidelity report: %s → ATF v0.1.1\n\n", source)
	fmt.Fprintf(&b, "Generated from the shared 10k-step corpus (testdata/corpus/).\n\n")
	fmt.Fprintf(&b, "| Metric | Value | Target | Pass |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| Field parity | %.1f%% | ≥95%% | %s |\n", r.MatchRate()*100, mark(r.Pass()))
	fmt.Fprintf(&b, "| Spans expected / converted | %d / %d | 100%% | %s |\n",
		r.SpansExpected, r.SpansGot, mark(r.SpansExpected == r.SpansGot))
	if len(r.MismatchedFields) > 0 {
		fmt.Fprintf(&b, "\n## Mismatches (%d)\n\n", r.TotalMismatches)
		fmt.Fprintf(&b, "| Span | Field | Want | Got |\n|---|---|---|---|\n")
		for _, m := range firstN(r.MismatchedFields, 50) {
			fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", m.SpanID, m.Field, m.Want, m.Got)
		}
		if len(r.MismatchedFields) > 50 {
			fmt.Fprintf(&b, "\n(+ %d more)\n", len(r.MismatchedFields)-50)
		}
	}
	return b.String()
}

func mark(b bool) string {
	if b {
		return "✅"
	}
	return "❌"
}

func firstN[T any](xs []T, n int) []T {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}
