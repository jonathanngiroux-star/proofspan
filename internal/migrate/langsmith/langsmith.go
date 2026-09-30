// Package langsmith converts LangSmith run exports to ATF v0.1.1.
//
// Fidelity targets (docs/fidelity/langsmith.md): >95% field parity on
// the 10k-step shared corpus, MAD assertion drift <2%, cost drift <0.5%.
// Do not add a second converter before that report exists and CI enforces it.
package langsmith

import (
	"encoding/json"
	"io"
	"os"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

// Run mirrors one JSONL line of a LangSmith run export.
// Names follow the public REST run object where practical; the fixture
// corpus (testdata/corpus/langsmith/) pins the exact contract.
type Run struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	RunType        string         `json:"run_type"`
	Inputs         map[string]any `json:"inputs"`
	Outputs        map[string]any `json:"outputs"`
	Error          string         `json:"error"`
	StartTime      string         `json:"start_time"` // RFC 3339
	EndTime        string         `json:"end_time"`
	ParentRunID    string         `json:"parent_run_id"`
	SessionID      string         `json:"session_id"`
	TraceID        string         `json:"trace_id"` // fallback when session_id absent
	Tags           []string       `json:"tags"`
	DottedOrder    string         `json:"dotted_order"`
	ExecutionOrder int            `json:"execution_order"`
	Extra          struct {
		Metadata        map[string]string `json:"metadata"`
		ModelName       string            `json:"model_name"`
		InvocationParams map[string]any    `json:"invocation_params"`
		TokenUsage *struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"token_usage"`
		TotalCost float64 `json:"total_cost"`
	} `json:"extra"`
}

// FieldMapping documents one field transformation.
type FieldMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
	Note string `json:"note,omitempty"`
}

// DroppedField documents a source field with no ATF target.
type DroppedField struct {
	Field string `json:"field"`
	Note  string `json:"note"`
}

// DiffSkeleton is the dry-run output: the conversion plan for a corpus.
// Machine-readable and stable; CI diffs it.
type DiffSkeleton struct {
	DryRun        bool           `json:"dry_run"`
	Source        string         `json:"source"`
	TargetVersion string         `json:"target_version"`
	InputFile     string         `json:"input_file"`
	Trajectories  int            `json:"trajectories"`
	RunsRead      int            `json:"runs_read"`
	SpansPlanned  int            `json:"spans_planned"`
	FieldMappings []FieldMapping `json:"field_mappings"`
	DroppedFields []DroppedField `json:"dropped_fields"`
	SampleSpan    *schema.Span   `json:"sample_span,omitempty"`
}

// MigrateDryRun reads a LangSmith run-export .jsonl and prints the
// planned conversion as a JSON diff skeleton. It writes nothing.
func MigrateDryRun(w io.Writer, path string) error {
	res, err := convertFile(path)
	if err != nil {
		return err
	}
	skel := DiffSkeleton{
		DryRun:        true,
		Source:        "langsmith",
		TargetVersion: schema.ATFVersion,
		InputFile:     path,
		Trajectories:  len(res.Trajectories),
		RunsRead:      totalSpans(res),
		SpansPlanned:  totalSpans(res),
		FieldMappings: Mappings(),
		DroppedFields: Dropped(),
	}
	skel.SampleSpan = earliestSpan(res.SpansByTrajectory)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(skel)
}

// earliestSpan returns the span with the smallest start time across all
// trajectories — a deterministic sample regardless of map iteration order.
func earliestSpan(byTrajectory map[string][]schema.Span) *schema.Span {
	var best *schema.Span
	for _, spans := range byTrajectory {
		for i := range spans {
			if best == nil || spans[i].StartedAtUnixMs < best.StartedAtUnixMs {
				s := spans[i]
				best = &s
			}
		}
	}
	return best
}

// ConvertFile is the file-reading entry point for the CLI.
func ConvertFile(path string) (*Result, error) {
	return convertFile(path)
}

func convertFile(path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Convert(f)
}

func totalSpans(res *Result) int {
	n := 0
	for _, spans := range res.SpansByTrajectory {
		n += len(spans)
	}
	return n
}

// Mappings returns the full LangSmith → ATF field mapping table.
func Mappings() []FieldMapping {
	return []FieldMapping{
		{"id", "span_id", ""},
		{"name", "name", ""},
		{"run_type", "kind", "llm|tool|retriever→retrieval|custom"},
		{"session_id", "trajectory_id", "trace_id fallback"},
		{"parent_run_id", "parent_span_id", ""},
		{"start_time", "started_at_unix_ms", "RFC 3339 → epoch ms"},
		{"end_time", "ended_at_unix_ms", "RFC 3339 → epoch ms"},
		{"inputs", "input", "JSON-encoded map"},
		{"outputs", "output", "JSON-encoded map"},
		{"error", "error", ""},
		{"tags", "attributes.tags", "JSON array"},
		{"dotted_order", "attributes.dotted_order", "replay ordering preserved"},
		{"execution_order", "attributes.execution_order", ""},
		{"extra.model_name", "model", "extra.invocation_params.model_name fallback"},
		{"extra.token_usage.prompt_tokens", "tokens_prompt", ""},
		{"extra.token_usage.completion_tokens", "tokens_completion", ""},
		{"extra.total_cost", "cost_usd", ""},
		{"extra.metadata", "trajectory.metadata", "first run of a session carries the header"},
	}
}

// Dropped returns source fields with no ATF target.
func Dropped() []DroppedField {
	return []DroppedField{
		{"extra.invocation_params (non-model keys)", "out of scope v0.1: prompt params are not span semantics"},
	}
}
