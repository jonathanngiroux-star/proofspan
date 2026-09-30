package langsmith

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

const fullFixture = `{"id":"run-0001","name":"search_docs","run_type":"tool","inputs":{"query":"refund policy"},"outputs":{"result":"3 docs returned"},"start_time":"2024-09-27T16:00:00.100Z","end_time":"2024-09-27T16:00:00.450Z","session_id":"trj_a","tags":["prod","web"],"dotted_order":"20240927T160000.100Zrun-0001","execution_order":1,"extra":{"metadata":{"user":"u_42","env":"prod"}}}
{"id":"run-0002","name":"synthesize","run_type":"llm","parent_run_id":"run-0001","inputs":{"question":"What is the refund policy?"},"outputs":{"answer":"Refunds allowed within 30 days"},"start_time":"2024-09-27T16:00:00.500Z","end_time":"2024-09-27T16:00:02.100Z","session_id":"trj_a","tags":["prod"],"dotted_order":"20240927T160000.500Zrun-0002","execution_order":2,"error":"","extra":{"invocation_params":{"model_name":"gpt-4o-2024-08-06"},"token_usage":{"prompt_tokens":412,"completion_tokens":87},"total_cost":0.0034}}
{"id":"run-0003","name":"failed_call","run_type":"llm","parent_run_id":"run-0001","inputs":{"q":"x"},"outputs":{},"start_time":"2024-09-27T16:00:02.200Z","end_time":"2024-09-27T16:00:03.000Z","session_id":"trj_a","error":"rate limited","extra":{"invocation_params":{"model_name":"gpt-4o-mini"}}}
{"id":"run-0004","name":"fetch_docs","run_type":"retriever","inputs":{"k":3},"outputs":{"docs":["d1"]},"start_time":"2024-09-27T16:00:03.100Z","end_time":"2024-09-27T16:00:03.400Z","session_id":"trj_b","tags":["eval"]}
`

