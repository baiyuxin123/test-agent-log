import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import fs from "node:fs/promises";
import path from "node:path";

const options = Object.fromEntries(process.argv.slice(2).map((arg) => {
  const [key, ...rest] = arg.replace(/^--/, "").split("=");
  return [key, rest.join("=")];
}));

if (!options.query) throw new Error("--query is required");
const binary = options.binary || "bin/logs-mcp";
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
  const timer = setTimeout(() => reject(new Error(`MCP timeout: ${method}`)), 120000);
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
  clientInfo: { name: "generic-sls-query", version: "1.0.0" },
});
child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");

const args = {
  project: options.project,
  project_alias: options.projectAlias,
  logstore: options.logstore || "dsers-app",
  query: options.query,
  minutes: Number(options.minutes || 1440),
  reverse: options.reverse !== "false",
  line: Number(options.line || 100),
  max_content_chars: Number(options.maxContentChars || 4000),
};
if (options.fromTime) args.from_time = Number(options.fromTime);
if (options.toTime) args.to_time = Number(options.toTime);
for (const key of Object.keys(args)) if (args[key] === undefined) delete args[key];
const result = await request("tools/call", { name: "sls_query", arguments: args });
child.stdin.end();
const text = (result.content || []).filter((item) => item.type === "text").map((item) => item.text).join("\n");
if (options.output) {
  await fs.mkdir(path.dirname(options.output), { recursive: true });
  await fs.writeFile(options.output, text);
  const payload = JSON.parse(text);
  console.log(JSON.stringify({ output: options.output, completed: payload.completed, count: payload.count }, null, 2));
} else {
  console.log(text);
}
if (stderr.length) console.error(stderr.join(""));
