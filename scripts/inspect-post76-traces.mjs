import fs from "node:fs/promises";
import path from "node:path";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const inputPath = process.argv[2];
const outputDir = process.argv[3] ?? "outputs/post76-trace-inspect";

if (!inputPath) {
  throw new Error("Usage: node inspect-post76-traces.mjs <input.xlsx> [output-dir]");
}

const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(inputPath));
const sheet = workbook.worksheets.getItem("单条件用例");
const used = sheet.getUsedRange(true);
const usedValues = used.values;
const lastRow = usedValues.length;

if (lastRow < 76) {
  console.log(JSON.stringify({ sheet: sheet.name, lastRow, traces: [] }, null, 2));
  process.exit(0);
}

const values = sheet.getRange(`A76:S${lastRow}`).values;
const traces = values.flatMap((row, index) => {
  const trace = String(row[17] ?? "").trim();
  if (!/^[a-fA-F0-9]{32}$/.test(trace)) return [];
  return [{
    row: index + 76,
    caseId: row[0] ?? "",
    caseName: row[1] ?? "",
    status: row[14] ?? "",
    traceId: trace,
    executedAt: row[18] ?? "",
  }];
});

await fs.mkdir(outputDir, { recursive: true });
const preview = await workbook.render({
  sheetName: sheet.name,
  range: `A${Math.max(76, lastRow - 24)}:S${lastRow}`,
  scale: 0.8,
  format: "png",
});
await fs.writeFile(path.join(outputDir, "before.png"), new Uint8Array(await preview.arrayBuffer()));

const style = await workbook.inspect({
  kind: "computedStyle",
  sheetId: sheet.name,
  range: `A76:S${Math.min(lastRow, 78)}`,
  maxChars: 5000,
});

console.log(JSON.stringify({ sheet: sheet.name, lastRow, traces }, null, 2));
console.log(style.ndjson);
