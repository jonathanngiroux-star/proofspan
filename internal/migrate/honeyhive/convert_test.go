package honeyhive

import (
	"os"
	"path/filepath"
	"testing"

	"proofspan/internal/schema"
)

const fixture = `{"eventId":"ev-0001","sessionId":"sess_a","eventName":"search_docs","eventType":"tool","inputs":{"query":"refund policy"},"outputs":{"result":"3 docs"},"startedAt":"2024-09-27T16:00:00.100Z","endedAt":"2024-09-27T16:00:00.450Z"}
{"eventId":"ev-0002","sessionId":"sess_a","parentId":"ev-0001","eventName":"synthesize","eventType":"model","inputs":{"q":"?"},"outputs":{"a":"..."},"startedAt":"2024-09-27T16:00:00.500Z","endedAt":"2024-09-27T16:00:02.100Z","model":"gpt-4o-2024-08-06","tokenUsage":{"promptTokens":412,"completionTokens":87},"cost":0.0034}
{"eventId":"ev-0003","sessionId":"sess_a","parentId":"ev-0001","eventName":"fetch","eventType":"retriever","inputs":{"k":3},"outputs":{"docs":["d1"]},"startedAt":"2024-09-27T16:00:02.200Z","endedAt":"2024-09-27T16:00:03.000Z","error":"timeout"}
{"eventId":"ev-0004","sessionId":"sess_b","eventName":"route","eventType":"agent","inputs":{},"outputs":{},"startedAt":"2024-09-27T16:00:03.100Z","endedAt":"2024-09-27T16:00:03.400Z"}
`

func convert(t *testing.T, lines string) *Result {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runs.jsonl")
	if err := os.WriteFile(path, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := ConvertFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestConvertGroupsBySession(t *testing.T) {
	res := convert(t, fixture)
	if len(res.Trajectories) != 2 {
		t.Fatalf("want 2 trajectories, got %d", len(res.Trajectories))
	}
	if len(res.SpansByTrajectory["sess_a"]) != 3 {
		t.Fatalf("sess_a spans: %d", len(res.SpansByTrajectory["sess_a"]))
	}
	if len(res.SpansByTrajectory["sess_b"]) != 1 {
		t.Fatalf("sess_b spans: %d", len(res.SpansByTrajectory["sess_b"]))
	}
}

func TestConvertSpanCoreFields(t *testing.T) {
	res := convert(t, fixture)
	spans := res.SpansByTrajectory["sess_a"]
	if spans[0].SpanID != "ev-0001" || spans[0].Name != "search_docs" || spans[0].Kind != "tool" {
		t.Errorf("span1 wrong: %+v", spans[0])
	}
	if spans[0].StartedAtUnixMs != 1727452800100 || spans[0].EndedAtUnixMs != 1727452800450 {
		t.Errorf("span1 times wrong: %d-%d", spans[0].StartedAtUnixMs, spans[0].EndedAtUnixMs)
	}
	if spans[0].Input != `{"query":"refund policy"}` {
		t.Errorf("input = %q", spans[0].Input)
	}
	if spans[0].ParentSpanID != "" {
		t.Errorf("root has parent %q", spans[0].ParentSpanID)
	}
}

func TestConvertModelFields(t *testing.T) {
	res := convert(t, fixture)
	spans := res.SpansByTrajectory["sess_a"]
	var llm schema.Span
	for _, s := range spans {
		if s.SpanID == "ev-0002" {
			llm = s
		}
	}
	if llm.Model != "gpt-4o-2024-08-06" {
		t.Errorf("model = %q", llm.Model)
	}
	if llm.TokensPrompt != 412 || llm.TokensCompletion != 87 {
		t.Errorf("tokens = %d/%d", llm.TokensPrompt, llm.TokensCompletion)
	}
	if llm.CostUSD != 0.0034 {
		t.Errorf("cost = %f", llm.CostUSD)
	}
	if llm.Kind != "llm" {
		t.Errorf("model eventType must map to llm, got %q", llm.Kind)
	}
}

func TestConvertErrorAndKindFallback(t *testing.T) {
	res := convert(t, fixture)
	spans := res.SpansByTrajectory["sess_a"]
	var failed schema.Span
	for _, s := range spans {
		if s.SpanID == "ev-0003" {
			failed = s
		}
	}
	if failed.Error != "timeout" {
		t.Errorf("error = %q", failed.Error)
	}
	if failed.Kind != "retrieval" {
		t.Errorf("retriever must map to retrieval, got %q", failed.Kind)
	}
	var agent schema.Span
	for _, s := range res.SpansByTrajectory["sess_b"] {
		if s.SpanID == "ev-0004" {
			agent = s
		}
	}
	if agent.Kind != "custom" {
		t.Errorf("agent eventType must map to custom, got %q", agent.Kind)
	}
}

func TestConvertRejectsBadLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.jsonl")
	if err := os.WriteFile(path, []byte("{oops}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertFile(path); err == nil {
		t.Fatal("want error on malformed line")
	}
}
