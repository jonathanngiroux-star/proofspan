// Command corpusgen generates the shared 10k-step fixture corpus in
// three formats from one deterministic source of truth:
//
//	testdata/corpus/atf/corpus.jsonl        native ATF v0.1.1
//	testdata/corpus/langsmith/corpus.jsonl  LangSmith run export
//	testdata/corpus/honeyhive/corpus.jsonl  HoneyHive run export
//
// All three describe the same trajectories. Converters must reproduce
// the ATF file from either vendor export at >95% field parity — that
// round-trip IS the fidelity report.
//
// Deterministic: seeded PRNG, fixed base timestamp, no clock, no network.
// PII-scrubbed: synthetic IDs and payloads only.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	totalSpans    = 10000
	spansPerTraj  = 25
	baseMs        = 1727452800000 // 2024-09-27T16:00:00Z
	models        = "gpt-4o-2024-08-06,gpt-4o-mini-2024-07-18,claude-3-5-sonnet-20241022"
	vendorPrefix  = "vd_"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: corpusgen <output-dir>")
		os.Exit(2)
	}
	out := os.Args[1]
	rng := rand.New(rand.NewSource(42)) // deterministic

	var atf, ls, hh []string
	trajCount := 0
	spans := 0
	for spans < totalSpans {
		trajCount++
		trajID := fmt.Sprintf("trj_%05d", trajCount)
		n := spansPerTraj
		if spans+n > totalSpans {
			n = totalSpans - spans
		}
		start := baseMs + int64(trajCount)*3600_000
		// session user/env metadata
		user := fmt.Sprintf("u_%03d", rng.Intn(200))
		env := pick(rng, "prod,staging,dev")
		// one fact per trajectory: retrieval outputs cite it, llm outputs
		// ground in it (or deliberately don't — the planted defect)
		fact := fmt.Sprintf("fact_%d_%d", trajCount, 100+rng.Intn(900))
		atf = append(atf, fmt.Sprintf(`{"type":"trajectory","version":"atf/v0.1.1","trajectory_id":%q,"source":"native","started_at_unix_ms":%d,"metadata":{"user":%q,"env":%q}}`, trajID, start, user, env))
		ls = append(ls, "") // langsmith export has no separate header line
		var parent string
		for i := 0; i < n; i++ {
			spans++
			spanID := fmt.Sprintf("spn_%05d_%03d", trajCount, i)
			kind := pickKind(rng)
			name := fmt.Sprintf("%s_%s", kind, strings.ToLower(strings.ReplaceAll(pick(rng, "search,synthesize,verify,fetch,route,summarize"), ",", "_")))
			name = fmt.Sprintf("%s_step_%d", kind, i)
			model := ""
			tokP, tokC, cost := 0, 0, 0.0
			if kind == "llm" {
				model = pick(rng, models)
				tokP = 50 + rng.Intn(2000)
				tokC = 5 + rng.Intn(500)
				cost = float64(tokP)*0.0000025 + float64(tokC)*0.00001
			}
			s := start + int64(i)*1500
			e := s + int64(200+rng.Intn(1200))
			// Payload realism for the builtin factual-consistency judge:
			// retrieval outputs cite the trajectory's fact; llm outputs ground
			// in it — except ~20% that deliberately cite a wrong fact
			// (planted defect the judge must catch).
			grounded := ""
			if kind == "retrieval" {
				grounded = fact
			} else if kind == "llm" {
				if rng.Intn(5) == 0 {
					grounded = fmt.Sprintf("fact_%d_%d", trajCount, 100+rng.Intn(900)) // planted defect
				} else {
					grounded = fact
				}
			}
			// vendor payloads are raw JSON objects...
			input := fmt.Sprintf(`{"%s_input":"payload_%d"}`, kind, spans)
			output := fmt.Sprintf(`{"%s_output":"result_%d %s"}`, kind, spans, grounded)
			// ...while ATF stores them as JSON-encoded strings (matching converter output)
			inputStr, _ := json.Marshal(input)
			outputStr, _ := json.Marshal(output)
			errStr := ""
			if rng.Intn(50) == 0 {
				errStr = pick(rng, "rate limited,timeout,context overflow")
			}
			parentAttr := ""
			if parent != "" {
				parentAttr = fmt.Sprintf(`,"parent_span_id":%q`, parent)
			}
			// native ATF line
			atf = append(atf, fmt.Sprintf(`{"type":"span","span_id":%q,"trajectory_id":%q%s,"name":%q,"kind":%q,"started_at_unix_ms":%d,"ended_at_unix_ms":%d%s%s%s%s}`,
				spanID, trajID, parentAttr, name, kind, s, e,
				modelJSON(model), ioJSON("input", string(inputStr)), ioJSON("output", string(outputStr)),
				tokensJSON(tokP, tokC, cost, errStr)))
			// langsmith export line
			lsParent := ""
			if parent != "" {
				lsParent = fmt.Sprintf(`,"parent_run_id":%q`, parent)
			}
			lsErr := ""
			if errStr != "" {
				lsErr = fmt.Sprintf(`,"error":%q`, errStr)
			}
			lsExtra := fmt.Sprintf(`"extra":{"metadata":{"user":%q,"env":%q}%s%s}`,
				user, env, lsModelExtra(model), lsTokensExtra(tokP, tokC, cost))
			lsRunType := map[string]string{"llm": "llm", "tool": "tool", "retrieval": "retriever", "custom": "chain"}[kind]
			ls = append(ls, fmt.Sprintf(`{"id":%q,"name":%q,"run_type":%q%s,"inputs":%s,"outputs":%s,"start_time":%q,"end_time":%q,"session_id":%q,"tags":[%q],"dotted_order":%q,"execution_order":%d,%s%s}`,
				spanID, name, lsRunType, lsParent, input, output,
				rfc3339(s), rfc3339(e), trajID,
				pick(rng, "prod,eval,staging"), dottedOrder(s, spanID), i+1, lsExtra, lsErr))
			// honeyhive export line
			hhParent := ""
			if parent != "" {
				hhParent = fmt.Sprintf(`,"parentId":%q`, parent)
			}
			hhErr := ""
			if errStr != "" {
				hhErr = fmt.Sprintf(`,"error":%q`, errStr)
			}
			hh = append(hh, fmt.Sprintf(`{"eventId":%q,"sessionId":%q%s,"eventName":%q,"eventType":%q,"inputs":%s,"outputs":%s,"startedAt":%q,"endedAt":%q%s%s%s}`,
				spanID, trajID, hhParent, name, hhType(kind), input, output,
				rfc3339(s), rfc3339(e), hhModel(model), hhTokens(tokP, tokC, cost), hhErr))
			parent = spanID
		}
	}
	_ = vendorPrefix

	if err := write(filepath.Join(out, "atf"), "corpus.jsonl", nonEmpty(atf)); err != nil {
		fail(err)
	}
	if err := write(filepath.Join(out, "langsmith"), "corpus.jsonl", nonEmpty(ls)); err != nil {
		fail(err)
	}
	if err := write(filepath.Join(out, "honeyhive"), "corpus.jsonl", nonEmpty(hh)); err != nil {
		fail(err)
	}
	fmt.Printf("corpus: %d trajectories, %d spans, 3 formats → %s\n", trajCount, spans, out)
}

