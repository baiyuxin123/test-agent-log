import fs from "node:fs/promises";
import { FileBlob, SpreadsheetFile } from "@oai/artifact-tool";

const sourcePath = "/Users/baiyuxin/Documents/脚本/outputs/customer-product-rule-cases-20260825/客户商品自动化规则测试用例.xlsx";
const outputDir = "/Users/baiyuxin/Documents/查查查/outputs/customer-product-rule-cases-20260825-order-cases";
const outputPath = `${outputDir}/客户商品自动化规则测试用例.xlsx`;
const previewPath = `${outputDir}/订单单条件用例预览.png`;
const sheetName = "单条件用例";
const startRow = 76;

const fields = [
  {
    id: "ORDER-STATUS",
    name: "订单状态",
    key: "order.status",
    type: "select",
    base: "WAIT_BUYER_PAY",
    operators: ["OPERATOR_EQ", "OPERATOR_IN", "OPERATOR_NEQ", "OPERATOR_NOT_IN"],
  },
  {
    id: "ORDER-COUNTRY",
    name: "目的国家",
    key: "order.country",
    type: "select",
    base: "US",
    operators: ["OPERATOR_EQ", "OPERATOR_IN", "OPERATOR_NEQ", "OPERATOR_NOT_IN"],
  },
  {
    id: "ORDER-AMOUNT",
    name: "订单金额",
    key: "order.amount",
    type: "number",
    base: 200,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_IS_EMPTY", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN", "OPERATOR_NOT_EMPTY"],
  },
  {
    id: "ORDER-PRODUCT-AMOUNT",
    name: "商品金额",
    key: "order.product_amount",
    type: "number",
    base: 200,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_IS_EMPTY", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN", "OPERATOR_NOT_EMPTY"],
  },
  {
    id: "ORDER-LOGISTICS-FEE",
    name: "物流费用",
    key: "order.logistics_fee",
    type: "number",
    base: 10,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_IS_EMPTY", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN", "OPERATOR_NOT_EMPTY"],
  },
  {
    id: "ORDER-ADJUST-AMOUNT",
    name: "调整金额",
    key: "order.adjust_amount",
    type: "number",
    base: 2,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_IS_EMPTY", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN", "OPERATOR_NOT_EMPTY"],
  },
  {
    id: "ORDER-TOTAL-WEIGHT",
    name: "订单商品总重量",
    key: "order.total_weight",
    type: "number",
    base: 1000,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_IS_EMPTY", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN", "OPERATOR_NOT_EMPTY"],
  },
  {
    id: "ORDER-PRODUCT-QUANTITY",
    name: "订单商品数量合计",
    key: "order.product_quantity",
    type: "number",
    base: 2,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN"],
  },
  {
    id: "ORDER-PRODUCT-VARIETY",
    name: "订单商品种类数合计",
    key: "order.product_variety",
    type: "number",
    base: 2,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN"],
  },
  {
    id: "ORDER-SKU-VARIETY",
    name: "订单SKU种类合计",
    key: "order.sku_variety",
    type: "number",
    base: 2,
    operators: ["OPERATOR_BETWEEN", "OPERATOR_EQ", "OPERATOR_GT", "OPERATOR_GTE", "OPERATOR_LT", "OPERATOR_LTE", "OPERATOR_NEQ", "OPERATOR_NOT_BETWEEN"],
  },
  {
    id: "ORDER-REMARK",
    name: "订单备注",
    key: "order.remark",
    type: "text",
    base: "test remark",
    operators: ["OPERATOR_CONTAINS", "OPERATOR_ENDS_WITH", "OPERATOR_EQ", "OPERATOR_IS_EMPTY", "OPERATOR_NEQ", "OPERATOR_NOT_CONTAINS", "OPERATOR_NOT_EMPTY", "OPERATOR_STARTS_WITH"],
  },
];

const operatorNames = {
  OPERATOR_BETWEEN: "介于",
  OPERATOR_CONTAINS: "包含",
  OPERATOR_ENDS_WITH: "结尾是",
  OPERATOR_EQ: "等于",
  OPERATOR_GT: "大于",
  OPERATOR_GTE: "大于等于",
  OPERATOR_IN: "属于",
  OPERATOR_IS_EMPTY: "为空",
  OPERATOR_LT: "小于",
  OPERATOR_LTE: "小于等于",
  OPERATOR_NEQ: "不等于",
  OPERATOR_NOT_BETWEEN: "不介于",
  OPERATOR_NOT_CONTAINS: "不包含",
  OPERATOR_NOT_EMPTY: "不为空",
  OPERATOR_NOT_IN: "不属于",
  OPERATOR_STARTS_WITH: "开头是",
};

