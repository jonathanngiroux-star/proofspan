# Corpus report: langsmith export

File: `testdata/corpus/langsmith/corpus.jsonl`

| Metric | Value |
|---|---|
| Parsed lines | 10000 of 10000 |
| Parse errors | 0 |
| Trajectories (sessions) | 400 |
| Analysis time | 556 ms |

## Spans by run type

| Type | Count |
|---|---|
| chain | 997 |
| llm | 4004 |
| retriever | 1969 |
| tool | 3030 |

## Known-field coverage

| Field | Spans carrying it |
|---|---|
| dotted_order | 10000 |
| end_time | 10000 |
| error | 198 |
| execution_order | 10000 |
| extra.invocation_params | 0 |
| extra.metadata | 10000 |
| extra.model_name | 4004 |
| extra.token_usage | 4004 |
| extra.token_usage.completion_tokens | 0 |
| extra.token_usage.prompt_tokens | 0 |
| extra.total_cost | 4004 |
| id | 10000 |
| inputs | 10000 |
| name | 10000 |
| outputs | 10000 |
| parent_run_id | 9600 |
| run_type | 10000 |
| session_id | 10000 |
| start_time | 10000 |
| tags | 10000 |
| trace_id | 0 |

## Unknown keys

None — every key in this export maps to an ATF field.
