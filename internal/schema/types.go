// Package schema defines ATF (Agent Trace Format) v0.1.1 types.
// Line-delimited JSON; one trajectory per stream, N spans per trajectory.
package schema

// ATFVersion pins the wire format.
const ATFVersion = "atf/v0.1.1"

// Trajectory is a header line in an .jsonl trace stream.
type Trajectory struct {
	Type            string            `json:"type"` // always "trajectory"
	Version         string            `json:"version"`
	TrajectoryID    string            `json:"trajectory_id"`
	Source          string            `json:"source"` // "langsmith" | "honeyhive" | "native"
	StartedAtUnixMs int64             `json:"started_at_unix_ms"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

// Span is one observed step (model call, tool call, retrieval, etc.).
type Span struct {
	Type             string            `json:"type"` // always "span"
	SpanID           string            `json:"span_id"`
	TrajectoryID     string            `json:"trajectory_id"`
	ParentSpanID     string            `json:"parent_span_id,omitempty"`
	Name             string            `json:"name"`
	Kind             string            `json:"kind"` // "llm" | "tool" | "retrieval" | "custom"
	StartedAtUnixMs  int64             `json:"started_at_unix_ms"`
	EndedAtUnixMs    int64             `json:"ended_at_unix_ms"`
	Model            string            `json:"model,omitempty"`
	Input            string            `json:"input,omitempty"`
	Output           string            `json:"output,omitempty"`
	TokensPrompt     int64             `json:"tokens_prompt,omitempty"`
	TokensCompletion int64             `json:"tokens_completion,omitempty"`
	CostUSD          float64           `json:"cost_usd,omitempty"`
	Error            string            `json:"error,omitempty"`
	Attributes       map[string]string `json:"attributes,omitempty"`
}
