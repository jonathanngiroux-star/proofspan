# Proofspan

Proofspan is a self-hosted agent evaluation harness: it ingests existing LLM-agent traces, checks them against versioned assertions, and turns the result into a CI gate. It ships as a single Go binary with SQLite storage and no external services.

It currently imports traces from **LangSmith** and **HoneyHive** exports, converts them into a neutral trace format (ATF v0.1.1), and runs assertions from a WASM registry — plus optional LLM judges whose models are cryptographically pinned in a manifest.

```
$ proofspan version
proofspan 0.1.1
schema atf/v0.1.1
```

## Requirements

- Go 1.27+ (build only; the binary is static, CGO disabled)
- No runtime dependencies — SQLite is embedded (pure-Go driver)

## Build

```sh
git clone https://github.com/jonathanngiroux-star/proofspan.git
cd proofspan
go build -o proofspan ./cmd/proofspan
```

Docker:

```sh
docker build -t proofspan .
```

## Quickstart

```sh
# 1. Analyze a trace export before touching anything (read-only):
./proofspan report --from=langsmith traces.jsonl

# 2. Preview the migration — machine-readable JSON, no writes:
./proofspan migrate --from=langsmith --dry-run --format=json traces.jsonl

# 3. Ingest into SQLite:
./proofspan migrate --from=langsmith --db=proofspan.sqlite traces.jsonl
# migrated 400 trajectories, 10000 spans into proofspan.sqlite

# 4. Run the eval gate over everything ingested:
./proofspan eval --db=proofspan.sqlite
# eval: 400/400 trajectories pass   (exit 0; any failure exits 1)
```

## Commands

### `proofspan report --from=SOURCE [--out=DIR] FILE`

Read-only pre-migration analysis of an export. Reports how many lines parse, which converter-mapped fields the export actually uses, and a census of keys the converter does not map yet (nothing is dropped silently — this is the list).

JSON goes to stdout (pipeable); with `--out=DIR` a markdown report is also written.

### `proofspan migrate --from=SOURCE [--dry-run] [--format=json] [--db=DB] FILE`

`SOURCE` is `langsmith` or `honeyhive`; `FILE` is a line-delimited JSON export (one run/event object per line).

- `--dry-run`: prints a conversion plan — counts, the full field-mapping table, dropped-field notes, and one fully converted sample span. No writes.
- Without `--dry-run`: converts and ingests. Re-running on the same file is idempotent (trajectories are upserted, not duplicated).

Field mappings for both sources are documented in [docs/migrate.md](docs/migrate.md).

### `proofspan eval [--db=DB] [--trajectory=ID] [--assertions=a@v,...] [--judges-run=id,...]`

Runs versioned assertions over stored trajectories:

- **WASM assertions** come from `registry/` (compiled to `registry/bin/<id>@<version>.wasm`). The first is `span-correlation@1.0.0`, which verifies parent/child integrity: every `parent_span_id` must resolve, and children must not start before their parent ends.
- **LLM judges** (optional, `--judges-run`) execute after assertions. Providers are user-declared in [judges/manifest.json](judges/manifest.json) — any OpenAI-compatible endpoint works (NVIDIA, OpenAI, OpenRouter, local vLLM/ollama), with the API key read from an environment variable named in the manifest (`api_key_env`), never stored in the repo. Each judge is pinned to an exact model fingerprint; the engine verifies the served model matches the pin *before* grading, and refuses to run otherwise. Verdicts are printed and persisted to the `eval_runs` table.

Exit code is nonzero if any assertion or judge fails — designed to be a CI step.

```sh
# deterministic builtin judge only (offline, no API key):
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency

# with a live LLM judge (key comes from the provider's api_key_env):
export NVIDIA_API_KEY=nvapi-...
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 \
  --judges-run=factual-consistency,semantic-consistency

# ad-hoc endpoint/key override (e.g. local vLLM), no manifest edit:
./proofspan eval --db=ps.sqlite --judges-run=semantic-consistency \
  --judge-endpoint http://localhost:8000/v1
```

See [docs/judges.md](docs/judges.md) for provider declaration, fingerprint rules, and the current registry.

### `proofspan serve [--db=DB] [--addr=127.0.0.1:7400] [--scim]`

Local HTTP server: `GET /healthz`, `GET /v1/trajectories`, `GET /v1/trajectories/{id}`, and (with `--scim`) a minimal SCIM 2.0 provider (`/scim/v2/Users`, `/scim/v2/Groups`, `/scim/v2/ServiceProviderConfig`). SCIM users and groups persist to the same SQLite file.

**Security model:**

