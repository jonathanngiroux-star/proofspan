# Judge registry — Proofspan

`judges/manifest.json` (v1.1.0) is the judge registry. Every judge pins a `model_fingerprint`; validation and execution both enforce the pin.

## Rules (from GOVERNANCE + brief)

1. **Pinned, not named.** A model name alone is not a pin. The fingerprint is `<model>@sha256:<hash>`; the hash is computed from the provider's canonical model card (the exact `/v1/models/<id>` response body). Regenerate with:
   ```sh
   curl -s "https://integrate.api.nvidia.com/v1/models/z-ai/glm-5.3" -H "Authorization: Bearer $NVIDIA_API_KEY" \
     | python3 -c "import sys,hashlib; print('z-ai/glm-5.3@sha256:'+hashlib.sha256(sys.stdin.read().encode()).hexdigest())"
   ```
2. **UNPINNED refuses to execute.** A placeholder fingerprint is a hard error at judge-run time, never a silent grade.
3. **Served model must match the pin.** Before any HTTP judge call, the engine fetches the provider's model card and compares the served `id` against the pinned model segment. Mismatch = refusal, no grading happens.
4. **Builtin judges are deterministic.** `builtin:<id>@<v>` fingerprints run a reference implementation in-process — offline, CI-safe, no drift possible.

## Current registry

| Judge | Fingerprint | Kind |
|---|---|---|
| `factual-consistency` | `builtin:factual-consistency@v1` | deterministic: every number in an llm span's output must appear in some retrieval span's output |
| `semantic-consistency` | `z-ai/glm-5.3@sha256:b742dc1b96291719802100d3ffd6bfdbf29051a6cd607b8ab9c74c2adf5d576f` | live LLM judge, OpenAI-compatible endpoint (NVIDIA integrate), PASS/FAIL + reason |

## Running judges

```sh
export NVIDIA_API_KEY=nvapi-...   # from build.nvidia.com; required for HTTP judges only

# builtin only (offline):
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency

# builtin + live:
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency,semantic-consistency
```

Verdicts print to stdout and persist to the `eval_runs` table with the exact fingerprint used — audit-ready.

## Fail-closed semantics

- Unknown judge id → error, not skip.
- Fingerprint mismatch → error before any grading.
- UNPINNED → error.
- Judge FAIL verdict → the eval gate fails (nonzero exit).

A judge verdict is never a silent pass. LLM-as-judge drift is a Series A killer; the pin is how we prove which model graded which trajectory on which day.

## Adding a judge

1. Add the entry to `judges/manifest.json` with a **real** fingerprint (see rule 1).
2. For a builtin: extend `builtinJudge` in `internal/judges/engine.go` with the reference implementation + tests.
3. For an HTTP judge: nothing else — the generic OpenAI-compatible client handles it.
4. Bump the manifest `version`.
