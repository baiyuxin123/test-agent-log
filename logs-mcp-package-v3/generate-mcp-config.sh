#!/bin/sh
set -eu

DIR=$(cd "$(dirname "$0")" && pwd)
OUT="$DIR/mcp.generated.json"

cat > "$OUT" <<EOF
{
  "mcpServers": {
    "dsers-logs": {
      "command": "$DIR/run-dsers-logs-mcp",
      "args": []
    }
  }
}
EOF

printf '%s\n' "$OUT"
