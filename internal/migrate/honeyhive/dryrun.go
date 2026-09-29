package honeyhive

import (
	"encoding/json"
	"io"

	"proofspan/internal/schema"
)

// DiffSkeleton mirrors the langsmith dry-run plan shape (stable contract).
type DiffSkeleton struct {
	DryRun        bool              `json:"dry_run"`
	Source        string            `json:"source"`
	TargetVersion string            `json:"target_version"`
	InputFile     string            `json:"input_file"`
	Trajectories  int               `json:"trajectories"`
	RunsRead      int               `json:"runs_read"`
	SpansPlanned  int               `json:"spans_planned"`
	FieldMappings []FieldMapping    `json:"field_mappings"`
	DroppedFields []DroppedField    `json:"dropped_fields"`
	SampleSpan    *schema.Span      `json:"sample_span,omitempty"`
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

// MigrateDryRun converts the export and prints the plan without writing.
func MigrateDryRun(w io.Writer, path string) error {
	res, err := ConvertFile(path)
	if err != nil {
		return err
	}
	skel := DiffSkeleton{
		DryRun:        true,
		Source:        "honeyhive",
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

func totalSpans(res *Result) int {
	n := 0
	for _, spans := range res.SpansByTrajectory {
		n += len(spans)
	}
	return n
}

// Mappings returns the HoneyHive → ATF field mapping table.
func Mappings() []FieldMapping {
	return []FieldMapping{
		{"eventId", "span_id", ""},
		{"sessionId", "trajectory_id", ""},
		{"parentId", "parent_span_id", ""},
		{"eventName", "name", ""},
		{"eventType", "kind", "model→llm|tool→tool|retriever→retrieval|else custom"},
		{"startedAt", "started_at_unix_ms", "RFC 3339 → epoch ms"},
		{"endedAt", "ended_at_unix_ms", "RFC 3339 → epoch ms"},
		{"inputs", "input", "JSON-encoded map"},
		{"outputs", "output", "JSON-encoded map"},
		{"model", "model", ""},
		{"error", "error", ""},
		{"tokenUsage.promptTokens", "tokens_prompt", ""},
		{"tokenUsage.completionTokens", "tokens_completion", ""},
		{"cost", "cost_usd", ""},
	}
}

// Dropped returns source fields with no ATF target.
func Dropped() []DroppedField {
	return []DroppedField{
		{"eventType=agent/chain nesting depth", "flattened to parent_span_id; depth not preserved in v0.1"},
	}
}
