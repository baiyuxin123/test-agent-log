# MCP analysis tools (0.3.0)

The five existing tools retain their input/output contracts. This release adds five read-only tools and `logs://price-audit-rules`. The daily report script is unchanged. The v2/v3 directories are historical distribution snapshots; rebuild from `cmd/logs-mcp` for this version.

## Tools

| Tool | Required input | Result |
| --- | --- | --- |
| `sls_query_all` | `query` | Bounded, fully paginated raw search with explicit completeness, coverage and redaction |
| `order_price_history` | `order_sn` string | Creation quotation, first saved snapshot, changes, latest log snapshot, rule events and attribution gaps |
| `order_price_audit` | `order_sns` array of strings | Batch price-pattern classification, source-order records and unique-order summary |
| `order_context` | `order_sns` array of strings | Order/account fields with source timestamp, project, trace and explicit missing fields |
| `resolve_user_emails` | `user_ids` array of strings | Same-object ID/email evidence, conflicts, and source-order records |

Order tools accept **DianShi `order_sn`**, not an interchangeable DSers ID or Shopify display number. Context returns discovered DSers/seller IDs and order names. All IDs must be JSON strings to preserve 64-bit precision.

Business tools query both DSers and DianShi projects. `days` defaults to 30 for orders, 180 for email. Explicit `from_time` and `to_time` override days and must be supplied together. Windows are `[from_time,to_time)` Unix seconds, with a fixed cutoff for the whole batch. The latest observed log is not a live business-system readback.

Defaults: `concurrency: 5` (1–10), `max_requests: 200` (1–2000), `max_rows: 20000` (1–200000). Request/row budgets apply to each unique order, or each email group of at most 20 IDs, across both projects and enrichment queries. A batch accepts at most 1000 input rows. Duplicate inputs retain their positions but do not repeat the investigation. There is no persistent result cache or filesystem input dependency.

## Examples

Arguments passed to `tools/call`:

```json
{"name":"sls_query_all","arguments":{"project_alias":"dsers","query":"\"ORDER_ID\" and ORDER_CHANGE","minutes":1440,"max_requests":100}}
```

```json
{"name":"order_price_history","arguments":{"order_sn":"9007199254740993123","days":30,"max_requests":200}}
```

```json
{"name":"order_price_audit","arguments":{"order_sns":["9007199254740993123","9007199254740993123"],"days":30,"concurrency":10}}
```

```json
{"name":"order_context","arguments":{"order_sns":["9007199254740993123"],"fields":["order_name","status","agency_id","customer_name","created_at"]}}
```

```json
{"name":"resolve_user_emails","arguments":{"user_ids":["123","456"],"days":180,"concurrency":5}}
```

`order_context.fields` supports `order_name`, `dsers_order_id`, `seller_order_id`, `status`, `agency_id`, `dsers_user_id`, `shop_name`, `store_id`, `customer_name`, `created_at`, `creation_log_time`. `created_at` is the persisted `createdTime` value, retaining its source representation. `creation_log_time` is the distinct observed creation-response time. Unavailable values stay in `missing_fields`.

## Completeness and evidence

- Existing `sls_query.completed` is **only the SLS response processing flag**. A complete response containing 100 rows is not proof that all rows were fetched.
- `sls_query_all` retries failed/incomplete pages up to three times, splits incomplete time windows into non-overlapping halves, and paginates until the final short page. One-second incomplete windows remain partial.
- New tools expose `all_pages_fetched`, query coverage and `missing_evidence`. Exhausted budgets and failed queries never become a negative result. `sls_query_all.query_completed` is a conservative aggregate success flag for collection, not the legacy one-page flag.
- Collection completion describes the requested scope; it cannot prove that expired, never-emitted, or later-ingested logs exist. There is no separate SQL total-count reconciliation in this release.
- Raw collection preserves identical separate log occurrences; business analysis collapses identical retrieved events. Returned raw content is recursively credential-redacted. Unstructured content is omitted from the public raw collector; business tools return only selected evidence fields.
- JSON decoding preserves integer identifiers, including nested JSON strings. Email matches require an accepted ID and email field in the same object. Evidence for competing emails produces `conflict`; incomplete scope never produces `resolved` or a definitive absence.

## Price semantics

All monetary fields end in `_minor` and use integer minor units. Saved amounts must balance; missing amounts are never replaced with zero.

The current adapter supports the observed `CreateOrder`, `获取物流包裹参数`, `SaveOrderSnapshot`, `BatchUpdateOrderPrice` and `executeTaskSuccess` schemas. Creation attribution requires an exact returned order ID, a unique request associated by trace or exact DSers ID, and quotation trace/span plus item quantities, country/postcode and a single agency. Multi-agency and ambiguous quotations remain evidence gaps. The historical quotation schema uses USD minor units; non-USD records are not classified with this adapter.

Stages remain distinct:

1. Creation quotation (`initial_quote`).
2. First observed persisted snapshot (`first_saved`).
3. Persisted changes (`saved_price_changes`, including the first state).
4. Rule action before/after amounts (`rule_events`).
5. Latest observed persisted snapshot (`latest_log_snapshot`).

Explicit price-update requests are separately exposed in `price_update_requests` with requested amount, operator and response code. They are not treated as persistence proof. Price queries select operational events rather than broad order-list responses. Additional account-name queries run only for `order_context`.

Rule events expose whether their result matches the next observed saved snapshot, without claiming causation. A missing rule amount is `null`. Missing rule logs mean `not_found_in_scope`, never “the rule did not run.”

The audit predicate is fixed and explicit: **latest freight < initial quoted freight AND latest total < initial quoted total**. It returns:

- `matches`
- `historical_match_recovered`
- `not_observed`
- `insufficient_evidence`

These are price patterns, not root causes or rule-match results. A first-save difference is separate from a later saved-price change. Same-time conflicting snapshots, missing fields, unsupported currency and incomplete coverage prevent conclusive classification. Some historical zero-freight parcel logs omit the freight field: this implementation reports insufficient evidence rather than inheriting old scripts' implicit zero.

## Build and verification

```sh
go test ./cmd/logs-mcp
go test -race ./cmd/logs-mcp
go vet ./cmd/logs-mcp
go build -o bin/logs-mcp ./cmd/logs-mcp
python3 scripts/smoke-mcp-analysis.py
```

The ordinary tests use synthetic fixtures; `TestLiveKeyEndpointShape` is opt-in. Historical replay is also opt-in, reads private cache files outside source control and prints counts only:

```sh
MCP_REPLAY_CACHE=/absolute/path/to/cache \
MCP_REPLAY_REFERENCE=/absolute/path/to/audit.json \
go test ./cmd/logs-mcp -run TestHistoricalReplay -v
```

Use a newly started MCP process after building. Already-running clients must reconnect/restart their MCP connection to discover the five new tools. This release does not change external client configuration or overwrite historical v2/v3 packages. Excel export, formatting and external delivery remain caller responsibilities.
