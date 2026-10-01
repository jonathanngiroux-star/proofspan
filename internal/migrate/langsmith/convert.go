package langsmith

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/jonathanngiroux-star/proofspan/internal/schema"
)

// Result is a converted corpus: trajectories with their spans.
type Result struct {
	Trajectories      []schema.Trajectory
	SpansByTrajectory map[string][]schema.Span
}

// Convert reads a LangSmith run-export JSONL stream and converts it to
// ATF v0.1.1. One session_id (or trace_id fallback) = one trajectory.
func Convert(r io.Reader) (*Result, error) {
	res := &Result{SpansByTrajectory: map[string][]schema.Span{}}
	trajMeta := map[string]schema.Trajectory{}
	trajStart := map[string]int64{}
	trajOrder := []string{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var run Run
		if err := json.Unmarshal([]byte(line), &run); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		trajID := run.SessionID
		if trajID == "" {
			trajID = run.TraceID
		}
		if trajID == "" {
			return nil, fmt.Errorf("line %d: run %s has neither session_id nor trace_id", lineNo, run.ID)
		}
		span, err := toSpan(run, trajID)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if _, ok := trajMeta[trajID]; !ok {
			trajOrder = append(trajOrder, trajID)
			trajStart[trajID] = span.StartedAtUnixMs
			trajMeta[trajID] = newTrajectory(trajID, span.StartedAtUnixMs, run)
		}
		if span.StartedAtUnixMs < trajStart[trajID] {
			trajStart[trajID] = span.StartedAtUnixMs
		}
		res.SpansByTrajectory[trajID] = append(res.SpansByTrajectory[trajID], span)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	for _, id := range trajOrder {
		spans := res.SpansByTrajectory[id]
		sort.SliceStable(spans, func(i, j int) bool {
			return spans[i].StartedAtUnixMs < spans[j].StartedAtUnixMs
		})
		tr := trajMeta[id]
		tr.StartedAtUnixMs = trajStart[id]
		res.Trajectories = append(res.Trajectories, tr)
	}
	return res, nil
}

func newTrajectory(id string, startMs int64, run Run) schema.Trajectory {
	meta := map[string]string{}
	for k, v := range run.Extra.Metadata {
		meta[k] = v
	}
	return schema.Trajectory{
		Type:            "trajectory",
		Version:         schema.ATFVersion,
		TrajectoryID:    id,
		Source:          "langsmith",
		StartedAtUnixMs: startMs,
		Metadata:        meta,
	}
}

func toSpan(run Run, trajID string) (schema.Span, error) {
	start, err := parseMs(run.StartTime)
	if err != nil {
		return schema.Span{}, fmt.Errorf("run %s start_time: %w", run.ID, err)
	}
	end, err := parseMs(run.EndTime)
	if err != nil {
		return schema.Span{}, fmt.Errorf("run %s end_time: %w", run.ID, err)
	}
	model := run.Extra.ModelName
	if model == "" && run.Extra.InvocationParams != nil {
		if v, ok := run.Extra.InvocationParams["model_name"].(string); ok {
			model = v
		}
	}
	attrs := map[string]string{}
	if len(run.Tags) > 0 {
		b, err := json.Marshal(run.Tags)
		if err == nil {
			attrs["tags"] = string(b)
		}
	}
	if run.DottedOrder != "" {
		attrs["dotted_order"] = run.DottedOrder
	}
	if run.ExecutionOrder != 0 {
		attrs["execution_order"] = fmt.Sprintf("%d", run.ExecutionOrder)
	}
	if len(attrs) == 0 {
		attrs = nil
	}
	s := schema.Span{
		Type:            "span",
		SpanID:          run.ID,
		TrajectoryID:    trajID,
		ParentSpanID:    run.ParentRunID,
		Name:            run.Name,
		Kind:            mapKind(run.RunType),
		StartedAtUnixMs: start,
		EndedAtUnixMs:   end,
		Model:           model,
		Input:           marshalOrEmpty(run.Inputs),
		Output:          marshalOrEmpty(run.Outputs),
		Error:           run.Error,
		CostUSD:         run.Extra.TotalCost,
	}
	if run.Extra.TokenUsage != nil {
		s.TokensPrompt = run.Extra.TokenUsage.PromptTokens
		s.TokensCompletion = run.Extra.TokenUsage.CompletionTokens
	}
	if len(attrs) > 0 {
		s.Attributes = attrs
	}
	return s, nil
}

func parseMs(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
}

func mapKind(runType string) string {
	switch runType {
	case "llm":
		return "llm"
	case "tool":
		return "tool"
	case "retriever", "retrieval":
		return "retrieval"
	default:
		return "custom"
	}
}

func marshalOrEmpty(m map[string]any) string {
	if m == nil {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}
