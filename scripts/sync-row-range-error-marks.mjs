import fs from "node:fs/promises";
import path from "node:path";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const inputPath = process.argv[2];
const outputPath = process.argv[3];
const startRow = 1;
const endRow = 76;
const matchedRows = new Set([64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 76]);

if (!inputPath || !outputPath) throw new Error("Missing input or output path");

const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(inputPath));
const sheet = workbook.worksheets.getItem("单条件用例");
const styleResult = await workbook.inspect({
  kind: "computedStyle",
  sheetId: sheet.name,
  range: `A${startRow}:A${endRow}`,
  maxChars: 50000,
});

const oldRedRows = new Set();
for (const line of styleResult.ndjson.split("\n")) {
  if (!line.trim().startsWith("{")) continue;
  const item = JSON.parse(line);
  const row = Number(String(item.for ?? "").replace(/^A/, ""));
  const color = item.style?.fill?.color?.value;
  if (color === "F4CCCC" && row >= startRow && row <= endRow) oldRedRows.add(row);
}

const clearedRows = [...oldRedRows].filter((row) => !matchedRows.has(row));
for (const row of clearedRows) sheet.getRange(`A${row}:S${row}`).format.fill = "#FFFFFF";
for (const row of matchedRows) sheet.getRange(`A${row}:S${row}`).format.fill = "#F4CCCC";

await fs.mkdir(path.dirname(outputPath), { recursive: true });
const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);

const preview = await workbook.render({ sheetName: sheet.name, range: "A58:S76", scale: 0.8, format: "png" });
await fs.writeFile(path.join(path.dirname(outputPath), "after-1-76.png"), new Uint8Array(await preview.arrayBuffer()));
const verification = await workbook.inspect({
  kind: "computedStyle",
  sheetId: sheet.name,
  range: "A62:A76",
  maxChars: 12000,
});
const errors = await workbook.inspect({
  kind: "match",
  searchTerm: "#REF!|#DIV/0!|#VALUE!|#NAME\\?|#N/A",
  options: { useRegex: true, maxResults: 50 },
  maxChars: 5000,
});

console.log(JSON.stringify({ oldRedRows: [...oldRedRows], clearedRows, matchedRows: [...matchedRows] }, null, 2));
console.log(verification.ndjson);
console.log(errors.ndjson);
