# Pricing — Proofspan

Public pricing. No "contact sales" for 50–500 seats. Self-host core is free, forever, MIT.

| Tier | Price | Seats | Spans | Notes |
|---|---|---|---|---|
| **Self-host** | **$0** | unlimited | unlimited | MIT core: binary + SQLite, `docker run`, 5-minute deploy |
| **Cloud Starter** | **$500/mo** | 5 | 1M spans/mo | hosted, backups, updates |
| **Cloud Pro** | **$800/mo** | 10 | 5M spans/mo | VPC deploy, SSO/SCIM, audit log export, SLA 99.9%, SOC2 report |

Overages: $0.20 per additional 100k spans. Annual = 10x monthly (2 months free).

## Why pay when self-host is free?

You are buying operational risk transfer, not features:

- **3am pages become ours.** SLA 99.9% on Cloud Pro.
- **VPC deployment** inside your own cloud account (Cloud Pro) — data never leaves your boundary.
- **SSO/SCIM** — SCIM provisioning is in the self-host base too (MIT), hosted SSO is on us to operate.
- **Backups, restores, upgrades** — we hold the pager on the hosted control plane.
- **Audit log export + SOC2 report** for procurement.

Nothing in the eval runner, converters, assertions, or judge registry is ever gated behind cloud. If the hosted tier dies tomorrow, your self-host install is the full product.

## Buyer math

| | Incumbent (LangSmith/HoneyHive) | Proofspan Cloud Pro |
|---|---|---|
| Typical startup eng-lead spend | $500–$2,000/mo | $800/mo |
| Trace export + migration | manual, lossy | `proofspan migrate --dry-run` with fidelity report, >95% field parity |
| Self-host escape hatch | none (or enterprise $) | MIT core, `docker run`, SQLite |
| VPC option | enterprise tier only | Cloud Pro at list price |

## Replacement-cost worksheet

See `docs/replacement-cost.md` for the 3x-savings template procurement asks for.
