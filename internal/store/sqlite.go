// Package store persists trajectories and eval runs in SQLite.
// Pure-Go driver (modernc.org/sqlite): CGO_ENABLED=0 stays valid, one binary.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers "sqlite"

	"proofspan/internal/schema"
)

// Store wraps the SQLite database holding trajectories, spans, and eval runs.
type Store struct {
	db *sql.DB
}

// EvalRun is one assertion result over one trajectory.
type EvalRun struct {
	ID               string          `json:"id"`
	TrajectoryID     string          `json:"trajectory_id"`
	AssertionID      string          `json:"assertion_id"`
	AssertionVersion string          `json:"assertion_version"`
	JudgeID          string          `json:"judge_id,omitempty"`
	JudgeFingerprint string          `json:"judge_fingerprint,omitempty"`
	Status           string          `json:"status"` // pass|fail|error
	Detail           json.RawMessage `json:"detail"`
	CreatedAtUnixMs  int64           `json:"created_at_unix_ms"`
}

// Open creates (if needed) and migrates the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // sqlite single-writer; avoids database-locked churn
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Exec runs a statement with no rows expected (DDL, DML). Exposed for
// satellite schemas (SCIM) that live in the same single-file database.
func (s *Store) Exec(query string, args ...any) (sql.Result, error) {
	return s.db.Exec(query, args...)
}

// QueryRow returns a single-row query handle.
func (s *Store) QueryRow(query string, args ...any) *sql.Row {
	return s.db.QueryRow(query, args...)
}

