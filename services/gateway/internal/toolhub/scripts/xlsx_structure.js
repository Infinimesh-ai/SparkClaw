
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX structure adapter requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const operation = String(req.operation || "");
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const sheetName = String(req.sheet || "").trim();
    if (!sheetName) throw new Error("sheet is required");
    const sheet = workbook.getWorksheet(sheetName);
    if (!sheet) throw new Error("sheet not found: " + sheetName);

    const result = {
      status: "xlsx_version_written",
      operation,
      path: req.path,
      output_path: req.output_path,
      sheet: sheetName
    };

    function positiveRow(value) {
      const row = Number(value || 0);
      if (!Number.isInteger(row) || row <= 0) throw new Error("row must be a positive 1-based integer");
      return row;
    }

    function existingRow(value) {
      const row = positiveRow(value);
      if (row > sheet.rowCount) throw new Error("row out of range: " + row);
      return row;
    }

    function valuesArray(value) {
      if (!Array.isArray(value)) throw new Error("values must be an array");
      return value;
    }

    function writeRow(rowNumber, values) {
      const row = sheet.getRow(rowNumber);
      row.values = [];
      values.forEach((value, index) => {
        row.getCell(index + 1).value = value;
      });
      row.commit();
    }

    function assertCell(address) {
      const cell = String(address || "").trim().toUpperCase();
      if (!/^[A-Z]+[1-9][0-9]*$/.test(cell)) throw new Error("cell must be a valid A1 address");
      return cell;
    }

    if (operation === "update_cell") {
      const cellAddress = assertCell(req.cell);
      sheet.getCell(cellAddress).value = req.value;
      result.cell = cellAddress;
      result.value = req.value;
    } else if (operation === "insert_row") {
      const row = existingRow(req.row);
      const position = String(req.position || "").trim().toLowerCase();
      if (position !== "before" && position !== "after") throw new Error("position must be before or after");
      const insertAt = position === "before" ? row : row + 1;
      const values = valuesArray(req.values);
      sheet.spliceRows(insertAt, 0, values);
      result.row = row;
      result.inserted_row = insertAt;
      result.values = values;
    } else if (operation === "delete_row") {
      const row = existingRow(req.row);
      sheet.spliceRows(row, 1);
      result.row = row;
    } else if (operation === "update_row") {
      const row = existingRow(req.row);
      const values = valuesArray(req.values);
      writeRow(row, values);
      result.row = row;
      result.values = values;
    } else if (operation === "append_row") {
      const values = valuesArray(req.values);
      const newRow = sheet.rowCount + 1;
      sheet.addRow(values);
      result.row = newRow;
      result.values = values;
    } else {
      throw new Error("unsupported xlsx operation: " + operation);
    }

    const fs = require("fs");
    const path = require("path");
    fs.mkdirSync(path.dirname(req.output_path), { recursive: true });
    await workbook.xlsx.writeFile(req.output_path);
    result.bytes = fs.statSync(req.output_path).size;
    console.log(JSON.stringify(result));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
