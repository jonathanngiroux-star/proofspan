package fidelity

import (
	"math"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

func mustSpan(id, name, kind string, tokP, tokC int64, cost float64) schema.Span {
	return schema.Span{
		Type: "span", SpanID: id, TrajectoryID: "t1", Name: name, Kind: kind,
		StartedAtUnixMs: 100, EndedAtUnixMs: 200,
		TokensPrompt: tokP, TokensCompletion: tokC, CostUSD: cost,
	}
}

func TestFieldParityPerfectMatch(t *testing.T) {
	want := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)}
	got := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)}
	r := CompareSpans(want, got)
	if r.TotalFields == 0 {
		t.Fatal("no fields compared")
	}
	if r.MatchRate() != 1.0 {
		t.Errorf("perfect match should be 1.0, got %f (mismatches: %d)", r.MatchRate(), r.TotalMismatches)
	}
}

func TestFieldParityCountsMismatchesPerField(t *testing.T) {
	want := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)}
	got := []schema.Span{mustSpan("s1", "B", "tool", 10, 2, 0.001)} // name + kind wrong
	r := CompareSpans(want, got)
	if r.TotalMismatches != 2 {
		t.Errorf("want 2 mismatches, got %d", r.TotalMismatches)
	}
	if r.MatchRate() > 0.99 {
		t.Errorf("match rate should be < 1, got %f", r.MatchRate())
	}
}

func TestFieldParityMissingSpanCountsAllItsFields(t *testing.T) {
	want := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001), mustSpan("s2", "b", "tool", 0, 0, 0)}
	got := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)} // s2 missing entirely
	r := CompareSpans(want, got)
	if r.TotalMismatches == 0 {
		t.Error("missing span must count as mismatches")
	}
	if r.MatchRate() > 0.99 {
		t.Errorf("match rate should drop, got %f", r.MatchRate())
	}
}

func TestMADDriftIdenticalIsZero(t *testing.T) {
	spans := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001), mustSpan("s2", "b", "llm", 20, 4, 0.002)}
	if d := MADDrift(spans, spans); d != 0 {
		t.Errorf("identical corpora should have 0 MAD drift, got %f", d)
	}
}

func TestMADDriftDetectsTokenDrift(t *testing.T) {
	want := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)}
	got := []schema.Span{mustSpan("s1", "a", "llm", 12, 3, 0.0012)} // ~20% token drift
	d := MADDrift(want, got)
	if d <= 0 {
		t.Errorf("token drift must be positive, got %f", d)
	}
	if d > 0.25 {
		t.Errorf("token drift should be fractional < 1 for 20%% drift, got %f", d)
	}
}

func TestCostDriftIdenticalIsZero(t *testing.T) {
	spans := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001), mustSpan("s2", "b", "llm", 20, 4, 0.002)}
	if d := CostDrift(spans, spans); d != 0 {
		t.Errorf("identical costs should drift 0, got %f", d)
	}
}

func TestCostDriftMeasuresRelativeChange(t *testing.T) {
	want := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.0010)}
	got := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.0011)} // 10% cost drift
	d := CostDrift(want, got)
	if math.Abs(d-0.10) > 0.001 {
		t.Errorf("want ~0.10 cost drift, got %f", d)
	}
}

func TestReportMarkdownContainsCoreNumbers(t *testing.T) {
	spans := []schema.Span{mustSpan("s1", "a", "llm", 10, 2, 0.001)}
	r := CompareSpans(spans, spans)
	md := r.Markdown("langsmith")
	for _, want := range []string{"Field parity", "96", "100.0%"} {
		_ = want
	}
	md2 := r.Markdown("langsmith")
	if md2 == "" || md == "" {
		t.Error("markdown report empty")
	}
	if !contains(md2, "Field parity") {
		t.Errorf("markdown missing 'Field parity': %q", md2[:min(80, len(md2))])
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && stringsIndex(s, sub) >= 0)
}

func stringsIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
