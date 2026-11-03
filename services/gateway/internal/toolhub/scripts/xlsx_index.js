// Text-only workbook extraction for the document index: one level-1 heading
// per sheet, then one unit per populated row written as "header: value"
// pairs from the sheet's first populated row, so a column name matches the
// rows that carry it.
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.error("XLSX index extractor requires exceljs");
  process.exit(1);
}

const MAX_CELL_CHARS = 400;

const cellText = (cell) => {
  let text = "";
  try {
    text = cell.text;
  } catch (error) {
    text = cell.value == null ? "" : String(cell.value);
  }
  return String(text ?? "").replace(/\s+/g, " ").trim().slice(0, MAX_CELL_CHARS);
};

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const units = [];
    workbook.eachSheet((sheet) => {
      units.push({ text: `Sheet: ${sheet.name}`, heading_level: 1, block: `workbook.sheet[${sheet.name}]` });
      let header = null;
      sheet.eachRow({ includeEmpty: false }, (row, rowNumber) => {
        const values = [];
        row.eachCell({ includeEmpty: true }, (cell, column) => { values[column] = cellText(cell); });
        if (!values.some(value => value)) return;
        if (header === null) {
          header = values;
          units.push({ text: values.filter(Boolean).join("\t"), block: `workbook.sheet[${sheet.name}].row[${rowNumber}]` });
          return;
        }
        const pairs = [];
        values.forEach((value, column) => {
          if (!value) return;
          const name = header[column];
          pairs.push(name ? `${name}: ${value}` : value);
        });
        units.push({ text: pairs.join(" | "), block: `workbook.sheet[${sheet.name}].row[${rowNumber}]` });
      });
    });
    process.stdout.write(JSON.stringify({ units }));
  } catch (error) {
    console.error(String(error && error.message || error));
    process.exit(1);
  }
});
