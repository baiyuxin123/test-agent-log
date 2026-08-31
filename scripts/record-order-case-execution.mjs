import fs from "node:fs/promises";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const sourcePath = "/Users/baiyuxin/Documents/脚本/outputs/customer-product-rule-cases-20260825/客户商品自动化规则测试用例.xlsx";
const outputDir = "/Users/baiyuxin/Documents/查查查/outputs/customer-product-rule-cases-20260825-executed";
const outputPath = `${outputDir}/客户商品自动化规则测试用例.xlsx`;
const input = await FileBlob.load(sourcePath);
const workbook = await SpreadsheetFile.importXlsx(input);
const sheet = workbook.worksheets.getItem("单条件用例");
const timestamp = new Intl.DateTimeFormat("sv-SE", {
  timeZone: "Asia/Shanghai",
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
}).format(new Date()).replace(",", "");

sheet.getRange("O76:S76").values = [["规则已保存（草稿，待渲染）", "", "", "", timestamp]];

const check = await workbook.inspect({
  kind: "table",
  sheetId: "单条件用例",
  range: "A76:S76",
  include: "values",
  tableMaxRows: 1,
  tableMaxCols: 19,
  maxChars: 3000,
});
console.log(check.ndjson);

await fs.mkdir(outputDir, { recursive: true });
const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);
console.log(outputPath);