- The server speaks plain HTTP. It binds `127.0.0.1` by default and is intended for localhost use or behind a reverse proxy (nginx, Caddy, Traefik) that terminates TLS. Do not expose the port directly to a network without a TLS-terminating proxy in front.
- SCIM endpoints enforce bearer auth when `PROOFSPAN_SCIM_TOKEN` is set: requests without `Authorization: Bearer <token>` get `401` with the SCIM error schema. `/scim/v2/ServiceProviderConfig` stays public per RFC 7643 (capability discovery; it exposes no user data).
- Without `PROOFSPAN_SCIM_TOKEN` SCIM is open — dev mode; acceptable only on localhost.
- Token comparison is constant-time.

## Trace format (ATF v0.1.1)

Line-delimited JSON. One trajectory header, then spans:

```json
{"type":"trajectory","version":"atf/v0.1.1","trajectory_id":"trj_01","source":"native","started_at_unix_ms":1727452800000,"metadata":{}}
{"type":"span","span_id":"spn_01","trajectory_id":"trj_01","name":"search_docs","kind":"tool","started_at_unix_ms":1727452800100,"ended_at_unix_ms":1727452800450,"input":"{...}","output":"{...}"}
```

Span kinds: `llm`, `tool`, `retrieval`, `custom`. Full type definitions in [`internal/schema/types.go`](internal/schema/types.go); a complete sample in [`testdata/corpus/example.jsonl`](testdata/corpus/example.jsonl).

## Converter fidelity

Both converters are measured against a shared 10,000-span corpus generated deterministically in all three formats (native ATF + simulated vendor exports), and the check runs in CI ([.github/workflows/ci.yml](.github/workflows/ci.yml)):

| Converter | Field parity | MAD token drift | Cost drift |
|---|---|---|---|
| LangSmith | 100.00% | 0.0000 | 0.00000 |
| HoneyHive | 100.00% | 0.0000 | 0.00000 |

CI fails below 95% field parity, 2% MAD drift, or 0.5% cost drift. Full reports: [docs/fidelity/langsmith.md](docs/fidelity/langsmith.md), [docs/fidelity/honeyhive.md](docs/fidelity/honeyhive.md).

Reproduce locally:

```sh
go run ./cmd/corpusgen testdata/corpus     # regenerate the corpus (seed 42, deterministic)
go run ./cmd/fidelitygen testdata/corpus docs/fidelity
```

## CI

`dagger call eval` runs the full gate in one container: unit tests → corpus generation → fidelity checks → binary build → both migrations → eval over all trajectories. The same sequence runs in GitHub Actions via the `dagger-for-github` action.

## Repository layout

```
cmd/proofspan/           CLI entry point
cmd/corpusgen/           deterministic test-corpus generator (3 formats)
cmd/fidelitygen/         converter-vs-native fidelity comparison (CI gate)
cmd/judgerun/            judge execution harness (dev tool)
internal/schema/         ATF v0.1.1 types
internal/store/          SQLite persistence (trajectories, spans, eval runs)
internal/migrate/        langsmith + honeyhive converters
internal/fidelity/       parity / MAD / cost-drift math
internal/eval/           assertion runner
internal/assert/wasm/   WASM runtime (wazero) + version-pinned assertion registry
internal/judges/         judge manifest validation + execution engine
internal/corpus/         export analysis (report command)
internal/serve/          local HTTP server
internal/scim/           SCIM 2.0 provider (store-backed)
registry/                WASM assertion sources (span-correlation@1.0.0)
judges/manifest.json     pinned judge registry
testdata/corpus/         shared 10k-span corpus (generated)
docs/                    fidelity, corpus, judges, pricing, deployment docs
.dagger/                 Dagger CI module
```

## Documentation

- [docs/migrate.md](docs/migrate.md) — converter usage and complete field-mapping tables
- [docs/judges.md](docs/judges.md) — judge registry, fingerprint pinning, execution rules
- [docs/cold-start.md](docs/cold-start.md) — measured deployment times
- [docs/pricing.md](docs/pricing.md) — hosted tiers (self-host core is free, MIT)
- [docs/replacement-cost.md](docs/replacement-cost.md) — cost-comparison worksheet
- [AGENTS.md](AGENTS.md) — build rules and scope for contributions
- [GOVERNANCE.md](GOVERNANCE.md) — license policy and change process

## Contributing

DCO-only (`git commit -s`), no CLA. See [GOVERNANCE.md](GOVERNANCE.md).

Every change should answer: does this improve converter fidelity, the CI gate, judge pinning, SCIM, or deployment time? If not, it is probably out of scope — see AGENTS.md.

## Support the project

If Proofspan saves you the eval-spreadsheet work, you can support development:

- **Ethereum / USDC (ERC-20)**: `0x85ee7E71f762d772599cbF1EC20E651B30657521`
- **Bitcoin**: `bc1qxe2zx5tv3hdreaej6s2x4p7han85uey828rrhg`

## License

- Core (everything in this repository): **MIT** — [LICENSE](LICENSE)
- Future cloud tier: Business Source License 1.1 (Change Date 2030-09-27, converts to MIT) — [LICENSE-CLOUD](LICENSE-CLOUD)
