
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

    function valuesArray(value, nonEmpty = false) {
      if (!Array.isArray(value)) throw new Error("values must be an array");
      if (nonEmpty && value.length === 0) throw new Error("values must be a non-empty array");
      return value;
    }

    function writeNewRow(rowNumber, values) {
      const row = sheet.getRow(rowNumber);
      row.values = [];
      values.forEach((value, index) => {
        row.getCell(index + 1).value = value;
      });
      row.commit();
    }

    function cellSnapshot(cell) {
      const value = cell.value;
      const formula = cell.formula || (value && typeof value === "object" && value.formula) || "";
      let rawValue = value;
      let valueKind = typeof value;
      if (formula) {
        valueKind = "formula";
        rawValue = cell.result ?? (value && typeof value === "object" ? value.result : null);
      } else if (value === null || value === undefined || value === "") {
        valueKind = "blank";
        rawValue = null;
      } else if (value instanceof Date) {
        valueKind = "date";
        rawValue = value.toISOString();
      } else if (value && typeof value === "object" && Array.isArray(value.richText)) {
        valueKind = "rich_text";
        rawValue = String(cell.text || "");
      } else if (value && typeof value === "object" && value.error !== undefined) {
        valueKind = "error";
        rawValue = String(value.error);
      } else if (value && typeof value === "object" && value.text !== undefined) {
        valueKind = "string";
        rawValue = String(value.text);
      } else if (!["string", "number", "boolean"].includes(typeof value)) {
        valueKind = "unknown";
        rawValue = String(cell.text || "");
      }
      return {
        address: cell.address,
        value_kind: valueKind,
        raw_value: rawValue,
        display_text: String(cell.text || ""),
        formula: String(formula || ""),
        number_format: String(cell.numFmt || "")
      };
    }

    function updateCells(cells) {
      const changed = [];
      for (const [cell, value] of cells) {
        const before = cellSnapshot(cell);
        cell.value = value;
        const after = cellSnapshot(cell);
        if (JSON.stringify(before) !== JSON.stringify(after)) {
          changed.push({ address: cell.address, before, after });
        }
      }
      return changed;
    }

    function rowHasContent(rowNumber) {
      const row = sheet.getRow(rowNumber);
      for (let column = 1; column <= row.cellCount; column += 1) {
        const value = row.getCell(column).value;
        if (value !== null && value !== undefined && value !== "") return true;
      }
      return false;
    }

    function assertCell(address) {
      const cell = String(address || "").trim().toUpperCase();
      if (!/^[A-Z]+[1-9][0-9]*$/.test(cell)) throw new Error("cell must be a valid A1 address");
      return cell;
    }

    if (operation === "update_cell") {
      const cellAddress = assertCell(req.cell);
      result.changed_cells = updateCells([[sheet.getCell(cellAddress), req.value]]);
      result.changed = result.changed_cells.length;
      result.cell = cellAddress;
      result.value = req.value;
    } else if (operation === "insert_row") {
      const row = existingRow(req.row);
      const position = String(req.position || "").trim().toLowerCase();
      if (position !== "before" && position !== "after") throw new Error("position must be before or after");
      const insertAt = position === "before" ? row : row + 1;
      const values = valuesArray(req.values);
      sheet.spliceRows(insertAt, 0, values);
      result.changed = 1;
      result.row = row;
      result.inserted_row = insertAt;
      result.values = values;
    } else if (operation === "delete_row") {
      const row = existingRow(req.row);
      sheet.spliceRows(row, 1);
      result.changed = 1;
      result.row = row;
    } else if (operation === "update_row") {
      const row = existingRow(req.row);
      const values = valuesArray(req.values, true);
      const targetRow = sheet.getRow(row);
      result.changed_cells = updateCells(values.map((value, index) => [targetRow.getCell(index + 1), value]));
      result.changed = result.changed_cells.length;
      result.row = row;
      result.values = values;
    } else if (operation === "append_row") {
      const values = valuesArray(req.values);
      const appendAfterRow = Number(req.append_after_row);
      if (!Number.isInteger(appendAfterRow) || appendAfterRow < 0) {
        throw new Error("append_after_row must be a non-negative row anchor");
      }
      const newRow = appendAfterRow + 1;
      if (rowHasContent(newRow)) throw new Error("append target row is not empty: " + newRow);
      writeNewRow(newRow, values);
      result.changed = 1;
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
