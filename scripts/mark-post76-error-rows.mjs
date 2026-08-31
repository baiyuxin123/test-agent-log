import fs from "node:fs/promises";
import path from "node:path";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const inputPath = process.argv[2];
const outputPath = process.argv[3];
const matchedRows = [76, 77, 78, 79];

if (!inputPath || !outputPath) {
  throw new Error("Usage: node mark-post76-error-rows.mjs <input.xlsx> <output.xlsx>");
}

const workbook = await SpreadsheetFile.importXlsx(await FileBlob.load(inputPath));
const sheet = workbook.worksheets.getItem("单条件用例");

for (const row of matchedRows) {
  sheet.getRange(`A${row}:S${row}`).format.fill = "#F4CCCC";
}

await fs.mkdir(path.dirname(outputPath), { recursive: true });
const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);

const preview = await workbook.render({
  sheetName: sheet.name,
  range: "A74:S82",
  scale: 0.9,
  format: "png",
});
await fs.writeFile(path.join(path.dirname(outputPath), "after.png"), new Uint8Array(await preview.arrayBuffer()));

const styles = await workbook.inspect({
  kind: "computedStyle",
  sheetId: sheet.name,
  range: "A76:A80",
  maxChars: 5000,
});
const errors = await workbook.inspect({
  kind: "match",
  searchTerm: "#REF!|#DIV/0!|#VALUE!|#NAME\\?|#N/A",
  options: { useRegex: true, maxResults: 50 },
  summary: "formula error scan",
  maxChars: 5000,
});

console.log(JSON.stringify({ outputPath, matchedRows }, null, 2));
console.log(styles.ndjson);
console.log(errors.ndjson);
