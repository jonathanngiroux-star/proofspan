# docs/migrate.md — Proofspan converters

The migration CLI is the product. Both converters ship. Fidelity is measured against the shared 10k-step corpus and enforced in CI (`go run ./cmd/fidelitygen`; exit nonzero under threshold).

| Source | Command | Field parity (10k corpus) |
|---|---|---|
| LangSmith | `proofspan migrate --from=langsmith [--dry-run] --format=json FILE` | 100.00% |
| HoneyHive | `proofspan migrate --from=honeyhive [--dry-run] --format=json FILE` | 100.00% |

Full reports: `docs/fidelity/langsmith.md`, `docs/fidelity/honeyhive.md`.

## Dry-run: the diff skeleton

```sh
./proofspan migrate --from=langsmith --dry-run --format=json traces.jsonl
```

Output (machine-readable, stable shape; CI diffs it):

```json
{
  "dry_run": true,
  "source": "langsmith",
  "target_version": "atf/v0.1.1",
  "input_file": "traces.jsonl",
  "trajectories": 1,
  "runs_read": 3,
  "spans_planned": 3,
  "field_mappings": [ { "from": "id", "to": "span_id" }, "..." ],
  "dropped_fields": [ { "field": "extra.invocation_params (non-model keys)", "note": "out of scope v0.1" } ],
  "sample_span": { "span_id": "run-0001", "kind": "tool", "..." : "..." }
}
```

## Write path: ingest into SQLite

```sh
./proofspan migrate --from=langsmith --db=proofspan.sqlite traces.jsonl
# migrated 400 trajectories, 10000 spans into proofspan.sqlite
```

Both converters idempotently replace a trajectory on re-ingest (upsert by trajectory_id).

## Field mapping (LangSmith → ATF v0.1.1)

| LangSmith | ATF | Note |
|---|---|---|
| `id` | `span_id` | |
| `name` | `name` | |
| `run_type` | `kind` | `llm`→`llm`, `tool`→`tool`, `retriever`→`retrieval`, else `custom` |
| `session_id` | `trajectory_id` | `trace_id` fallback |
| `parent_run_id` | `parent_span_id` | |
| `start_time` / `end_time` | `started_at_unix_ms` / `ended_at_unix_ms` | RFC 3339 → epoch ms |
| `inputs` / `outputs` | `input` / `output` | JSON-encoded map |
| `error` | `error` | |
| `tags` | `attributes.tags` | JSON array |
| `dotted_order` | `attributes.dotted_order` | replay ordering preserved |
| `execution_order` | `attributes.execution_order` | |
| `extra.model_name` | `model` | `extra.invocation_params.model_name` fallback |
| `extra.token_usage.*` | `tokens_prompt` / `tokens_completion` | |
| `extra.total_cost` | `cost_usd` | |
| `extra.metadata` | trajectory `metadata` | header per session |

## Field mapping (HoneyHive → ATF v0.1.1)

| HoneyHive | ATF | Note |
|---|---|---|
| `eventId` | `span_id` | |
| `sessionId` | `trajectory_id` | |
| `parentId` | `parent_span_id` | |
| `eventName` | `name` | |
| `eventType` | `kind` | `model`→`llm`, `retriever`→`retrieval`, `tool`→`tool`, else `custom` |
| `startedAt` / `endedAt` | `started_at_unix_ms` / `ended_at_unix_ms` | RFC 3339 → epoch ms |
| `inputs` / `outputs` | `input` / `output` | JSON-encoded map |
| `model` | `model` | |
| `error` | `error` | |
| `tokenUsage.promptTokens` / `completionTokens` | `tokens_prompt` / `tokens_completion` | |
| `cost` | `cost_usd` | |

Dropped (tracked, never silent): LangSmith `extra.invocation_params` non-model keys; HoneyHive agent/chain nesting depth (flattened to parent_span_id).

## Corpus

`testdata/corpus/{atf,langsmith,honeyhive}/corpus.jsonl` — 10,000 spans, 400 trajectories, deterministic (seed 42), PII-scrubbed. Regenerate: `go run ./cmd/corpusgen testdata/corpus`. The vendor exports are faithful simulations of each REST run-object shape; the converters' contract is pinned by `internal/migrate/*/convert_test.go`.
