import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";
import fs from "node:fs/promises";

const sourcePath = "/Users/baiyuxin/Documents/脚本/outputs/customer-product-rule-cases-20260825/客户商品自动化规则测试用例.xlsx";
const input = await FileBlob.load(sourcePath);
const workbook = await SpreadsheetFile.importXlsx(input);

const dictionary = workbook.worksheets.getItem("运算符字典");
const dictionaryValues = dictionary.getUsedRange().values;
console.log("Dictionary used range:", dictionary.getUsedRange().address, dictionaryValues.length, dictionaryValues[0]?.length);
console.log("--- BIZ_OBJECT_ORDER dictionary rows ---");
for (let index = 0; index < dictionaryValues.length; index += 1) {
  const row = dictionaryValues[index];
  if (row.some((value) => String(value).includes("BIZ_OBJECT_ORDER"))) {
    console.log(index + 1, JSON.stringify(row));
  }
}

const preview = await workbook.render({
  sheetName: "运算符字典",
  autoCrop: "all",
  scale: 1.5,
  format: "png",
});
await fs.mkdir("/Users/baiyuxin/Documents/查查查/outputs/customer-product-rule-cases-20260825-marked", { recursive: true });
await fs.writeFile(
  "/Users/baiyuxin/Documents/查查查/outputs/customer-product-rule-cases-20260825-marked/运算符字典预览.png",
  new Uint8Array(await preview.arrayBuffer()),
);

const cases = workbook.worksheets.getItem("单条件用例");
const caseValues = cases.getRange("A1:S90").values;
console.log("--- Existing case headers and final rows ---");
for (const index of [0, 1, 72, 73, 74, 75, 76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86, 87, 88, 89]) {
  console.log(index + 1, JSON.stringify(caseValues[index]));
}

for (const sheetId of ["使用说明", "保存接口模板"]) {
  const sheet = workbook.worksheets.getItem(sheetId);
  console.log(`--- ${sheetId} ---`);
  console.log(JSON.stringify(sheet.getUsedRange().values));
}
