import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import fs from "node:fs/promises";
import path from "node:path";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const workbookPath = process.argv[2];
const binary = process.argv[3] ?? "bin/logs-mcp";
if (!workbookPath) throw new Error("Missing workbook path");

const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(workbookPath));
const sheet = workbook.worksheets.getItem("单条件用例");
const lastRow = sheet.getUsedRange(true).values.length;
const startRow = Math.max(1, Number(process.argv[4] ?? 76));
const endRow = Math.min(lastRow, Number(process.argv[5] ?? lastRow));
const dumpRows = process.argv.includes("--dump");
const dumpFileArg = process.argv.find((arg) => arg.startsWith("--dump-file="));
const dumpFile = dumpFileArg ? dumpFileArg.slice("--dump-file=".length) : "";
const rows = sheet.getRange(`A${startRow}:S${endRow}`).values.flatMap((values, index) => {
  const rowNumber = index + startRow;
  const traceId = String(values[17] ?? "").trim().toLowerCase();
  if (!/^[a-f0-9]{32}$/.test(traceId)) return [];
  return [{ row: rowNumber, caseId: values[0], caseName: values[1], traceId, executedAt: values[18] }];
});

const parseLocalTime = (value) => Math.floor(new Date(String(value).replace(" ", "T") + "+08:00").getTime() / 1000);
const times = rows.map((row) => parseLocalTime(row.executedAt)).filter(Number.isFinite);
const fromTime = Math.min(...times) - 600;
const toTime = Math.max(...times) + 900;

const child = spawn(binary, ["--allow-env-fallback", "--env-file", ".env"], {
  cwd: process.cwd(),
  stdio: ["pipe", "pipe", "pipe"],
});
const pending = new Map();
const stderr = [];
child.stderr.on("data", (chunk) => stderr.push(String(chunk)));
createInterface({ input: child.stdout }).on("line", (line) => {
  if (!line.trim()) return;
  let message;
  try { message = JSON.parse(line); } catch { return; }
  if (message.id != null && pending.has(message.id)) {
    pending.get(message.id)(message);
    pending.delete(message.id);
  }
});

let nextId = 1;
const request = (method, params) => new Promise((resolve, reject) => {
  const id = nextId++;
  const timer = setTimeout(() => {
    pending.delete(id);
    reject(new Error(`MCP request timed out: ${method}`));
  }, 120000);
  pending.set(id, (message) => {
    clearTimeout(timer);
    if (message.error) reject(new Error(JSON.stringify(message.error)));
    else resolve(message.result);
  });
  child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id, method, params }) + "\n");
});

await request("initialize", {
  protocolVersion: "2025-03-26",
  capabilities: {},
  clientInfo: { name: "post76-error-check", version: "1.0.0" },
});
child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");

const matched = new Set();
const batches = [];
const logs = [];
for (let start = 0; start < rows.length; start += 15) {
  const batch = rows.slice(start, start + 15);
  const traceExpression = batch.map((row) => row.traceId).join(" or ");
  const query = `(${traceExpression}) not _container_name_:istio-proxy and merchant-automation-core and error`;
  const result = await request("tools/call", {
    name: "sls_query",
    arguments: {
      project: "k8s-log-cf17973a51cbc4a35bbabd741b391b626",
      logstore: "dsers-test",
      query,
      from_time: fromTime,
      to_time: toTime,
      reverse: false,
      line: 100,
      max_content_chars: dumpRows ? 0 : 2000,
    },
  });
  const text = (result.content ?? []).filter((item) => item.type === "text").map((item) => item.text).join("\n");
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    throw new Error(`SLS returned non-JSON content for rows ${batch[0].row}-${batch.at(-1).row}: ${text.slice(0, 500)}`);
  }
  const returnedRows = Array.isArray(payload.rows) ? payload.rows : [];
  if (dumpRows) logs.push(...returnedRows);
  const rowText = JSON.stringify(returnedRows).toLowerCase();
  for (const row of batch) {
    if (rowText.includes(row.traceId)) matched.add(row.traceId);
  }
  batches.push({
    firstRow: batch[0].row,
    lastRow: batch.at(-1).row,
    matchedTraceIds: batch.filter((row) => matched.has(row.traceId)).map((row) => row.traceId),
    resultCount: returnedRows.length,
    completed: payload.completed === true,
  });
}

child.stdin.end();
const matchedRows = rows.filter((row) => matched.has(row.traceId));
const output = { fromTime, toTime, traceCount: rows.length, batches, matchedRows, ...(dumpRows ? { logs } : {}) };
if (dumpFile) {
  await fs.mkdir(path.dirname(dumpFile), { recursive: true });
  await fs.writeFile(dumpFile, JSON.stringify(output, null, 2));
  console.log(JSON.stringify({ fromTime, toTime, traceCount: rows.length, batches, matchedRows, dumpFile, logCount: logs.length }, null, 2));
} else {
  console.log(JSON.stringify(output, null, 2));
}
if (stderr.length) console.error(stderr.join(""));
