package judges

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"proofspan/internal/schema"
)

// The engine executes a pinned judge over a trajectory. Provider resolution:
//   builtin:<judge-id>   deterministic reference implementation (offline, CI)
//   openai-compat:<endpoint-host>  HTTP chat-completions call, env-keyed
// The judge's model_fingerprint is resolved against the provider's reported
// model before the call: mismatch = hard error, never a silent grade.

const testManifest = `{
	"namespace": "proofspan/llm-judge-registry",
	"version": "v1.1.0",
	"judges": [
		{"id": "factual-consistency", "model_fingerprint": "builtin:factual-consistency@v1", "description": "deterministic reference judge"},
		{"id": "conciseness", "model_fingerprint": "nvidia/llama-3.1-8b-instruct@sha256:abc123", "description": "HTTP judge"}
	]
}`

func testJudgeSetup(t *testing.T) (*Manifest, []schema.Span) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(testManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	spans := []schema.Span{
		{Type: "span", SpanID: "s1", TrajectoryID: "t1", Kind: "retrieval", Name: "fetch_docs",
			Input: `{"query":"refund policy"}`, Output: `{"docs":"30-day refunds"}`,
			StartedAtUnixMs: 100, EndedAtUnixMs: 200},
		{Type: "span", SpanID: "s2", TrajectoryID: "t1", ParentSpanID: "s1", Kind: "llm", Name: "answer",
			Input: `{"question":"What is the refund policy?"}`, Output: `{"answer":"Refunds within 30 days"}`,
			StartedAtUnixMs: 210, EndedAtUnixMs: 400},
	}
	return m, spans
}

func TestEngineExecutesBuiltinJudge(t *testing.T) {
	m, spans := testJudgeSetup(t)
	eng := NewEngine(m)
	res, err := eng.Judge(context.Background(), "factual-consistency", "t1", spans)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "pass" {
		t.Errorf("consistent answer must pass, got %s: %s", res.Status, res.Detail)
	}
	if res.JudgeID != "factual-consistency" || res.JudgeFingerprint != "builtin:factual-consistency@v1" {
		t.Errorf("result must carry judge identity: %+v", res)
	}
}

func TestEngineBuiltinFailsOnInconsistency(t *testing.T) {
	m, _ := testJudgeSetup(t)
	spans := []schema.Span{
		{SpanID: "s1", TrajectoryID: "t1", Kind: "retrieval", Output: `{"docs":"90-day refunds"}`},
		{SpanID: "s2", TrajectoryID: "t1", Kind: "llm", Output: `{"answer":"Refunds within 30 days"}`},
	}
	eng := NewEngine(m)
	res, err := eng.Judge(context.Background(), "factual-consistency", "t1", spans)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "fail" {
		t.Errorf("docs say 90-day, answer says 30-day → must fail, got %s", res.Status)
	}
}

func TestEngineRejectsUnknownJudge(t *testing.T) {
	m, spans := testJudgeSetup(t)
	eng := NewEngine(m)
	if _, err := eng.Judge(context.Background(), "no-such-judge", "t1", spans); err == nil {
		t.Fatal("unknown judge must error")
	}
}

func TestEngineHTTPJudgeVerifiesFingerprint(t *testing.T) {
	m, spans := testJudgeSetup(t)
	// fake server that reports a DIFFERENT model than the manifest pins
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/the-model" {
			w.Write([]byte(`{"id":"some-other-model"}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"PASS"}}]}`))
	}))
	defer srv.Close()
	eng := NewEngine(m).WithHTTPClient(srv.Client()).WithAPIKey("test-key")
	res, err := eng.JudgeHTTP(context.Background(), "conciseness", "t1", spans, srv.URL+"/v1")
	if err == nil {
		t.Fatalf("expected fingerprint mismatch error, got result %+v", res)
	}
	if !strings.Contains(err.Error(), "fingerprint") {
		t.Errorf("error must name the fingerprint mismatch: %v", err)
	}
}

func TestEngineHTTPJudgePassesOnMatch(t *testing.T) {
	m, spans := testJudgeSetup(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models/nvidia/llama-3.1-8b-instruct" {
			w.Write([]byte(`{"id":"nvidia/llama-3.1-8b-instruct"}`))
			return
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"FAIL: answer rambles"}}]}`))
	}))
	defer srv.Close()
	eng := NewEngine(m).WithHTTPClient(srv.Client()).WithAPIKey("test-key")
	res, err := eng.JudgeHTTP(context.Background(), "conciseness", "t1", spans, srv.URL+"/v1")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "fail" {
		t.Errorf("judge said FAIL → must be fail, got %s (%s)", res.Status, res.Detail)
	}
	if res.JudgeFingerprint != "nvidia/llama-3.1-8b-instruct@sha256:abc123" {
		t.Errorf("fingerprint must round-trip: %s", res.JudgeFingerprint)
	}
}

func TestEngineUnpinnedJudgeRefusesLiveCall(t *testing.T) {
	// UNPINNED fingerprints (v0.1 placeholder) must refuse live execution
	man := `{"namespace":"n","version":"v1.1.0","judges":[{"id":"u","model_fingerprint":"x@sha256:UNPINNED-v0.1-NO-LIVE-JUDGE-EXECUTION"}]}`
	path := filepath.Join(t.TempDir(), "m.json")
	if err := os.WriteFile(path, []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(m)
	if _, err := eng.Judge(context.Background(), "u", "t1", nil); err == nil {
		t.Fatal("UNPINNED judge must refuse execution")
	}
}
