# Proofspan

**Category:** Agent evaluation harness (ATF / Harness pattern)
**Cohort:** Faster-to-cash *and* highest-upside if funded
**Window:** Sept 2026 – Mar 2028
**First repo if you only open one this month.**

## Pitch

A single-binary, CI-native evaluation harness that replays agent trajectories deterministically, converts LangSmith and HoneyHive traces at >95% field parity, and sells Cloud Pro at $800/month for 10 seats and 5M spans instead of $500–$2,000/month incumbents.

## Weekly job

Engineering leads at AI-agent startups (10–200 people) copy-paste LangSmith traces into spreadsheets, write custom eval scripts, and re-run them every sprint to catch prompt-chain regressions.

## Who pays

Startup engineering leads who own the “agent quality” line item.

## Why incumbents fail

LangSmith and HoneyHive lock traces in proprietary formats, charge per-seat premiums, and lack CI-native gates. Teams burn 4–6 hours/week on hand-rolled regression checks.

## Why this is still open (late 2026)

No open alternative ships all three: converter fidelity reports (>95% parity on a 10k-step corpus), framework-native CI emission (LangChain PR #14287 class), and a measured ~2m12s cold-start deploy.

## MVP (4–8 weeks)

- Go single binary + SQLite
- Line-delimited JSON trace schema (ATF v0.1.x)
- `proofspan migrate --from=langsmith --dry-run` (JSON field diff)
- HoneyHive converter on the same corpus
- `dagger call eval` CI gate
- WASM assertion registry (`span-correlation@v1.0.0`)
- Judge registry with pinned `model_fingerprint` (do not skip this)

## License

MIT/Apache core (schema, converters, runtime) + AGPL or BSL/ELv2 cloud tier. GOVERNANCE.md: no CLA surprise, 4/4 consensus + 30-day notice for license change.

## Pricing

- Cloud Starter — $500/mo (5 seats, 1M spans)
- Cloud Pro — $800/mo (10 seats, 5M spans, VPC, SLA, SOC2)
- Self-host — free core

## 90-day evidence

- Dual-converter fidelity: LangSmith ≥96.8%, HoneyHive ≥97.3% on a shared 10k-step corpus
- At least one major framework emitting ATF in *their* CI
- SCIM minimal provider in `v0.2.0` latest tag
- ≥3 invoiced teams on independent procurement paths (10+ Cloud Pro by Q1 2027)
- Star-to-paid via UTM on `dagger call eval` ≥0.8%
- Stars-to-contributors 1:50–80 at v1.0
- Public pricing page, no “contact sales” for 50–500 seats

## Living-income path

10 Cloud Pro teams × $800 = $8k MRR by month 12. ~3.2% self-host-to-cloud on a few thousand deploys is the analog from the monitoring benchmark.

## Investor “no”

Revenue concentration >80% from <3 design partners; missing judge registry; star-to-paid <0.5% by month 18; MIT-only cloud that a hyperscaler can strip.

## Fastest death

Scope creep into “agent orchestration platform” before the eval wedge has 10+ paying Cloud Pro teams. Maintainer burns 40% of cycles on integrations instead of converter fidelity.
