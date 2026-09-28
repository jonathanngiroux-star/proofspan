// Package honeyhive converts HoneyHive run exports to ATF v0.1.1.
//
// Fidelity targets (docs/fidelity/honeyhive.md): >95% field parity on the
// shared 10k-step corpus, MAD assertion drift <2%, cost drift <0.5%.
package honeyhive

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"proofspan/internal/schema"
)

// Event mirrors one JSONL line of a HoneyHive run export.
type Event struct {
	EventID   string         `json:"eventId"`
	SessionID string         `json:"sessionId"`
	ParentID  string         `json:"parentId"`
	EventName string         `json:"eventName"`
	EventType string         `json:"eventType"` // model|tool|retriever|agent|chain
	Inputs    map[string]any `json:"inputs"`
	Outputs   map[string]any `json:"outputs"`
	StartedAt string         `json:"startedAt"` // RFC 3339
	EndedAt   string         `json:"endedAt"`
	Model     string         `json:"model"`
	Error     string         `json:"error"`
	TokenUsage *struct {
		PromptTokens     int64 `json:"promptTokens"`
		CompletionTokens int64 `json:"completionTokens"`
	} `json:"tokenUsage"`
	Cost float64 `json:"cost"`
}

// Result is a converted corpus, same shape as the langsmith converter.
type Result struct {
	Trajectories      []schema.Trajectory
	SpansByTrajectory map[string][]schema.Span
}

// Convert reads a HoneyHive run-export JSONL stream.
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
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		if ev.EventID == "" || ev.SessionID == "" {
			return nil, fmt.Errorf("line %d: event needs eventId and sessionId", lineNo)
		}
		start, err := parseMs(ev.StartedAt)
		if err != nil {
			return nil, fmt.Errorf("line %d: startedAt: %w", lineNo, err)
		}
		end, err := parseMs(ev.EndedAt)
		if err != nil {
			return nil, fmt.Errorf("line %d: endedAt: %w", lineNo, err)
		}
		if _, ok := trajMeta[ev.SessionID]; !ok {
			trajOrder = append(trajOrder, ev.SessionID)
			trajStart[ev.SessionID] = start
			trajMeta[ev.SessionID] = schema.Trajectory{
				Type:            "trajectory",
				Version:         schema.ATFVersion,
				TrajectoryID:    ev.SessionID,
				Source:          "honeyhive",
				StartedAtUnixMs: start,
				Metadata:        map[string]string{},
			}
		}
		if start < trajStart[ev.SessionID] {
			trajStart[ev.SessionID] = start
		}
		s := schema.Span{
			Type:            "span",
			SpanID:          ev.EventID,
			TrajectoryID:    ev.SessionID,
			ParentSpanID:    ev.ParentID,
			Name:            ev.EventName,
			Kind:            mapKind(ev.EventType),
			StartedAtUnixMs: start,
			EndedAtUnixMs:   end,
			Model:           ev.Model,
			Input:           marshalOrEmpty(ev.Inputs),
			Output:          marshalOrEmpty(ev.Outputs),
			Error:           ev.Error,
			CostUSD:         ev.Cost,
		}
		if ev.TokenUsage != nil {
			s.TokensPrompt = ev.TokenUsage.PromptTokens
			s.TokensCompletion = ev.TokenUsage.CompletionTokens
		}
		res.SpansByTrajectory[ev.SessionID] = append(res.SpansByTrajectory[ev.SessionID], s)
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

// ConvertFile is the CLI entry point.
func ConvertFile(path string) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Convert(f)
}

func mapKind(eventType string) string {
	switch eventType {
	case "model":
		return "llm"
	case "tool":
		return "tool"
	case "retriever":
		return "retrieval"
	default: // agent, chain, anything else
		return "custom"
	}
}

func parseMs(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.UnixMilli(), nil
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