func convertFixture(t *testing.T) *Result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(fullFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	res, err := Convert(f)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestConvertGroupsBySession(t *testing.T) {
	res := convertFixture(t)
	if len(res.Trajectories) != 2 {
		t.Fatalf("want 2 trajectories, got %d", len(res.Trajectories))
	}
	if len(res.SpansByTrajectory["trj_a"]) != 3 {
		t.Fatalf("trj_a spans: %d", len(res.SpansByTrajectory["trj_a"]))
	}
	if len(res.SpansByTrajectory["trj_b"]) != 1 {
		t.Fatalf("trj_b spans: %d", len(res.SpansByTrajectory["trj_b"]))
	}
}

func TestConvertTrajectoryHeader(t *testing.T) {
	res := convertFixture(t)
	var hdr schema.Trajectory
	for _, tr := range res.Trajectories {
		if tr.TrajectoryID == "trj_a" {
			hdr = tr
		}
	}
	if hdr.TrajectoryID != "trj_a" {
		t.Fatal("trj_a missing")
	}
	if hdr.Source != "langsmith" {
		t.Errorf("source = %q", hdr.Source)
	}
	if hdr.Version != schema.ATFVersion {
		t.Errorf("version = %q", hdr.Version)
	}
	if hdr.StartedAtUnixMs != 1727452800100 {
		t.Errorf("trajectory started_at = %d", hdr.StartedAtUnixMs)
	}
	if hdr.Metadata["user"] != "u_42" || hdr.Metadata["env"] != "prod" {
		t.Errorf("metadata = %v", hdr.Metadata)
	}
}

func TestConvertSpanCoreFields(t *testing.T) {
	res := convertFixture(t)
	spans := res.SpansByTrajectory["trj_a"]
	if spans[0].SpanID != "run-0001" || spans[0].Name != "search_docs" || spans[0].Kind != "tool" {
		t.Errorf("span1 wrong: %+v", spans[0])
	}
	if spans[0].StartedAtUnixMs != 1727452800100 || spans[0].EndedAtUnixMs != 1727452800450 {
		t.Errorf("span1 times wrong: %+v", spans[0])
	}
	if spans[0].Input != `{"query":"refund policy"}` {
		t.Errorf("span1 input = %q", spans[0].Input)
	}
	if spans[0].ParentSpanID != "" {
		t.Errorf("root span has parent %q", spans[0].ParentSpanID)
	}
}

func TestConvertLLMFields(t *testing.T) {
	res := convertFixture(t)
	spans := res.SpansByTrajectory["trj_a"]
	var llm schema.Span
	for _, s := range spans {
		if s.SpanID == "run-0002" {
			llm = s
		}
	}
	if llm.Kind != "llm" || llm.Model != "gpt-4o-2024-08-06" {
		t.Errorf("llm span wrong: %+v", llm)
	}
	if llm.TokensPrompt != 412 || llm.TokensCompletion != 87 {
		t.Errorf("tokens wrong: %d/%d", llm.TokensPrompt, llm.TokensCompletion)
	}
	if llm.CostUSD != 0.0034 {
		t.Errorf("cost = %f", llm.CostUSD)
	}
	if llm.ParentSpanID != "run-0001" {
		t.Errorf("parent = %q", llm.ParentSpanID)
	}
}

func TestConvertAttributesCarryTagsDottedExecution(t *testing.T) {
	res := convertFixture(t)
	spans := res.SpansByTrajectory["trj_a"]
	if spans[0].Attributes["tags"] != `["prod","web"]` {
		t.Errorf("tags attr = %q", spans[0].Attributes["tags"])
	}
	if spans[0].Attributes["dotted_order"] != "20240927T160000.100Zrun-0001" {
		t.Errorf("dotted_order attr = %q", spans[0].Attributes["dotted_order"])
	}
	if spans[0].Attributes["execution_order"] != "1" {
		t.Errorf("execution_order attr = %q", spans[0].Attributes["execution_order"])
	}
}

func TestConvertErrorAndKindFallback(t *testing.T) {
	res := convertFixture(t)
	spans := res.SpansByTrajectory["trj_a"]
	var failed schema.Span
	for _, s := range spans {
		if s.SpanID == "run-0003" {
			failed = s
		}
	}
	if failed.Error != "rate limited" {
		t.Errorf("error = %q", failed.Error)
	}
	if failed.Kind != "llm" {
		t.Errorf("kind = %q", failed.Kind)
	}
	b := res.SpansByTrajectory["trj_b"][0]
	if b.Kind != "retrieval" {
		t.Errorf("retriever should map to retrieval, got %q", b.Kind)
	}
}

func TestConvertModelFromTopLevelFallback(t *testing.T) {
	// some exports put model at extra.model_name instead of invocation_params
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	line := `{"id":"r1","name":"m","run_type":"llm","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","session_id":"s1","extra":{"model_name":"claude-3-5"}}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	res, err := Convert(f)
	if err != nil {
		t.Fatal(err)
	}
	if res.SpansByTrajectory["s1"][0].Model != "claude-3-5" {
		t.Errorf("model fallback failed: %+v", res.SpansByTrajectory["s1"][0])
	}
}

func TestConvertEmptySessionFallsBackToTraceID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	line := `{"id":"r1","name":"m","run_type":"tool","inputs":{},"outputs":{},"start_time":"2024-09-27T16:00:00.000Z","end_time":"2024-09-27T16:00:01.000Z","trace_id":"tr_9"}`
	if err := os.WriteFile(path, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	res, err := Convert(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.SpansByTrajectory["tr_9"]) != 1 {
		t.Errorf("trace_id fallback failed: %+v", res.SpansByTrajectory)
	}
}

func TestConvertRejectsUnparseableLine(t *testing.T) {
	f, err := os.OpenFile(filepath.Join(t.TempDir(), "x.jsonl"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{bad}\n")
	f.Seek(0, 0)
	if _, err := Convert(f); err == nil {
		t.Fatal("want error on malformed line")
	}
}
