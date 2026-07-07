
let ExcelJS;
try {
  ExcelJS = require("exceljs");
} catch (error) {
  console.log(JSON.stringify({ error: "XLSX adapter requires exceljs" }));
  process.exit(0);
}

let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const replacements = req.replacements || [];
    const counts = new Map(replacements.map(item => [item.Find || item.find, 0]));
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    let total = 0;
    workbook.eachSheet(sheet => {
      sheet.eachRow(row => {
        row.eachCell(cell => {
          if (typeof cell.value !== "string") return;
          let text = cell.value;
          for (const item of replacements) {
            const find = item.Find || item.find || "";
            const repl = item.Replace || item.replace || "";
            if (!find) continue;
            const count = text.split(find).length - 1;
            if (count > 0) {
              text = text.split(find).join(repl);
              counts.set(find, (counts.get(find) || 0) + count);
              total += count;
            }
          }
          cell.value = text;
        });
      });
    });
    const missing = [...counts.entries()].filter(([, count]) => count === 0).map(([find]) => find);
    if (missing.length) {
      console.log(JSON.stringify({ error: "find text was not matched: " + missing.map(x => JSON.stringify(x)).join(", ") }));
      return;
    }
    const fs = require("fs");
    const path = require("path");
    fs.mkdirSync(path.dirname(req.output_path), { recursive: true });
    await workbook.xlsx.writeFile(req.output_path);
    console.log(JSON.stringify({
      replacements: total,
      bytes: fs.statSync(req.output_path).size,
      details: [...counts.entries()].map(([find, count]) => ({ find, count }))
    }));
  } catch (error) {
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