// Query returns a multi-row result.
func (s *Store) Query(query string, args ...any) (*sql.Rows, error) {
	return s.db.Query(query, args...)
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS trajectories (
			trajectory_id      TEXT PRIMARY KEY,
			version            TEXT NOT NULL,
			source             TEXT NOT NULL,
			started_at_unix_ms INTEGER NOT NULL,
			metadata           TEXT NOT NULL DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS spans (
			span_id            TEXT NOT NULL,
			trajectory_id      TEXT NOT NULL REFERENCES trajectories(trajectory_id) ON DELETE CASCADE,
			parent_span_id     TEXT,
			name               TEXT NOT NULL,
			kind               TEXT NOT NULL,
			started_at_unix_ms INTEGER NOT NULL,
			ended_at_unix_ms   INTEGER NOT NULL,
			model              TEXT,
			input              TEXT,
			output             TEXT,
			tokens_prompt      INTEGER NOT NULL DEFAULT 0,
			tokens_completion  INTEGER NOT NULL DEFAULT 0,
			cost_usd           REAL NOT NULL DEFAULT 0,
			error              TEXT,
			attributes         TEXT NOT NULL DEFAULT '{}',
			PRIMARY KEY (trajectory_id, span_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_spans_trajectory ON spans(trajectory_id, started_at_unix_ms)`,
		`CREATE TABLE IF NOT EXISTS eval_runs (
			id                 TEXT PRIMARY KEY,
			trajectory_id      TEXT NOT NULL REFERENCES trajectories(trajectory_id) ON DELETE CASCADE,
			assertion_id       TEXT NOT NULL,
			assertion_version  TEXT NOT NULL,
			judge_id           TEXT,
			judge_fingerprint  TEXT,
			status             TEXT NOT NULL,
			detail             TEXT NOT NULL DEFAULT '{}',
			created_at_unix_ms INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_eval_runs_trajectory ON eval_runs(trajectory_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate schema: %w", err)
		}
	}
	return nil
}

// SaveTrajectory persists a trajectory header and its spans atomically.
// Re-saving the same trajectory replaces it (idempotent migration target).
func (s *Store) SaveTrajectory(t schema.Trajectory, spans []schema.Span) error {
	if t.TrajectoryID == "" {
		return fmt.Errorf("trajectory_id required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	meta, err := json.Marshal(t.Metadata)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO trajectories (trajectory_id, version, source, started_at_unix_ms, metadata)
		VALUES (?,?,?,?,?) ON CONFLICT(trajectory_id) DO UPDATE SET
		version=excluded.version, source=excluded.source,
		started_at_unix_ms=excluded.started_at_unix_ms, metadata=excluded.metadata`,
		t.TrajectoryID, t.Version, t.Source, t.StartedAtUnixMs, string(meta)); err != nil {
		return fmt.Errorf("save trajectory %s: %w", t.TrajectoryID, err)
	}
	if _, err = tx.Exec(`DELETE FROM spans WHERE trajectory_id = ?`, t.TrajectoryID); err != nil {
		return err
	}
	const ins = `INSERT INTO spans (span_id, trajectory_id, parent_span_id, name, kind,
		started_at_unix_ms, ended_at_unix_ms, model, input, output,
		tokens_prompt, tokens_completion, cost_usd, error, attributes)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	for _, sp := range spans {
		if sp.SpanID == "" {
			return fmt.Errorf("span with empty span_id in trajectory %s", t.TrajectoryID)
		}
		if sp.TrajectoryID != "" && sp.TrajectoryID != t.TrajectoryID {
			return fmt.Errorf("span %s claims trajectory %s, expected %s", sp.SpanID, sp.TrajectoryID, t.TrajectoryID)
		}
		attrs := "{}"
		if sp.Attributes != nil {
			b, err := json.Marshal(sp.Attributes)
			if err != nil {
				return err
			}
			attrs = string(b)
		}
		if _, err = tx.Exec(ins, sp.SpanID, t.TrajectoryID, nullStr(sp.ParentSpanID), sp.Name, sp.Kind,
			sp.StartedAtUnixMs, sp.EndedAtUnixMs, nullStr(sp.Model), nullStr(sp.Input), nullStr(sp.Output),
			sp.TokensPrompt, sp.TokensCompletion, sp.CostUSD, nullStr(sp.Error), attrs); err != nil {
			return fmt.Errorf("save span %s: %w", sp.SpanID, err)
		}
	}
	return tx.Commit()
}

// GetTrajectory loads a trajectory header and its spans ordered by start time.
func (s *Store) GetTrajectory(id string) (schema.Trajectory, []schema.Span, error) {
	var t schema.Trajectory
	var meta string
	row := s.db.QueryRow(`SELECT trajectory_id, version, source, started_at_unix_ms, metadata
		FROM trajectories WHERE trajectory_id = ?`, id)
	err := row.Scan(&t.TrajectoryID, &t.Version, &t.Source, &t.StartedAtUnixMs, &meta)
	if err == sql.ErrNoRows {
		return t, nil, fmt.Errorf("trajectory %q not found", id)
	}
	if err != nil {
		return t, nil, err
	}
	_ = json.Unmarshal([]byte(meta), &t.Metadata)
	if t.Metadata == nil {
		t.Metadata = map[string]string{}
	}
	t.Type = "trajectory"

	rows, err := s.db.Query(`SELECT span_id, parent_span_id, name, kind, started_at_unix_ms, ended_at_unix_ms,
		model, input, output, tokens_prompt, tokens_completion, cost_usd, error, attributes
		FROM spans WHERE trajectory_id = ? ORDER BY started_at_unix_ms ASC`, id)
	if err != nil {
		return t, nil, err
	}
	defer rows.Close()
	var spans []schema.Span
	for rows.Next() {
		var sp schema.Span
		var parent, model, in, out, errMsg, attrs sql.NullString
		if err = rows.Scan(&sp.SpanID, &parent, &sp.Name, &sp.Kind, &sp.StartedAtUnixMs, &sp.EndedAtUnixMs,
			&model, &in, &out, &sp.TokensPrompt, &sp.TokensCompletion, &sp.CostUSD, &errMsg, &attrs); err != nil {
			return t, nil, err
		}
		sp.Type = "span"
		sp.TrajectoryID = id
		sp.ParentSpanID = parent.String
		sp.Model = model.String
		sp.Input = in.String
		sp.Output = out.String
		sp.Error = errMsg.String
		if attrs.Valid && attrs.String != "" {
			_ = json.Unmarshal([]byte(attrs.String), &sp.Attributes)
		}
		if sp.Attributes == nil {
			sp.Attributes = map[string]string{}
		}
		spans = append(spans, sp)
	}
	return t, spans, rows.Err()
}

// ListTrajectoryIDs returns stored trajectory IDs, newest first.
func (s *Store) ListTrajectoryIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT trajectory_id FROM trajectories ORDER BY started_at_unix_ms DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// SaveEvalRun persists one assertion result over one trajectory.
func (s *Store) SaveEvalRun(r EvalRun) error {
	detail := "{}"
	if len(r.Detail) > 0 {
		detail = string(r.Detail)
	}
	_, err := s.db.Exec(`INSERT INTO eval_runs (id, trajectory_id, assertion_id, assertion_version,
		judge_id, judge_fingerprint, status, detail, created_at_unix_ms)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET
		status=excluded.status, detail=excluded.detail, judge_id=excluded.judge_id,
		judge_fingerprint=excluded.judge_fingerprint`,
		r.ID, r.TrajectoryID, r.AssertionID, r.AssertionVersion,
		nullStr(r.JudgeID), nullStr(r.JudgeFingerprint), r.Status, detail, r.CreatedAtUnixMs)
	return err
}

// ListEvalRuns returns all eval results for a trajectory, oldest first.
func (s *Store) ListEvalRuns(trajectoryID string) ([]EvalRun, error) {
	rows, err := s.db.Query(`SELECT id, trajectory_id, assertion_id, assertion_version,
		judge_id, judge_fingerprint, status, detail, created_at_unix_ms
		FROM eval_runs WHERE trajectory_id = ? ORDER BY created_at_unix_ms ASC`, trajectoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []EvalRun
	for rows.Next() {
		var r EvalRun
		var detail string
		var judgeID, judgeFP sql.NullString
		if err := rows.Scan(&r.ID, &r.TrajectoryID, &r.AssertionID, &r.AssertionVersion,
			&judgeID, &judgeFP, &r.Status, &detail, &r.CreatedAtUnixMs); err != nil {
			return nil, err
		}
		r.JudgeID = judgeID.String
		r.JudgeFingerprint = judgeFP.String
		if detail != "" {
			r.Detail = json.RawMessage(detail)
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func nullStr(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
