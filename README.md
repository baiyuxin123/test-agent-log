# Aliyun SLS Query Tool

This workspace contains a small Codex-local tool for querying Alibaba Cloud Simple Log Service (SLS).

Defaults were extracted from the console URL:

- Project: `k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a`
- Logstore: `dsers-app`

## Setup

The Go MCP server does not require a local plaintext `.env` for SLS credentials. At runtime it calls this internal API and keeps the returned key material only in process memory:

```text
https://super-app-api-gw-test.dsers.com/dsers-logistics-mgmt-bff/main/aliyun-key
```

The result is cached in memory for one hour by default, so the API is not called before every SLS query.

Install dependencies:

```bash
python3 -m venv .venv
. .venv/bin/activate
pip install -r requirements.txt
```

For the Python helper script only, configure credentials and endpoint in `.env` or as environment variables:

```bash
ALIBABA_CLOUD_ACCESS_KEY_ID=your_access_key_id
ALIBABA_CLOUD_ACCESS_KEY_SECRET=your_access_key_secret
# Optional, only for temporary STS credentials:
# ALIBABA_CLOUD_SECURITY_TOKEN=your_security_token
ALIYUN_SLS_ENDPOINT=cn-zhangjiakou.log.aliyuncs.com
ALIYUN_SLS_PROJECT=k8s-log-cc338bd1e67fa4d8e9be4ad1e9435670a
ALIYUN_SLS_LOGSTORE=dsers-app
```

Use the Project overview page in the SLS console to find the exact endpoint. If this tool runs inside Alibaba Cloud in the same region, prefer the intranet endpoint.

## Query

Latest 20 rows from the last hour:

```bash
python sls_query.py --pretty
```

Search errors from the last 30 minutes:

```bash
python sls_query.py --minutes 30 -q 'error or exception' --reverse --pretty
```

Run SQL analysis:

```bash
python sls_query.py --minutes 60 -q '* | select count(*) as total limit 1' --pretty
```

For SQL statements, use `limit` and `order by` inside the query rather than relying on `--line`, `--offset`, or `--reverse`.

## Go MCP Server

This repository also contains a stdio MCP server implemented in Go:

- Entrypoint: `cmd/logs-mcp/main.go`
- MCP transport: stdio with `Content-Length` framed JSON-RPC messages and TRAE JSONL stdio framing
- SLS client: official `github.com/aliyun/aliyun-log-go-sdk`
- Credentials: fetched from the key API at runtime and cached in memory

Build:

```bash
go mod tidy
go build -o bin/logs-mcp ./cmd/logs-mcp
```

Run it:

```bash
./bin/logs-mcp
```

Example Codex MCP config:

```toml
[mcp_servers.dsers-logs]
command = "/Users/baiyuxin/Documents/查查查/bin/logs-mcp"
args = []
```

Optional runtime flags:

```bash
./bin/logs-mcp \
  --key-url https://super-app-api-gw-test.dsers.com/dsers-logistics-mgmt-bff/main/aliyun-key \
  --key-cache-ttl 1h
```

`.env` fallback is disabled by default. For local development only, explicitly enable it:

```bash
./bin/logs-mcp --allow-env-fallback --env-file /path/to/.env
```

Exposed tools:

- `sls_query`: SLS search/query tool with project aliases `dsers`, `cc338`, `dianshi`, `shopify`, `aofei`, `傲飞`, and `c195`. It detects raw, SQL/SPL analysis, and trace queries, adds `LIMIT 100` to unbounded analysis queries, and returns a query receipt with warnings.
- `sls_inspect_index`: reads and caches the LogStore field/full-text index metadata for 15 minutes. Use it before composing field queries or SQL analysis.
- `trace_timeline`: queries an exact trace ID across the DSers and DianShi projects in parallel, then returns a chronological event timeline and observed log-time duration.
- `order_log_plan`: returns the order investigation query plan and required reporting checklist.
- `product_image_change`: scans product service logs for image URL timeline and first use of a target image.

`sls_inspect_index` needs the SLS `log:GetIndex` permission in addition to log-query permission. If it is unavailable, keep using exact raw searches and the known order field mappings.

Useful MCP tool arguments:

```json
{
  "name": "product_image_change",
  "arguments": {
    "product_id": "8259001254082",
    "product_name": "Lymphatic Body Brush",
    "target_image": "nuvetra_dry_body_brush_redesign.png",
    "project_alias": "c195",
    "days": 45
  }
}
```

```json
{
  "name": "sls_query",
  "arguments": {
    "project_alias": "dsers",
    "query": "\"2061331482808811520\" \"supplier_order_status\"",
    "minutes": 10080,
    "reverse": true,
    "line": 50
  }
}
```

Inspect index fields before writing a field query:

```json
{
  "name": "sls_inspect_index",
  "arguments": {
    "project_alias": "dsers",
    "logstore": "dsers-app"
  }
}
```

Reconstruct a cross-project trace timeline:

```json
{
  "name": "trace_timeline",
  "arguments": {
    "trace_id": "c587193e8fe7e0618e6961f010038bf9",
    "minutes": 1440,
    "pages": 5
  }
}
```

The MCP server also exposes `logs://rules` and `logs://order-fields` resources so an MCP client can read the query norms before calling the tools.

## AoFei Notes

AoFei-only lookup conventions and verified query examples are recorded in [aofei-log-notes.md](aofei-log-notes.md).

## Analysis tools (0.3.0)

Five read-only tools now extend the original MCP: `sls_query_all`,
`order_price_history`, `order_price_audit`, `order_context`, and
`resolve_user_emails`. Read `logs://price-audit-rules` before interpreting price
results. See [arguments, examples, evidence limitations and verification](docs/mcp-analysis.md).

Rebuild `bin/logs-mcp` and restart the client MCP connection to load the new tools.
Historical v2/v3 bundles and the daily-report script are unchanged.
