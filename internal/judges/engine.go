package judges

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"proofspan/internal/schema"
)

// Verdict is one judge outcome over one trajectory.
type Verdict struct {
	JudgeID          string          `json:"judge_id"`
	JudgeFingerprint string          `json:"judge_fingerprint"`
	TrajectoryID     string          `json:"trajectory_id"`
	Status           string          `json:"status"` // pass|fail|error
	Score            float64         `json:"score,omitempty"`
	Detail           string          `json:"detail,omitempty"`
	Raw              json.RawMessage `json:"raw,omitempty"`
}

// Engine executes pinned judges over trajectories.
//
// Provider resolution by fingerprint prefix:
//   builtin:<name>@<v>   deterministic reference implementation; offline, CI-safe
//   <model>@sha256:<h>   OpenAI-compatible HTTP judge; NVIDIA_API_KEY env-keyed
//
// A judge whose fingerprint contains UNPINNED refuses execution — a placeholder
// pin is a hard error, never a silent grade (uncontrolled LLM-as-judge drift
// is a Series A killer).
type Engine struct {
	manifest *Manifest
	http     *http.Client
	apiKey   string
}

// NewEngine wires an engine over a validated manifest.
func NewEngine(m *Manifest) *Engine {
	return &Engine{
		manifest: m,
		// reasoning models can spend minutes on long trajectory prompts
		http: &http.Client{Timeout: 5 * time.Minute},
	}
}

// WithHTTPClient overrides the HTTP client (tests, proxies).
func (e *Engine) WithHTTPClient(c *http.Client) *Engine {
	e.http = c
	return e
}

// WithAPIKey sets the bearer token for HTTP judges. Empty key → HTTP
// judges return an error telling the operator which env var to set.
func (e *Engine) WithAPIKey(key string) *Engine {
	e.apiKey = key
	return e
}

// Judge executes the named judge over the trajectory, resolving the
// provider from the pinned fingerprint.
func (e *Engine) Judge(ctx context.Context, judgeID, trajectoryID string, spans []schema.Span) (*Verdict, error) {
	j, ok := e.manifest.Judge(judgeID)
	if !ok {
		return nil, fmt.Errorf("unknown judge %q", judgeID)
	}
	if strings.Contains(j.ModelFingerprint, "UNPINNED") {
		return nil, fmt.Errorf("judge %q has an UNPINNED fingerprint (%s): pin the real model hash in the manifest before execution", judgeID, j.ModelFingerprint)
	}
	if strings.HasPrefix(j.ModelFingerprint, "builtin:") {
		return e.builtinJudge(ctx, j, trajectoryID, spans)
	}
	// HTTP judges need an endpoint; Judge() uses the default endpoint.
	return e.JudgeHTTP(ctx, judgeID, trajectoryID, spans, defaultEndpoint())
}

func defaultEndpoint() string {
	if v := os.Getenv("PROOFSPAN_JUDGE_ENDPOINT"); v != "" {
		return v
	}
	return "https://integrate.api.nvidia.com/v1"
}

