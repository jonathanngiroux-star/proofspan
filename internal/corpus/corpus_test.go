package corpus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The corpus report is what a design partner gets on THEIR export before
// they trust a migration: parse rate, known-field coverage, unknown-key
// census (the honest "we don't map this yet" list), and timing.

func write(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "export.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReportCountsSpansAndTrajectories(t *testing.T) {
	path := write(t, strings.Join([]string{
		`{"id":"r1","name":"a","run_type":"llm","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","session_id":"s1","extra":{"total_cost":0.01}}`,
		`{"id":"r2","name":"b","run_type":"tool","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:01.000Z","end_time":"2024-09-27T16:00:02.000Z","session_id":"s1","parent_run_id":"r1"}`,
		`{"id":"r3","name":"c","run_type":"tool","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:02.000Z","end_time":"2024-09-27T16:00:03.000Z","session_id":"s2"}`,
		"",
	}, "\n"))
	rep, err := Analyze(path, "langsmith", KnownKeys("langsmith"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.LinesTotal != 3 || rep.LinesParsed != 3 {
		t.Errorf("lines: total=%d parsed=%d", rep.LinesTotal, rep.LinesParsed)
	}
	if rep.Trajectories != 2 {
		t.Errorf("trajectories = %d", rep.Trajectories)
	}
	if rep.ParseErrors != 0 {
		t.Errorf("parse errors = %d", rep.ParseErrors)
	}
	if rep.SpansByKind["llm"] != 1 || rep.SpansByKind["tool"] != 2 {
		t.Errorf("kinds = %v", rep.SpansByKind)
	}
}

func TestReportFlagsParseErrors(t *testing.T) {
	path := write(t, "{not json}\n{\"id\":\"r1\"}\n")
	rep, err := Analyze(path, "langsmith", KnownKeys("langsmith"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.ParseErrors != 2 {
		t.Errorf("parse errors = %d, want 2 (malformed + missing timestamps)", rep.ParseErrors)
	}
}

func TestReportUnknownKeysCensus(t *testing.T) {
	path := write(t, `{"id":"r1","name":"a","run_type":"llm","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","session_id":"s1","deep_nested":{"custom":{"field":true},"another":"x"}}`)
	rep, err := Analyze(path, "langsmith", KnownKeys("langsmith"))
	if err != nil {
		t.Fatal(err)
	}
	if rep.UnknownKeys["deep_nested.custom.field"] != 1 {
		t.Errorf("nested unknown key must be counted dotted: %v", rep.UnknownKeys)
	}
	if rep.UnknownKeys["deep_nested.another"] != 1 {
		t.Errorf("second nested key: %v", rep.UnknownKeys)
	}
}

func TestReportKnownKeyCoverage(t *testing.T) {
	path := write(t, `{"id":"r1","name":"a","run_type":"llm","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","session_id":"s1","extra":{"total_cost":0.01,"token_usage":{"prompt_tokens":5,"completion_tokens":2}}}`)
	rep, err := Analyze(path, "langsmith", KnownKeys("langsmith"))
	if err != nil {
		t.Fatal(err)
	}
	// token_usage is mapped wholesale: the map itself counts as covered,
	// and its inner keys are not descended into (they are preserved opaque)
	if rep.FieldCoverage["extra.token_usage"] != 1 {
		t.Errorf("wholesale-mapped key coverage: %v", rep.FieldCoverage)
	}
	if rep.FieldCoverage["tags"] != 0 {
		t.Errorf("absent key must show 0 coverage: %v", rep.FieldCoverage["tags"])
	}
}

func TestReportMarkdownRenders(t *testing.T) {
	path := write(t, `{"id":"r1","name":"a","run_type":"llm","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","session_id":"s1"}`)
	rep, err := Analyze(path, "langsmith", KnownKeys("langsmith"))
	if err != nil {
		t.Fatal(err)
	}
	md := rep.Markdown("langsmith", path)
	for _, want := range []string{"# Corpus report", "langsmith", "Parsed lines", "Unknown keys"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
}
