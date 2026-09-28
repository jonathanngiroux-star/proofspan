package store

import (
	"os"
	"path/filepath"
	"testing"

	"proofspan/internal/schema"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func trj(id string) schema.Trajectory {
	return schema.Trajectory{
		Type: "trajectory", Version: schema.ATFVersion,
		TrajectoryID: id, Source: "native", StartedAtUnixMs: 1727452800000,
		Metadata: map[string]string{"env": "test"},
	}
}

func trjAt(id string, startMs int64) schema.Trajectory {
	t := trj(id)
	t.StartedAtUnixMs = startMs
	return t
}

func span(id, trjID, parent string, kind string, cost float64) schema.Span {
	return schema.Span{
		Type: "span", SpanID: id, TrajectoryID: trjID, ParentSpanID: parent,
		Name: "op-" + id, Kind: kind,
		StartedAtUnixMs: 1727452800100, EndedAtUnixMs: 1727452800450,
		Model: "gpt-4o-2024-08-06", Input: "in", Output: "out",
		TokensPrompt: 100, TokensCompletion: 20, CostUSD: cost,
		Attributes: map[string]string{"tags": "[\"a\"]"},
	}
}

func TestSaveAndListTrajectories(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveTrajectory(trjAt("t1", 1000), []schema.Span{span("s1", "t1", "", "llm", 0.01)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTrajectory(trjAt("t2", 2000), []schema.Span{span("s2", "t2", "", "tool", 0.02)}); err != nil {
		t.Fatal(err)
	}
	ids, err := s.ListTrajectoryIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("want 2 trajectories, got %v", ids)
	}
	// newest first: t2 was saved after t1
	if ids[0] != "t2" || ids[1] != "t1" {
		t.Fatalf("order wrong: %v", ids)
	}
}

func TestGetTrajectoryRoundTripsAllFields(t *testing.T) {
	s := newTestStore(t)
	in := trj("t1")
	spans := []schema.Span{
		span("s1", "t1", "", "llm", 0.0034),
		span("s2", "t1", "s1", "retrieval", 0),
	}
	if err := s.SaveTrajectory(in, spans); err != nil {
		t.Fatal(err)
	}
	got, gotSpans, err := s.GetTrajectory("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.TrajectoryID != "t1" || got.Source != "native" || got.Metadata["env"] != "test" {
		t.Fatalf("trajectory round-trip wrong: %+v", got)
	}
	if len(gotSpans) != 2 {
		t.Fatalf("want 2 spans, got %d", len(gotSpans))
	}
	first := gotSpans[0]
	if first.SpanID != "s1" || first.Model != "gpt-4o-2024-08-06" ||
		first.TokensPrompt != 100 || first.TokensCompletion != 20 ||
		first.CostUSD != 0.0034 || first.Attributes["tags"] != "[\"a\"]" {
		t.Fatalf("span round-trip wrong: %+v", first)
	}
	if gotSpans[1].ParentSpanID != "s1" {
		t.Fatalf("parent lost: %+v", gotSpans[1])
	}
}

func TestSaveTrajectoryAtomicRollsBackOnError(t *testing.T) {
	s := newTestStore(t)
	// valid trajectory, but a span with empty SpanID violates NOT NULL → whole tx must roll back
	err := s.SaveTrajectory(trj("t-bad"), []schema.Span{{
		Type: "span", TrajectoryID: "t-bad", Name: "x", Kind: "llm",
		StartedAtUnixMs: 1, EndedAtUnixMs: 2,
	}})
	if err == nil {
		t.Fatal("want error for span with empty span_id")
	}
	ids, _ := s.ListTrajectoryIDs()
	if len(ids) != 0 {
		t.Fatalf("rollback failed, found %v", ids)
	}
}

func TestSaveTwiceReplacesNotDuplicates(t *testing.T) {
	s := newTestStore(t)
	if err := s.SaveTrajectory(trj("t1"), []schema.Span{span("s1", "t1", "", "llm", 1)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTrajectory(trj("t1"), []schema.Span{span("s1", "t1", "", "llm", 1), span("s1b", "t1", "s1", "tool", 2)}); err != nil {
		t.Fatal(err)
	}
	ids, _ := s.ListTrajectoryIDs()
	if len(ids) != 1 {
		t.Fatalf("want 1 trajectory after re-save, got %v", ids)
	}
	_, spans, err := s.GetTrajectory("t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 2 {
		t.Fatalf("want 2 spans after re-save, got %d", len(spans))
	}
}

func TestOpenCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "created.sqlite")
	if _, err := os.Stat(path); err == nil {
		t.Fatal("db file should not exist yet")
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
}
