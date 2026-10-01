package judges

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
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
// Providers are declared in the manifest (OpenAI-compatible endpoint +
// the env var holding the API key) and referenced by judges by name.
// Resolution order per HTTP judge call:
//  1. WithEndpoint / WithAPIKey overrides (CLI flags) win outright
//  2. else the judge's provider: endpoint from the manifest; key from the
//     provider's api_key_env environment variable (no-auth endpoints
//     declare no api_key_env — local vLLM/ollama)
//
// A judge whose fingerprint contains UNPINNED refuses execution — a
// placeholder pin is a hard error, never a silent grade.
type Engine struct {
	manifest *Manifest
	http     *http.Client
	apiKey   string // explicit override; beats provider env vars
	endpoint string // explicit override; beats provider endpoints
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

// WithAPIKey sets an explicit key override for HTTP judges, beating any
// provider api_key_env. Only used for CLI flag injection.
func (e *Engine) WithAPIKey(key string) *Engine {
	e.apiKey = key
	return e
}

// WithEndpoint sets an explicit endpoint override, beating provider
// endpoints. Only used for CLI flag injection.
func (e *Engine) WithEndpoint(endpoint string) *Engine {
	e.endpoint = endpoint
	return e
}

// resolve returns the endpoint and bearer key for an HTTP judge.
// Precedence: explicit overrides > provider declaration > error naming
// exactly which env var to set.
func (e *Engine) resolve(j Judge) (endpoint, apiKey string, err error) {
	endpoint = e.endpoint
	if endpoint == "" {
		p, ok := e.manifest.Providers[j.Provider]
		if !ok {
			// Load() should have caught this; belt and suspenders.
			return "", "", fmt.Errorf("judge %q references unknown provider %q", j.ID, j.Provider)
		}
		endpoint = p.Endpoint
	}
	apiKey = e.apiKey
	if apiKey == "" {
		p := e.manifest.Providers[j.Provider]
		if p.APIKeyEnv != "" {
			apiKey = os.Getenv(p.APIKeyEnv)
			if apiKey == "" {
				return "", "", fmt.Errorf("judge %q: set %s to your API key for %s (or pass --judge-api-key)", j.ID, p.APIKeyEnv, j.Provider)
			}
		}
		// empty APIKeyEnv: no-auth endpoint (local vLLM/ollama) — key stays ""
	}
	if apiKey != "" {
		// Keys are stored raw and get one "Bearer " prefix at the call
		// sites. Provider docs commonly show the key WITH its prefix
		// ("Bearer nvapi-...") and users paste it into the env var —
		// strip it once here so requests never send "Bearer Bearer ...".
		if strings.HasPrefix(strings.ToLower(apiKey), "bearer ") {
			apiKey = apiKey[len("Bearer "):]
		}
	}
	return endpoint, apiKey, nil
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
	// HTTP judge: resolve endpoint+key from overrides or the judge's provider.
	endpoint, apiKey, err := e.resolve(j)
	if err != nil {
		return nil, err
	}
	return e.judgeCall(ctx, j, trajectoryID, spans, endpoint, apiKey)
}

// judgeCall verifies the pinned fingerprint against the endpoint and
// grades. The served model card (GET /models/<model>) must match the pin
// on BOTH segments: the card's id equals the pinned model id, and the
// sha256 of the exact card body equals the pinned @sha256: hash. A
// provider that silently re-serves a changed card under the same id is
// refused — the hash is the pin, not the name.
func (e *Engine) judgeCall(ctx context.Context, j Judge, trajectoryID string, spans []schema.Span, endpoint, apiKey string) (*Verdict, error) {
	judgeID := j.ID
	model, pinnedHash, ok := strings.Cut(j.ModelFingerprint, "@sha256:")
	if !ok || model == "" || pinnedHash == "" {
		return nil, fmt.Errorf("judge %q: model_fingerprint %q must be <model>@sha256:<hash of the /models/<model> response body> — a model name alone is not a pin (docs/judges.md rule 1)", judgeID, j.ModelFingerprint)
	}
	// resolve the served model card and compare against the pin
	servedID, servedHash, err := e.servedModel(ctx, endpoint, model, apiKey)
	if err != nil {
		return nil, fmt.Errorf("resolve model at %s: %w", endpoint, err)
	}
	if servedID != model {
		return nil, fmt.Errorf("fingerprint mismatch: judge %q pinned %s but endpoint serves %s — refusing to grade", judgeID, model, servedID)
	}
	if servedHash != pinnedHash {
		return nil, fmt.Errorf("fingerprint mismatch: judge %q pinned %s@sha256:%s but the endpoint's model card hashes to sha256:%s (the card body changed — update the pin deliberately or refuse) — refusing to grade", judgeID, model, pinnedHash, servedHash)
	}
	prompt := buildJudgePrompt(j, spans)
	body, err := json.Marshal(map[string]any{
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": prompt}},
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
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
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

// servedModel fetches GET {endpoint}/models/{model} and returns the card's
// id plus the sha256 hex of the EXACT response body (the fingerprint's
// @sha256: segment — docs/judges.md rule 1). apiKey may be empty
// (no-auth local endpoints).
func (e *Engine) servedModel(ctx context.Context, endpoint, model, apiKey string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(endpoint, "/")+"/models/"+model, nil)
	if err != nil {
		return "", "", err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", "", fmt.Errorf("models endpoint %d: %s", resp.StatusCode, string(b))
	}
	card, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(card, &out); err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(card)
	return out.ID, hex.EncodeToString(sum[:]), nil
}

// builtinJudge runs the deterministic reference implementation.
// factual-consistency: every llm span's output claim must be grounded in
// some retrieval span's output (number overlap as a minimal proxy).
func (e *Engine) builtinJudge(ctx context.Context, j Judge, trajectoryID string, spans []schema.Span) (*Verdict, error) {
	v := &Verdict{JudgeID: j.ID, JudgeFingerprint: j.ModelFingerprint, TrajectoryID: trajectoryID}
	switch j.ID {
	case "factual-consistency":
		// Grounding rule: every fact token cited by an llm span must appear
		// in some retrieval span's output. Facts are fact_<traj>_<num> tokens;
		// free numbers are NOT checked (span counters, token counts, etc.
		// would false-positive — caught by testing against the corpus).
		nums := map[string]bool{}
		for _, s := range spans {
			if s.Kind == "retrieval" {
				for _, n := range extractFacts(s.Output) {
					nums[n] = true
				}
			}
		}
		for _, s := range spans {
			if s.Kind != "llm" {
				continue
			}
			for _, n := range extractFacts(s.Output) {
				if !nums[n] {
					v.Status = "fail"
					v.Detail = fmt.Sprintf("llm span %s cites fact %q not present in any retrieval output", s.SpanID, n)
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

// extractFacts pulls fact_<traj>_<num> tokens from a payload. The corpus
// (and design partners) tag claims this way; free numbers are ignored —
// span counters and token counts would otherwise false-positive.
func extractFacts(s string) []string {
	return factPattern.FindAllString(s, -1)
}

var factPattern = regexp.MustCompile(`fact_\d+_\d+`)

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
