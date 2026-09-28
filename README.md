# Proofspan

CI-native agent evaluation harness. Replaces LangSmith / HoneyHive for startup eng leads burning 4–6 hours/week pasting traces into spreadsheets.

**One Go binary. SQLite. MIT core.** Buyer: startup engineering leads with a $500–$2,000/mo line item they want to kill and traces they already own.

## 5-minute deploy

```sh
# 1. binary (Go 1.27+)
go build -o proofspan ./cmd/proofspan

# 2. migrate your existing traces (dry-run first, always)
./proofspan migrate --from=langsmith --dry-run --format=json traces.jsonl

# 3. ingest + gate
./proofspan migrate --from=langsmith --db=proofspan.sqlite traces.jsonl
./proofspan eval --db=proofspan.sqlite
```

Docker:

```sh
docker build -t proofspan .
docker run --rm -v "$PWD/data:/data" proofspan migrate --from=langsmith --db=/data/proofspan.sqlite traces.jsonl
docker run --rm -v "$PWD/data:/data" -p 7400:7400 proofspan serve --db=/data/proofspan.sqlite --scim
```

`dagger call eval` runs the whole gate (tests → corpus → fidelity → migrate → eval) in CI. See `.github/workflows/ci.yml`.

## What it does

| Capability | Status |
|---|---|
| LangSmith → ATF converter, >95% field parity | ✅ 100.00% on 10k-step corpus |
| HoneyHive → ATF converter, >95% field parity | ✅ 100.00% on 10k-step corpus |
| Dry-run migration diffs (machine-readable JSON) | ✅ |
| WASM assertion registry (span-correlation@1.0.0) | ✅ |
| Judge registry, pinned model_fingerprint, validated pre-run | ✅ |
| SQLite store, single binary | ✅ |
| Local serve + SCIM 2.0 minimal provider (feature-flagged) | ✅ |
| Fidelity reports | ✅ `docs/fidelity/` |

Parity and drift targets are enforced in CI — `go run ./cmd/fidelitygen` exits nonzero if any converter drops below 95% parity, 2% MAD drift, or 0.5% cost drift.

## Fidelity (the product)

Converters are graded against a shared 10,000-step corpus generated deterministically in three formats (native ATF, LangSmith export, HoneyHive export). Regenerate with `go run ./cmd/corpusgen testdata/corpus && go run ./cmd/fidelitygen testdata/corpus docs/fidelity`:

| Converter | Field parity | MAD drift | Cost drift |
|---|---|---|---|
| LangSmith | 100.00% | 0.0000 | 0.00000 |
| HoneyHive | 100.00% | 0.0000 | 0.00000 |

Full reports: `docs/fidelity/langsmith.md`, `docs/fidelity/honeyhive.md`.

## CLI surface

```
proofspan version
proofspan migrate --from=langsmith|honeyhive [--dry-run] [--format=json] [--db=DB] FILE
proofspan eval [--db=DB] [--trajectory=ID] [--registry=DIR] [--judges=FILE] [--assertions=id@v,...]
proofspan serve [--db=DB] [--addr=127.0.0.1:7400] [--scim]
```

## Repo map

| Path | What |
|---|---|
| `cmd/proofspan/` | the binary |
| `cmd/corpusgen/` | deterministic 10k-step corpus generator (3 formats) |
| `cmd/fidelitygen/` | converter-vs-native comparison + CI gate |
| `internal/schema/` | ATF v0.1.1 types |
| `internal/store/` | SQLite persistence |
| `internal/migrate/` | langsmith + honeyhive converters |
| `internal/fidelity/` | parity / MAD / cost math |
| `internal/eval/` | pinned-assertion runner |
| `internal/assert/wasm/` | WASM runtime (wazero) + version-pinned registry |
| `internal/judges/` | judge manifest validation |
| `internal/serve/` | local HTTP server |
| `internal/scim/` | SCIM 2.0 minimal provider (Users/Groups) |
| `registry/` | WASM assertion modules (span-correlation@1.0.0) |
| `judges/manifest.json` | pinned judge registry |
| `testdata/corpus/` | shared 10k-step corpus (generated, deterministic) |
| `docs/` | fidelity, pricing, migration, replacement-cost |
| `.dagger/` | Dagger CI module (`dagger call eval`) |

## Docs
- `docs/migrate.md` — converter usage and field coverage
- `docs/pricing.md` — public pricing, no contact-sales theater
- `docs/replacement-cost.md` — the 3x-savings worksheet
- `docs/cold-start.md` — measured deploy time
- `GOVERNANCE.md` — license split and change rules

## License

- Core: **MIT** — see LICENSE
- Cloud tier (when it exists): BSL 1.1, Change Date 2030-09-27, Change License MIT — see LICENSE-CLOUD
- DCO-only contributions, no CLA, license changes need 4/4 maintainers + 30-day notice + migration path — GOVERNANCE.md

## What this is not

No agent orchestration, no model serving, no playground, no "OS for AI agents", no third converter. Out of scope until 10 invoiced Cloud Pro teams (AGENTS.md). Fastest death for this product is platform scope before converter fidelity.
