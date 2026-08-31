# DSers Logs MCP Package

This package bundles the current DSers/DianShi/Shopify log investigation capability as a local MCP server, plus the accumulated `logs` skill and reference knowledge.

## What's Included

- `bin/logs-mcp`: macOS arm64 MCP executable.
- `run-dsers-logs-mcp`: launcher for TRAE/Codex MCP clients.
- `mcp.template.json`: config template for other computers.
- `generate-mcp-config.sh`: creates an importable MCP config using the current folder path.
- `src/`: Go source and tests for rebuilding.
- `.trae/skills/logs/`: TRAE skill package.
- `.codex/skills/logs/`: Codex skill package.
- `knowledge/`: human-readable query rules and field references.

## Credential Flow

Normal use does not require a plaintext env file.

The MCP server calls this internal API at runtime when it first needs to query SLS:

```text
https://super-app-api-gw-test.dsers.com/dsers-logistics-mgmt-bff/main/aliyun-key
```

The returned SLS credentials are kept only in process memory and cached for one hour by default. The package does not write keys, tokens, or secrets to disk.

Optional overrides:

```bash
ALIYUN_KEY_URL=https://example/internal/aliyun-key ALIYUN_KEY_CACHE_TTL=30m ./run-dsers-logs-mcp
```

## Import Into TRAE

1. Copy this whole folder to the target computer.
2. Run `./generate-mcp-config.sh` from inside this folder.
3. Import the generated `mcp.generated.json` into TRAE MCP.

`mcp.template.json` is also provided if you prefer to edit the command path manually.

## Install Skill

For TRAE, copy or import:

```text
.trae/skills/logs
```

For Codex, copy:

```text
.codex/skills/logs
```

to the target machine's Codex skills directory.

The skill contains:

- Query routing rules for DSers, DianShi/点石, and Shopify logs.
- Required order-answer checklist.
- Status, platform, supply-source, and supplier-app interpretation rules.
- Field reference at `references/order-log-fields.md`.

## MCP Tools

- `sls_query`: run a narrow Alibaba Cloud SLS query.
- `order_log_plan`: return the investigation query plan and answer checklist for an order identifier.
- `product_image_change`: find product image URL timeline and first use of a target image.

MCP resources:

- `logs://rules`
- `logs://order-fields`

## Rebuild

Install Go, then run from this folder:

```bash
go build -o bin/logs-mcp ./src/cmd/logs-mcp
```

Run tests:

```bash
go test ./src/cmd/logs-mcp
```
