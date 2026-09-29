package honeyhive

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMigrateDryRunDeterministicSample pins the same contract as the
// langsmith converter: the dry-run sample span is the earliest-starting
// span, regardless of Go's randomized map iteration order.
func TestMigrateDryRunDeterministicSample(t *testing.T) {
	const fixture = `{"eventId":"ev-0001","sessionId":"sess_a","eventName":"first","eventType":"tool","inputs":{},"outputs":{},"startedAt":"2024-09-27T16:00:00.100Z","endedAt":"2024-09-27T16:00:00.400Z"}
{"eventId":"ev-0002","sessionId":"sess_b","eventName":"second","eventType":"model","inputs":{},"outputs":{},"startedAt":"2024-09-27T15:00:00.500Z","endedAt":"2024-09-27T15:00:02.100Z","model":"gpt-4o"}
`
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		var buf bytes.Buffer
		if err := MigrateDryRun(&buf, path); err != nil {
			t.Fatal(err)
		}
		var skel DiffSkeleton
		if err := json.Unmarshal(buf.Bytes(), &skel); err != nil {
			t.Fatal(err)
		}
		// ev-0002 starts an hour earlier and lives in a different session —
		// map iteration order must not change the sample
		if skel.SampleSpan == nil || skel.SampleSpan.SpanID != "ev-0002" {
			t.Fatalf("iteration %d: sample span must be earliest (ev-0002), got %+v", i, skel.SampleSpan)
		}
	}
}
