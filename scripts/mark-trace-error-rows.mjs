import fs from "node:fs/promises";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const sourcePath = "/Users/baiyuxin/Documents/脚本/outputs/customer-product-rule-cases-20260825/客户商品自动化规则测试用例.xlsx";
const outputDir = "/Users/baiyuxin/Documents/查查查/outputs/customer-product-rule-cases-20260825-marked";
const outputPath = `${outputDir}/客户商品自动化规则测试用例.xlsx`;
const previewPath = `${outputDir}/单条件用例-标红预览.png`;
const sheetName = "单条件用例";
const errorRows = [27, 29, 30, 31, 34, 35, 36, 53, 54, 55, 56, 57, 58, 63, 64, 67, 68, 69, 70, 73, 74];

const input = await FileBlob.load(sourcePath);
const workbook = await SpreadsheetFile.importXlsx(input);
const sheet = workbook.worksheets.getItem(sheetName);

if (process.argv.includes("--inspect")) {
  const inspection = await workbook.inspect({
    kind: "region,computedStyle",
    sheetId: sheetName,
    range: "A25:S36",
    maxChars: 6000,
  });
  console.log(inspection.ndjson);
  const preview = await workbook.render({
    sheetName,
    range: "A25:S36",
    scale: 1.5,
    format: "png",
  });
  await fs.mkdir(outputDir, { recursive: true });
  await fs.writeFile(previewPath, new Uint8Array(await preview.arrayBuffer()));
  console.log(previewPath);
  process.exit(0);
}

for (const row of errorRows) {
  sheet.getRange(`A${row}:S${row}`).format.fill = "#F4CCCC";
}

const verification = await workbook.inspect({
  kind: "computedStyle",
  sheetId: sheetName,
  range: "A27:S27",
  maxChars: 2000,
});
console.log(verification.ndjson);

const preview = await workbook.render({
  sheetName,
  range: "A25:S36",
  scale: 1.5,
  format: "png",
});
await fs.mkdir(outputDir, { recursive: true });
await fs.writeFile(previewPath, new Uint8Array(await preview.arrayBuffer()));

const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);
console.log(outputPath);
