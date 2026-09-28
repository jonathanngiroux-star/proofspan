package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"proofspan/internal/judges"
	"proofspan/internal/store"
)

// Command judgerun executes pinned judges over one stored trajectory,
// end to end: manifest validation → provider resolution → live call →
// eval_runs persistence. Used by the CLI eval path and by the live test.
//
// Usage: judgerun <db> <manifest> <trajectory-id> <judge-id> [judge-id...]
func main() {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: judgerun <db> <manifest> <trajectory-id> <judge-id> [judge-id...]")
		os.Exit(2)
	}
	dbPath, manifestPath, trajID := os.Args[1], os.Args[2], os.Args[3]
	judgeIDs := os.Args[4:]

	st, err := store.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer st.Close()

	m, err := judges.Load(manifestPath)
	if err != nil {
		fatal(err)
	}

	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("PROOFSPAN_JUDGE_API_KEY")
	}
	eng := judges.NewEngine(m).WithAPIKey(apiKey)

	_, spans, err := st.GetTrajectory(trajID)
	if err != nil {
		fatal(err)
	}
	if len(spans) == 0 {
		fatal(fmt.Errorf("trajectory %s has no spans", trajID))
	}

	ctx := context.Background()
	failed := false
	for _, jid := range judgeIDs {
		v, err := eng.Judge(ctx, jid, trajID, spans)
		if err != nil {
			fmt.Printf("%s: ERROR %v\n", jid, err)
			failed = true
			continue
		}
		fmt.Printf("%s: %s (%s) %s\n", jid, v.Status, v.JudgeFingerprint, v.Detail)
		run := store.EvalRun{
			ID:               "judge-" + jid + "-" + trajID,
			TrajectoryID:     trajID,
			AssertionID:      "judge:" + jid,
			AssertionVersion: "v1.1.0",
			JudgeID:          jid,
			JudgeFingerprint: v.JudgeFingerprint,
			Status:           v.Status,
			Detail:           mustJSON(v),
			CreatedAtUnixMs:  time.Now().UnixMilli(),
		}
		if err := st.SaveEvalRun(run); err != nil {
			fatal(err)
		}
		if v.Status != "pass" {
			failed = true
		}
	}
	// verify persistence round-trip
	runs, err := st.ListEvalRuns(trajID)
	if err != nil {
		fatal(err)
	}
	fmt.Printf("persisted %d eval_runs for %s\n", len(runs), trajID)
	if failed {
		os.Exit(1)
	}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "judgerun:", err)
	os.Exit(1)
}
