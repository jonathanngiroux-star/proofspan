// Package corpus analyzes a real vendor export the way a design partner
// needs before trusting a migration: parse rate, per-key coverage against
// the converter's known-field set, an honest unknown-key census, and
// timing. This is the pre-migration report — it runs before any span is
// written and never mutates the export.
package corpus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// Report is the analysis of one export file.
// Report is the read-only pre-migration analysis of an export. JSON tags
// keep the machine-readable stdout channel snake_case, consistent with
// wasm.Result and eval's TrajectoryReport.
type Report struct {
	Source        string         `json:"source"`
	Path          string         `json:"path"`
	LinesTotal    int            `json:"lines_total"`
	LinesParsed   int            `json:"lines_parsed"`
	ParseErrors   int            `json:"parse_errors"`
	Trajectories  int            `json:"trajectories"`
	SpansByKind   map[string]int `json:"spans_by_kind"`
	FieldCoverage map[string]int `json:"field_coverage"` // known key → spans carrying it
	UnknownKeys   map[string]int `json:"unknown_keys"`   // unknown key → occurrences
	DurationMs    int64          `json:"duration_ms"`
}

// Analyze scans an export and produces the report. known is the set of
// converter-mapped keys (dotted paths, e.g. "extra.token_usage.prompt_tokens").
func Analyze(path, source string, known map[string]bool) (*Report, error) {
	rep := &Report{
		Source:        source,
		Path:          path,
		SpansByKind:   map[string]int{},
		FieldCoverage: map[string]int{},
		UnknownKeys:   map[string]int{},
	}
	start := time.Now()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	sessions := map[string]bool{}
	for sc.Scan() {
		rep.LinesTotal++
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var raw map[string]any
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			rep.ParseErrors++
			continue
		}
		if !validRun(raw, source) {
			rep.ParseErrors++
			continue
		}
		rep.LinesParsed++
		if sid, _ := raw["session_id"].(string); sid != "" {
			sessions[sid] = true
		}
		if sid, _ := raw["sessionId"].(string); sid != "" {
			sessions[sid] = true
		}
		if rt, _ := raw["run_type"].(string); rt != "" {
			rep.SpansByKind[rt]++
		}
		if et, _ := raw["eventType"].(string); et != "" {
			rep.SpansByKind[et]++
		}
		walk(raw, "", known, rep)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	rep.Trajectories = len(sessions)
	rep.DurationMs = time.Since(start).Milliseconds()
	return rep, nil
}

// validRun applies the converter's hard requirements per source:
// langsmith needs id + start_time + end_time; honeyhive needs eventId
// + startedAt + endedAt.
func validRun(raw map[string]any, source string) bool {
	switch source {
	case "honeyhive":
		id, _ := raw["eventId"].(string)
		st, _ := raw["startedAt"].(string)
		en, _ := raw["endedAt"].(string)
		return id != "" && st != "" && en != ""
	default:
		id, _ := raw["id"].(string)
		st, _ := raw["start_time"].(string)
		en, _ := raw["end_time"].(string)
		return id != "" && st != "" && en != ""
	}
}

// walk descends the raw run object, classifying every leaf key against known.
// Keys mapped wholesale (inputs, outputs, extra.metadata, ...) are counted as
// covered and NOT descended into — their inner structure is preserved by
// the converter as an opaque JSON document, so inner keys are not "unknown".
func walk(m map[string]any, prefix string, known map[string]bool, rep *Report) {
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if known[path] {
			rep.FieldCoverage[path]++
			continue // do not descend: covered as an opaque document
		}
		switch child := v.(type) {
		case map[string]any:
			walk(child, path, known, rep)
		default:
			rep.UnknownKeys[path]++
		}
	}
}

// KnownKeys returns the converter's mapped field set for a source.
func KnownKeys(source string) map[string]bool {
	ls := []string{
		"id", "name", "run_type", "inputs", "outputs", "error",
		"start_time", "end_time", "parent_run_id", "session_id",
		"trace_id", "tags", "dotted_order", "execution_order",
		"extra.metadata", "extra.model_name", "extra.invocation_params",
		"extra.token_usage", "extra.token_usage.prompt_tokens",
		"extra.token_usage.completion_tokens", "extra.total_cost",
	}
	hh := []string{
		"eventId", "sessionId", "parentId", "eventName", "eventType",
		"inputs", "outputs", "startedAt", "endedAt", "model", "error",
		"tokenUsage", "tokenUsage.promptTokens", "tokenUsage.completionTokens",
		"cost",
	}
	var keys []string
	switch source {
	case "langsmith":
		keys = ls
	case "honeyhive":
		keys = hh
	default:
		keys = append(ls, hh...)
	}
	set := map[string]bool{}
	for _, k := range keys {
		set[k] = true
	}
	return set
}

// Markdown renders the report for docs/corpus/<source>.md.
func (r *Report) Markdown(source, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Corpus report: %s export\n\n", source)
	fmt.Fprintf(&b, "File: `%s`\n\n", path)
	fmt.Fprintf(&b, "| Metric | Value |\n|---|---|\n")
	fmt.Fprintf(&b, "| Parsed lines | %d of %d |\n", r.LinesParsed, r.LinesTotal)
	fmt.Fprintf(&b, "| Parse errors | %d |\n", r.ParseErrors)
	fmt.Fprintf(&b, "| Trajectories (sessions) | %d |\n", r.Trajectories)
	fmt.Fprintf(&b, "| Analysis time | %d ms |\n", r.DurationMs)
	fmt.Fprintf(&b, "\n## Spans by run type\n\n")
	fmt.Fprintf(&b, "| Type | Count |\n|---|---|\n")
	for _, k := range sortedKeys(r.SpansByKind) {
		fmt.Fprintf(&b, "| %s | %d |\n", k, r.SpansByKind[k])
	}
	fmt.Fprintf(&b, "\n## Known-field coverage\n\n")
	fmt.Fprintf(&b, "| Field | Spans carrying it |\n|---|---|\n")
	known := KnownKeys(source)
	for _, k := range sortedBoolKeys(known) {
		c := r.FieldCoverage[k]
		fmt.Fprintf(&b, "| %s | %d |\n", k, c)
	}
	if len(r.UnknownKeys) > 0 {
		fmt.Fprintf(&b, "\n## Unknown keys (not mapped by the converter)\n\n")
		fmt.Fprintf(&b, "| Key | Occurrences |\n|---|---|\n")
		for _, k := range sortedKeys(r.UnknownKeys) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, r.UnknownKeys[k])
		}
		fmt.Fprintf(&b, "\nThese keys are preserved in the source export but have no ATF target yet. ")
		fmt.Fprintf(&b, "They are NOT dropped silently — the migration diff lists them, and this census is the ")
		fmt.Fprintf(&b, "backlog for reaching higher fidelity.\n")
	} else {
		fmt.Fprintf(&b, "\n## Unknown keys\n\nNone — every key in this export maps to an ATF field.\n")
	}
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedBoolKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