// JudgeHTTP executes an OpenAI-compatible HTTP judge at base endpoint.
// The provider's reported model must match the pinned fingerprint's
// model segment exactly, or the call is refused before any grading.
func (e *Engine) JudgeHTTP(ctx context.Context, judgeID, trajectoryID string, spans []schema.Span, endpoint string) (*Verdict, error) {
	j, ok := e.manifest.Judge(judgeID)
	if !ok {
		return nil, fmt.Errorf("unknown judge %q", judgeID)
	}
	if strings.Contains(j.ModelFingerprint, "UNPINNED") {
		return nil, fmt.Errorf("judge %q has an UNPINNED fingerprint: pin the real model hash before execution", judgeID)
	}
	pinnedModel := j.ModelFingerprint
	if i := strings.Index(pinnedModel, "@sha256:"); i > 0 {
		pinnedModel = pinnedModel[:i]
	}
	if e.apiKey == "" {
		return nil, fmt.Errorf("HTTP judge %q needs NVIDIA_API_KEY (or PROOFSPAN_JUDGE_API_KEY) set", judgeID)
	}
	// resolve the served model and compare against the pin
	served, err := e.servedModel(ctx, endpoint, pinnedModel)
	if err != nil {
		return nil, fmt.Errorf("resolve model at %s: %w", endpoint, err)
	}
	if served != pinnedModel {
		return nil, fmt.Errorf("fingerprint mismatch: judge %q pinned %s but endpoint serves %s — refusing to grade", judgeID, pinnedModel, served)
	}
	prompt := buildJudgePrompt(j, spans)
	body, err := json.Marshal(map[string]any{
		"model":    pinnedModel,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
		"temperature": 0,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(endpoint, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("judge call: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("judge endpoint returned %d: %s", resp.StatusCode, string(b))
	}
	var completion struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return nil, fmt.Errorf("decode completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("judge endpoint returned no choices")
	}
	content := strings.TrimSpace(completion.Choices[0].Message.Content)
	verdict := &Verdict{
		JudgeID:          judgeID,
		JudgeFingerprint: j.ModelFingerprint,
		TrajectoryID:     trajectoryID,
		Raw:              json.RawMessage(fmt.Sprintf(`{"content":%q}`, content)),
	}
	switch {
	case strings.HasPrefix(strings.ToUpper(content), "PASS"):
		verdict.Status = "pass"
	case strings.HasPrefix(strings.ToUpper(content), "FAIL"):
		verdict.Status = "fail"
	default:
		verdict.Status = "error"
		verdict.Detail = fmt.Sprintf("judge returned unparseable verdict: %q", truncate(content, 200))
	}
	if i := strings.LastIndex(content, ":"); i > 0 && verdict.Status != "error" {
		verdict.Detail = strings.TrimSpace(content[i+1:])
	}
	return verdict, nil
}

// servedModel fetches GET {endpoint}/models/{model} and returns its id.
func (e *Engine) servedModel(ctx context.Context, endpoint, model string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/")+"/models/"+model, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+e.apiKey)
	resp, err := e.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("models endpoint %d: %s", resp.StatusCode, string(b))
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// builtinJudge runs the deterministic reference implementation.
// factual-consistency: every llm span's output claim must be grounded in
// some retrieval span's output (number overlap as a minimal proxy).
func (e *Engine) builtinJudge(ctx context.Context, j Judge, trajectoryID string, spans []schema.Span) (*Verdict, error) {
	v := &Verdict{JudgeID: j.ID, JudgeFingerprint: j.ModelFingerprint, TrajectoryID: trajectoryID}
	switch j.ID {
	case "factual-consistency":
		nums := map[string]bool{}
		for _, s := range spans {
			if s.Kind == "retrieval" {
				for _, n := range extractNumbers(s.Output) {
					nums[n] = true
				}
			}
		}
		for _, s := range spans {
			if s.Kind != "llm" {
				continue
			}
			for _, n := range extractNumbers(s.Output) {
				if !nums[n] {
					v.Status = "fail"
					v.Detail = fmt.Sprintf("llm span %s cites number %q not present in any retrieval output", s.SpanID, n)
					return v, nil
				}
			}
		}
		v.Status = "pass"
		v.Score = 1.0
		return v, nil
	default:
		return nil, fmt.Errorf("no builtin implementation for judge %q (fingerprint %s); HTTP judges need a real model pin", j.ID, j.ModelFingerprint)
	}
}

func extractNumbers(s string) []string {
	var out []string
	cur := strings.Builder{}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			cur.WriteRune(r)
			continue
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func buildJudgePrompt(j Judge, spans []schema.Span) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are an evaluation judge: %s\n", j.Description)
	b.WriteString("Grade the following agent trajectory. Reply with exactly PASS or FAIL, then ': ' and a one-line reason.\n\n")
	// cap the trajectory rendering: first 8 spans, 300 chars per field —
	// long prompts make reasoning models burn the timeout
	shown := spans
	if len(shown) > 8 {
		b.WriteString(fmt.Sprintf("(showing first 8 of %d spans)\n", len(shown)))
		shown = shown[:8]
	}
	for _, s := range shown {
		fmt.Fprintf(&b, "[%s] %s (kind=%s)\n  input: %s\n  output: %s\n", s.SpanID, s.Name, s.Kind, truncate(s.Input, 300), truncate(s.Output, 300))
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// Judge looks up a judge by ID.
func (m *Manifest) Judge(id string) (Judge, bool) {
	for _, j := range m.Judges {
		if j.ID == id {
			return j, true
		}
	}
	return Judge{}, false
}
