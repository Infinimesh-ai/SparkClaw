
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX reader requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const maxBytes = Number(req.max_bytes || 20000);
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const sheets = [];
    const lines = [];
    workbook.eachSheet((sheet) => {
      const rows = [];
      sheet.eachRow({ includeEmpty: false }, (row, rowNumber) => {
        const cells = [];
        row.eachCell({ includeEmpty: true }, (cell, colNumber) => {
          let value = cell.text;
          if (value === undefined || value === null) value = "";
          cells.push({ address: cell.address, row: rowNumber, column: colNumber, value: String(value) });
        });
        rows.push({ index: rowNumber, cells });
      });
      sheets.push({ name: sheet.name, index: sheet.id, rows });
      lines.push("Sheet: " + sheet.name);
      for (const row of rows) {
        lines.push(row.cells.map(cell => cell.value).join("\\t"));
      }
    });
    let content = lines.join("\\n").trim();
    const bytes = Buffer.byteLength(content, "utf8");
    const truncated = bytes > maxBytes;
    if (truncated) {
      content = Buffer.from(content, "utf8").subarray(0, maxBytes).toString("utf8");
    }
    console.log(JSON.stringify({
      content,
      truncated,
      document: {
        schema_version: "document_read_v1",
        format: "xlsx",
        sheets,
        stats: { sheets: sheets.length, rows: sheets.reduce((sum, sheet) => sum + sheet.rows.length, 0) }
      }
    }));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
