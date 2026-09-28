# AGENTS.md — Proofspan build rules

Wedge only: CI-native agent evaluation harness replacing LangSmith / HoneyHive for startup eng leads ($500–$2,000/mo substitute).

## Rules
1. Converters (LangSmith, HoneyHive) at >95% field parity beat any new feature.
2. Single Go binary + SQLite. No multi-service anything. Cold start target 2m12s, hard cap 15 min.
3. Judge registry with pinned `model_fingerprint` is required from commit 1.
4. Migration CLI with dry-run JSON diffs is the product.
5. Cloud tier sells ops risk (VPC, SSO/SCIM, audit, SLA, SOC2). Never features ripped from self-host core.
6. License: MIT/Apache on schema/converters/runtime; AGPL or BSL/ELv2 on cloud. DCO-only, no CLA.

## Do not build (until 10 invoiced Cloud Pro teams)
- Agent orchestration / multi-agent OS
- Model serving, playground, Open-WebUI-for-agents
- >2 framework converters (LangSmith + HoneyHive only)
- Browser automation
- Generic observability platform
- Feature-gated eval runner

## Every PR answers
Does this help converter fidelity, CI gate, judge pin, SCIM, or deploy time? If no → don't build.

## Definition of done v0.1
- One binary, SQLite, documented `docker run`
- Cold start measured and published
- LangSmith dry-run migrate + fidelity markdown
- `dagger call eval` green on sample trajectory
- Judge manifest validated
- GOVERNANCE.md exists
- No orchestration, no second product
