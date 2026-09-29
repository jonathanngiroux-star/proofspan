package langsmith

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const fixture = `{"id":"run-0001","name":"search_docs","run_type":"tool","inputs":{"query":"refund policy"},"outputs":{"result":"3 docs returned"},"start_time":"2024-09-27T16:00:00.100Z","end_time":"2024-09-27T16:00:00.450Z","session_id":"trj_01","extra":{"metadata":{"user":"u_42"}}}
{"id":"run-0002","name":"synthesize","run_type":"llm","parent_run_id":"run-0001","inputs":{"q":"?"},"outputs":{"a":"..."},"start_time":"2024-09-27T16:00:00.500Z","end_time":"2024-09-27T16:00:02.100Z","session_id":"trj_01","extra":{"model_name":"gpt-4o-2024-08-06","token_usage":{"prompt_tokens":412,"completion_tokens":87},"total_cost":0.0034}}
{"id":"run-0003","name":"fetch","run_type":"retriever","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:02.150Z","end_time":"2024-09-27T16:00:02.280Z","session_id":"trj_02"}
`

func TestMigrateDryRunSkeleton(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := MigrateDryRun(&buf, path); err != nil {
		t.Fatal(err)
	}
	var skel DiffSkeleton
	if err := json.Unmarshal(buf.Bytes(), &skel); err != nil {
		t.Fatalf("output not valid JSON: %v", err)
	}
	if skel.DryRun != true || skel.Source != "langsmith" {
		t.Errorf("dry_run/source wrong: %+v", skel)
	}
	if skel.RunsRead != 3 || skel.SpansPlanned != 3 {
		t.Errorf("counts wrong: runs=%d spans=%d", skel.RunsRead, skel.SpansPlanned)
	}
	if skel.Trajectories != 2 {
		t.Errorf("trajectories=%d, want 2 (two session_ids)", skel.Trajectories)
	}
	if skel.SampleSpan == nil || skel.SampleSpan.SpanID != "run-0001" {
		t.Errorf("sample span wrong: %+v", skel.SampleSpan)
	}
	if skel.SampleSpan.Kind != "tool" {
		t.Errorf("kind mapping wrong: %q", skel.SampleSpan.Kind)
	}
	if len(skel.FieldMappings) < 10 {
		t.Errorf("field_mappings too thin: %d", len(skel.FieldMappings))
	}
}

// TestMigrateDryRunDeterministicSample pins the contract that the sample
// span is the earliest-starting span, regardless of Go's randomized map
// iteration order. (Caught by CI in a container after passing locally.)
func TestMigrateDryRunDeterministicSample(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		var buf bytes.Buffer
		if err := MigrateDryRun(&buf, path); err != nil {
			t.Fatal(err)
		}
		var skel DiffSkeleton
		if err := json.Unmarshal(buf.Bytes(), &skel); err != nil {
			t.Fatal(err)
		}
		if skel.SampleSpan == nil || skel.SampleSpan.SpanID != "run-0001" {
			t.Fatalf("iteration %d: sample span must be the earliest (run-0001), got %+v", i, skel.SampleSpan)
		}
	}
}

func TestMigrateDryRunBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte("{not json}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := MigrateDryRun(&buf, path); err == nil {
		t.Fatal("want error on malformed line, got nil")
	}
}