func nonEmpty(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func pick(rng *rand.Rand, commaList string) string {
	items := strings.Split(commaList, ",")
	return items[rng.Intn(len(items))]
}

func pickKind(rng *rand.Rand) string {
	switch rng.Intn(10) {
	case 0, 1, 2, 3:
		return "llm"
	case 4, 5, 6:
		return "tool"
	case 7, 8:
		return "retrieval"
	default:
		return "custom"
	}
}

func modelJSON(model string) string {
	if model == "" {
		return ""
	}
	return fmt.Sprintf(`,"model":%q`, model)
}

func ioJSON(which, body string) string {
	// body is already a JSON object literal
	return fmt.Sprintf(`,"%s":%s`, which, body)
}

func tokensJSON(tokP, tokC int, cost float64, errStr string) string {
	if tokP == 0 && tokC == 0 && cost == 0 && errStr == "" {
		return ""
	}
	parts := []string{}
	if errStr != "" {
		parts = append(parts, fmt.Sprintf(`"error":%q`, errStr))
	}
	if tokP > 0 || tokC > 0 {
		parts = append(parts, fmt.Sprintf(`"tokens_prompt":%d,"tokens_completion":%d`, tokP, tokC))
	}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf(`"cost_usd":%.6f`, cost))
	}
	return "," + strings.Join(parts, ",")
}

func lsModelExtra(model string) string {
	if model == "" {
		return ""
	}
	return fmt.Sprintf(`,"model_name":%q`, model)
}

func lsTokensExtra(tokP, tokC int, cost float64) string {
	if tokP == 0 && tokC == 0 && cost == 0 {
		return ""
	}
	parts := []string{}
	if tokP > 0 || tokC > 0 {
		parts = append(parts, fmt.Sprintf(`"token_usage":{"prompt_tokens":%d,"completion_tokens":%d}`, tokP, tokC))
	}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf(`"total_cost":%.6f`, cost))
	}
	if len(parts) == 0 {
		return ""
	}
	return "," + strings.Join(parts, ",")
}

func hhType(kind string) string {
	switch kind {
	case "llm":
		return "model"
	case "retrieval":
		return "retriever"
	case "custom":
		return "agent"
	default:
		return "tool"
	}
}

func hhModel(model string) string {
	if model == "" {
		return ""
	}
	return fmt.Sprintf(`,"model":%q`, model)
}

func hhTokens(tokP, tokC int, cost float64) string {
	if tokP == 0 && tokC == 0 && cost == 0 {
		return ""
	}
	parts := []string{}
	if tokP > 0 || tokC > 0 {
		parts = append(parts, fmt.Sprintf(`"tokenUsage":{"promptTokens":%d,"completionTokens":%d}`, tokP, tokC))
	}
	if cost > 0 {
		parts = append(parts, fmt.Sprintf(`"cost":%.6f`, cost))
	}
	return "," + strings.Join(parts, ",")
}

func rfc3339(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

func dottedOrder(startMs int64, spanID string) string {
	return time.UnixMilli(startMs).UTC().Format("20060102T150405.000") + "Z" + spanID
}

func write(dir, name string, lines []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	for _, l := range lines {
		if _, err := w.WriteString(l + "\n"); err != nil {
			return err
		}
	}
	return w.Flush()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "corpusgen:", err)
	os.Exit(1)
}
