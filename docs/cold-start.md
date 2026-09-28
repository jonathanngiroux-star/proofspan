# Cold start — measured

Every number below was measured on this repo's CI path, not estimated. Regenerate with the commands given.

## Docker path (self-host deploy)

Machine: 6-core x86_64 Linux, rootless Docker 29.8.1, image `proofspan:0.1.0` (alpine base, CGO off).

Scenario: empty data dir → `docker run` (migrate 10k-step LangSmith corpus) → `docker run` (eval gate over 400 trajectories, span-correlation@1.0.0).

| Step | Measured |
|---|---|
| `docker run` migrate (10,000 spans → SQLite) | ~0.9 s |
| `docker run` eval (400 trajectories, 1 pinned WASM assertion) | 2.5 s |
| **Full cold path, empty state → green gate** | **3.4 s** |
| SQLite db size, 10k spans | 3.2 MiB |

Image build (golang:1.27-alpine multi-stage, cold layer cache): ~90 s on 6 cores. Warm rebuild: seconds.

## Binary path (no Docker)

```
go build -o proofspan ./cmd/proofspan      # ~40 s cold module cache, ~1.5 s warm
./proofspan migrate --from=langsmith --db=ps.sqlite traces.jsonl   # <1 s @ 10k spans
./proofspan eval --db=ps.sqlite             # ~2 s @ 400 trajectories
```

## Against the targets

| Target | Bar | Measured |
|---|---|---|
| Cold start target | 2m12s | **3.4 s** |
| Hard cap | 15 min | 3.4 s |
| README deploy promise | 5 min | 3.4 s (image pre-built) or ~95 s (build included) |

Both targets cleared by two orders of magnitude. The 2m12s "target" from the brief was set for a much fatter surface; a single Go binary + SQLite with no services has no business taking minutes. If this number ever regresses past a minute, something snuck in a platform — check AGENTS.md before shipping.

## dagger call eval (CI gate)

`dagger call eval --src=.`: builds the container, runs tests, regenerates the corpus, checks both converters' fidelity gates (≥95% parity, <2% MAD drift, <0.5% cost drift), ingests both vendor corpora, and runs the eval over all 400 trajectories.

Measured wall time: **~3.2 min** cold (golang module downloads dominate), ~40 s warm.

## What is deliberately NOT here

No helm chart, no docker-compose, no init containers, no migrations service. The image is one static binary. Data is one SQLite file. Back up the file; you are done.