function conditionValue(field, operator) {
  if (operator === "OPERATOR_IS_EMPTY" || operator === "OPERATOR_NOT_EMPTY") {
    return { display: "无需输入", value: {} };
  }

  if (operator === "OPERATOR_IN") {
    return { display: `[${field.base}]`, value: { value: [String(field.base)] } };
  }

  if (operator === "OPERATOR_NOT_IN") {
    return { display: `[NOT_${field.base}]`, value: { value: [`NOT_${field.base}`] } };
  }

  if (operator === "OPERATOR_BETWEEN") {
    return { display: `${field.base - 1}~${field.base + 1}`, value: { min: String(field.base - 1), max: String(field.base + 1) } };
  }

  if (operator === "OPERATOR_NOT_BETWEEN") {
    return { display: `${field.base + 1}~${field.base + 2}`, value: { min: String(field.base + 1), max: String(field.base + 2) } };
  }

  if (field.type === "number") {
    const numericValues = {
      OPERATOR_EQ: field.base,
      OPERATOR_GT: field.base - 1,
      OPERATOR_GTE: field.base,
      OPERATOR_LT: field.base + 1,
      OPERATOR_LTE: field.base,
      OPERATOR_NEQ: field.base + 1,
    };
    return { display: String(numericValues[operator]), value: { value: String(numericValues[operator]) } };
  }

  const textValues = {
    OPERATOR_CONTAINS: field.type === "text" ? "test" : field.base,
    OPERATOR_ENDS_WITH: field.type === "text" ? "remark" : field.base,
    OPERATOR_EQ: field.base,
    OPERATOR_NEQ: `NOT_${field.base}`,
    OPERATOR_NOT_CONTAINS: field.type === "text" ? "NOT_FOUND" : `NOT_${field.base}`,
    OPERATOR_STARTS_WITH: field.type === "text" ? "test" : field.base,
  };
  return { display: textValues[operator], value: { value: String(textValues[operator]) } };
}

const rows = [];
for (const field of fields) {
  field.operators.forEach((operator, index) => {
    const value = conditionValue(field, operator);
    const caseId = `${field.id}-${String(index + 1).padStart(2, "0")}`;
    const condition = {
      bizObject: "BIZ_OBJECT_ORDER",
      fieldKey: field.key,
      operatorKey: operator,
      fieldValue: value.value,
    };
    const caseName = `${field.name}-${operatorNames[operator]}-待验证`;
    rows.push([
      caseId,
      caseName,
      field.name,
      field.key,
      field.type,
      operator,
      operatorNames[operator],
      value.display,
      JSON.stringify(condition),
      "待验证",
      "订单调整金额增加2",
      "基准测试订单数据需在执行前确认",
      "待配置订单测试数据",
      `${caseId}-${caseName}`,
      "未执行",
      "",
      "",
      "",
      "",
    ]);
  });
}

const input = await FileBlob.load(sourcePath);
const workbook = await SpreadsheetFile.importXlsx(input);
const sheet = workbook.worksheets.getItem(sheetName);
const endRow = startRow + rows.length - 1;

for (let row = startRow; row <= endRow; row += 1) {
  const styleSource = row % 2 === 0 ? "A72:S72" : "A73:S73";
  sheet.getRange(`A${row}:S${row}`).copyFrom(sheet.getRange(styleSource), "all");
}
sheet.getRange(`A${startRow}:S${endRow}`).values = rows;
for (let row = startRow; row <= endRow; row += 1) {
  const range = sheet.getRange(`A${row}:S${row}`);
  range.format.fill = row % 2 === 0 ? "#BFE3F3" : "#FFFFFF";
  range.format.font = { fontSize: 10, typeface: "Carlito", color: "#1F2937" };
  range.format.borders = {
    top: { style: "thin", color: "#D9E2F3" },
    bottom: { style: "thin", color: "#D9E2F3" },
  };
  range.format.wrapText = true;
}

const verification = await workbook.inspect({
  kind: "table,computedStyle",
  sheetId: sheetName,
  range: `A${startRow}:S${startRow + 8}`,
  include: "values,formulas",
  tableMaxRows: 9,
  tableMaxCols: 19,
  maxChars: 7000,
});
console.log(verification.ndjson);

const preview = await workbook.render({
  sheetName,
  range: `A${startRow - 2}:S${startRow + 8}`,
  scale: 1.5,
  format: "png",
});
await fs.mkdir(outputDir, { recursive: true });
await fs.writeFile(previewPath, new Uint8Array(await preview.arrayBuffer()));

const output = await SpreadsheetFile.exportXlsx(workbook);
await output.save(outputPath);
console.log(outputPath);
