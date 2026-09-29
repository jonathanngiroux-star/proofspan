# Judge registry — Proofspan

`judges/manifest.json` (v1.2.0) is the judge registry. Every judge pins a `model_fingerprint`; validation and execution both enforce the pin. HTTP judges are **provider-agnostic**: you declare any OpenAI-compatible endpoint (NVIDIA, OpenAI, OpenRouter, a local vLLM/ollama) as a provider and reference it by name. The API key lives in an environment variable — never in the manifest, never in the repo.

## Manifest format

```json
{
  "namespace": "proofspan/llm-judge-registry",
  "version": "v1.2.0",
  "providers": {
    "nvidia": {"endpoint": "https://integrate.api.nvidia.com/v1", "api_key_env": "NVIDIA_API_KEY"},
    "local":  {"endpoint": "http://localhost:8000/v1"}
  },
  "judges": [
    {"id": "factual-consistency", "model_fingerprint": "builtin:factual-consistency@v1", "description": "deterministic reference judge"},
    {"id": "semantic-consistency", "model_fingerprint": "z-ai/glm-5.3@sha256:b742...", "provider": "nvidia"}
  ]
}
```

- **providers** — named endpoints. `api_key_env` names the env var holding the key; omit it for no-auth endpoints (local vLLM, ollama, LM Studio).
- **judges** — each references a provider by name. Builtin judges (`builtin:` fingerprint) need no provider.
- Validation is strict: an HTTP judge without a provider, a reference to an undeclared provider, or a provider without an endpoint all fail at `Load` time — before anything runs.

## Rules (from GOVERNANCE + brief)

1. **Pinned, not named.** A model name alone is not a pin. The fingerprint is `<model>@sha256:<hash>`; the hash is computed from the provider's canonical model card (the exact `/v1/models/<id>` response body). Regenerate with:
   ```sh
   curl -s "https://integrate.api.nvidia.com/v1/models/z-ai/glm-5.3" -H "Authorization: Bearer $NVIDIA_API_KEY" \
     | python3 -c "import sys,hashlib; print('z-ai/glm-5.3@sha256:'+hashlib.sha256(sys.stdin.read().encode()).hexdigest())"
   ```
2. **UNPINNED refuses to execute.** A placeholder fingerprint is a hard error at judge-run time, never a silent grade.
3. **Served model must match the pin.** Before any HTTP judge call, the engine fetches the provider's model card and compares the served `id` against the pinned model segment. Mismatch = refusal, no grading happens.
4. **Builtin judges are deterministic.** `builtin:<id>@<v>` fingerprints run a reference implementation in-process — offline, CI-safe, no drift possible.
5. **Keys come from the environment.** The manifest stores only the env var *name*. One-off overrides: `--judge-api-key` / `--judge-endpoint` on `proofspan eval`.

## Using your own provider

Any OpenAI-compatible endpoint works. Example — local vLLM:

```sh
# 1. start vllm: vllm serve meta-llama/Llama-3.1-8B-Instruct --port 8000
# 2. add to your manifest:
#    "local": {"endpoint": "http://localhost:8000/v1"}
#    and reference it: {"id": "my-judge", "model_fingerprint": "<served-model>@sha256:<hash>", "provider": "local"}
# 3. run — no API key needed:
proofspan eval --db=ps.sqlite --trajectory=trj_001 --judges-run=my-judge
```

Ad-hoc override without touching the manifest:

```sh
proofspan eval --db=ps.sqlite --judges-run=semantic-consistency \
  --judge-endpoint http://localhost:8000/v1 --judge-api-key sk-local-anything
```

## Running judges

```sh
# builtin only (offline, no key):
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency

# with the manifest-declared provider (key from the provider's api_key_env):
export NVIDIA_API_KEY=nvapi-...
./proofspan eval --db=ps.sqlite --trajectory=trj_00001 --judges-run=factual-consistency,semantic-consistency
```

Verdicts print to stdout and persist to the `eval_runs` table with the exact fingerprint used — audit-ready.

## Fail-closed semantics

- Unknown judge id → error, not skip.
- HTTP judge with no declared provider → `Load` error (manifest is invalid).
- Fingerprint mismatch → error before any grading.
- UNPINNED → error.
- Judge FAIL verdict → the eval gate fails (nonzero exit).

A judge verdict is never a silent pass. LLM-as-judge drift is a Series A killer; the pin is how we prove which model graded which trajectory on which day.

## Adding a judge

1. Add the entry to `judges/manifest.json` with a **real** fingerprint (see rule 1) and a `provider` reference (for HTTP judges).
2. For a builtin: extend `builtinJudge` in `internal/judges/engine.go` with the reference implementation + tests.
3. For an HTTP judge: nothing else — the generic OpenAI-compatible client handles any provider.
4. Bump the manifest `version`.

