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
    const images = [];
    const resources = [];
    const comments = [];
    const hyperlinks = [];
    const mergedRanges = [];
    let extractedBytes = 0;
    const appendLine = (line) => {
      extractedBytes += Buffer.byteLength(line, "utf8") + (lines.length ? 1 : 0);
      if (extractedBytes > maxBytes) {
        const error = new Error("complete extracted content exceeds max_bytes");
        error.documentDeferred = true;
        throw error;
      }
      lines.push(line);
    };
    const styleHint = (cell) => ({
      bold: Boolean(cell.font && cell.font.bold),
      italic: Boolean(cell.font && cell.font.italic),
      fill_type: String(cell.fill && cell.fill.type || ""),
      horizontal_alignment: String(cell.alignment && cell.alignment.horizontal || ""),
    });
    const jsonValue = (value) => {
      if (value === undefined || value === null) return null;
      if (value instanceof Date) return value.toISOString();
      if (typeof value === "number") return Number.isFinite(value) ? value : String(value);
      if (["string", "boolean"].includes(typeof value)) return value;
      return null;
    };
    const typedCellValue = (cell) => {
      const value = cell.value;
      const formula = cell.formula || (value && typeof value === "object" && value.formula) || "";
      const displayText = String(cell.text ?? "");
      if (formula) {
        const result = cell.result ?? (value && typeof value === "object" ? value.result : null);
        return { value_kind: "formula", raw_value: jsonValue(result) ?? displayText, display_text: displayText, formula: String(formula) };
      }
      if (value === undefined || value === null || value === "") {
        return { value_kind: "blank", raw_value: null, display_text: displayText, formula: "" };
      }
      if (value instanceof Date) {
        return { value_kind: "date", raw_value: value.toISOString(), display_text: displayText, formula: "" };
      }
      if (typeof value === "string") {
        return { value_kind: "string", raw_value: value, display_text: displayText, formula: "" };
      }
      if (typeof value === "number") {
        return { value_kind: "number", raw_value: jsonValue(value), display_text: displayText, formula: "" };
      }
      if (typeof value === "boolean") {
        return { value_kind: "boolean", raw_value: value, display_text: displayText, formula: "" };
      }
      if (value && typeof value === "object" && Array.isArray(value.richText)) {
        return { value_kind: "rich_text", raw_value: displayText, display_text: displayText, formula: "" };
      }
      if (value && typeof value === "object" && value.error !== undefined) {
        return { value_kind: "error", raw_value: String(value.error), display_text: displayText, formula: "" };
      }
      if (value && typeof value === "object" && value.text !== undefined) {
        return { value_kind: "string", raw_value: String(value.text), display_text: displayText, formula: "" };
      }
      return { value_kind: "unknown", raw_value: displayText, display_text: displayText, formula: "" };
    };
    const cellStyle = (cell) => JSON.parse(JSON.stringify(cell.style || {}));
    const rangeAddress = (sheet, point) => {
      if (!point) return "";
      const row = Number(point.nativeRow ?? point.row ?? 0) + (point.nativeRow !== undefined ? 1 : 0);
      const column = Number(point.nativeCol ?? point.col ?? 0) + (point.nativeCol !== undefined ? 1 : 0);
      if (row <= 0 || column <= 0) return "";
      return sheet.getCell(row, column).address;
    };
    const extensionContentType = (extension) => {
      switch (String(extension || "").toLowerCase()) {
        case "png": return "image/png";
        case "jpg":
        case "jpeg": return "image/jpeg";
        case "gif": return "image/gif";
        case "webp": return "image/webp";
        default: return "application/octet-stream";
      }
    };

    workbook.eachSheet((sheet) => {
      const rows = [];
      appendLine("Sheet: " + sheet.name);
      sheet.eachRow({ includeEmpty: false }, (row, rowNumber) => {
        const cells = [];
        row.eachCell({ includeEmpty: true }, (cell, colNumber) => {
          const typed = typedCellValue(cell);
          const item = {
            address: cell.address,
            row: rowNumber,
            column: colNumber,
            value: typed.display_text,
            value_kind: typed.value_kind,
            raw_value: typed.raw_value,
            display_text: typed.display_text,
            formula: typed.formula,
            number_format: String(cell.numFmt || ""),
            hidden: Boolean(row.hidden || sheet.getColumn(colNumber).hidden),
            style_hint: styleHint(cell),
            style: cellStyle(cell),
            merge_anchor: cell.isMerged ? String(cell.master.address || "") : "",
          };
          cells.push(item);
          if (cell.note !== undefined && cell.note !== null) {
            const noteText = typeof cell.note === "string" ? cell.note : String(cell.note.texts?.map(part => part.text || "").join("") || "");
            comments.push({
              kind: "comment",
              text: noteText,
              location: { sheet: sheet.name, cell: cell.address, path: `workbook.sheet[${sheet.name}].cell[${cell.address}]` },
              source: { parser: "exceljs" },
            });
          }
          if (cell.hyperlink) {
            hyperlinks.push({
              kind: "hyperlink",
              text: typed.display_text,
              target: String(cell.hyperlink),
              location: { sheet: sheet.name, cell: cell.address, path: `workbook.sheet[${sheet.name}].cell[${cell.address}]` },
              source: { parser: "exceljs" },
            });
          }
        });
        rows.push({ index: rowNumber, hidden: Boolean(row.hidden), height: Number(row.height || 0), cells });
        appendLine(cells.map(cell => cell.value).join("\t"));
      });

      const sheetMerges = Array.isArray(sheet.model && sheet.model.merges) ? sheet.model.merges : [];
      for (const address of sheetMerges) {
        mergedRanges.push({ sheet: sheet.name, range: String(address), path: `workbook.sheet[${sheet.name}].merge[${address}]` });
      }
      const columns = [];
      for (let index = 1; index <= sheet.columnCount; index += 1) {
        const column = sheet.getColumn(index);
        columns.push({ index, width: Number(column.width || 0), hidden: Boolean(column.hidden), outline_level: Number(column.outlineLevel || 0) });
      }
      sheets.push({ name: sheet.name, index: sheet.id, hidden: sheet.state !== "visible", state: sheet.state, rows, columns });

      for (const placement of sheet.getImages()) {
        const image = workbook.getImage(Number(placement.imageId));
        if (!image || !image.buffer) continue;
        const extension = String(image.extension || "").toLowerCase();
        const contentType = extensionContentType(extension);
        const resourceKey = `xl:${placement.imageId}:${extension}`;
        const topLeft = rangeAddress(sheet, placement.range && placement.range.tl);
        const bottomRight = rangeAddress(sheet, placement.range && placement.range.br);
        const path = `workbook.sheet[${sheet.name}].image[${placement.imageId}]`;
        images.push({
          kind: "image",
          resource_key: resourceKey,
          parent_path: `workbook.sheet[${sheet.name}]`,
          location: { sheet: sheet.name, sheet_index: sheet.id, image_id: Number(placement.imageId), top_left: topLeft, bottom_right: bottomRight, path },
          source: { parser: "exceljs", relationship_id: String(placement.imageId), part_name: String(image.name || image.filename || "") },
          content_type: contentType,
        });
        if (!resources.some(resource => resource.key === resourceKey)) {
          resources.push({ key: resourceKey, kind: "image", content_type: contentType, data_base64: Buffer.from(image.buffer).toString("base64") });
        }
      }
    });
    let content = lines.join("\n").trim();
    const bytes = Buffer.byteLength(content, "utf8");
    const truncated = bytes > maxBytes;
    if (truncated) {
      content = Buffer.from(content, "utf8").subarray(0, maxBytes).toString("utf8");
    }
    console.log(JSON.stringify({
      content,
      truncated,
      extracted_bytes: extractedBytes,
      resources,
      document: {
        schema_version: "document_read_v1",
        format: "xlsx",
        source: "exceljs",
        sheets,
        enrichment: {
          schema_version: "document_enrichment_v1",
          assets: { images, charts: [], embedded_objects: [] },
          annotations: { comments, notes: [], hyperlinks },
          layout: { sections: [], page_settings: [], slide_layouts: [], merged_ranges: mergedRanges },
          extensions: { status: "deferred", parts: [] },
          coverage: { content: "complete", assets: "partial", annotations: "complete", layout: "partial", extensions: "deferred" },
        },
        stats: {
          sheets: sheets.length,
          rows: sheets.reduce((sum, sheet) => sum + sheet.rows.length, 0),
          images: images.length,
          comments: comments.length,
          hyperlinks: hyperlinks.length,
          merged_ranges: mergedRanges.length,
        }
      }
    }));
  } catch (error) {
    if (error && error.documentDeferred) {
      console.log(JSON.stringify({ content: "", truncated: true, extracted_bytes: Number(maxBytes) + 1 }));
      return;
    }
    console.log(JSON.stringify({ error: String(error && error.message || error) }));
  }
});
