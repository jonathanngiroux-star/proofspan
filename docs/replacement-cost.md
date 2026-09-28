# Replacement-cost worksheet

The number procurement actually asks for: what you pay today vs. Proofspan vs. the 3x-savings story. Fill one row per team; totals at the bottom. Rule of thumb: if Proofspan Cloud Pro ($800/mo) is not ≥3x cheaper than your current line item including engineering time, do not buy — self-host the MIT core for $0.

## Line items to count on the incumbent side

| Item | Where to find it | Monthly $ |
|---|---|---|
| LangSmith / HoneyHive subscription | billing | $500–$2,000 |
| Overage charges (spans/tokens) | billing | varies |
| Engineering time: trace export + spreadsheet evals (4–6 h/wk × blended rate) | team survey | $2,400–$6,000 |
| Context switching / context loss from eval spreadsheeing | estimate 10% of above | — |

## Proofspan side

| Item | Monthly $ |
|---|---|
| Cloud Pro tier | $800 flat (5M spans included) |
| OR self-host | $0 license + your infra (~$20–50 on any 2-vCPU box) |
| Migration cost | one-time: `proofspan migrate --dry-run` (minutes, machine-readable diff) |

## The 3x test

```
incumbent_total ≥ 3 × (proofspan_total)   → switch, pilot one team first
incumbent_total <  3 × (proofspan_total)  → self-host MIT core, revisit at >5M spans/mo
```

Worked example (10-50 person startup, ~800k spans/mo):

- Incumbent: $1,100/mo subscription + ~$3,200/mo engineering time = **$4,300**
- Proofspan Cloud Pro: **$800** (5.4x cheaper)
- Proofspan self-host: **~$40 infra** (107x cheaper, you own the pager)

## Pilot protocol (design partners, read this)

1. `proofspan migrate --from=<langsmith|honeyhive> --dry-run --format=json <export>.jsonl` — review the field-parity diff.
2. Compare `docs/fidelity/<source>.md` parity numbers on YOUR corpus, not ours.
3. 30-day paid pilot at list price. $800. No discounts — discounted theater proves nothing.
4. Pilot success = your eval gate runs in your CI on your traces.
