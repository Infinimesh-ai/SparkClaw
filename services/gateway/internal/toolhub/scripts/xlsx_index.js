// Native cell values are authoritative parser observations. Search context is
// separate; cached formula results are never recalculated or labelled current.
const ExcelJS = require("exceljs");
let raw = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", chunk => raw += chunk);
process.stdin.on("end", async () => {
  try {
    const req = JSON.parse(raw);
    const structureV2 = (req.structure_version ?? 1) >= 2;
    const workbook = new ExcelJS.Workbook();
    await workbook.xlsx.readFile(req.path);
    const units = [];
    let bytes = 0;
    const add = unit => {
      bytes += Buffer.byteLength(JSON.stringify(unit));
      if (units.length >= 250000 || bytes > 64 * 1024 * 1024) throw new Error("Workbook exceeds structure extraction admission");
      units.push(unit);
    };
    workbook.eachSheet(sheet => {
      const sheetBlock = `workbook.sheet[${sheet.name}]`;
      add({text:sheet.name,title:sheet.name,kind:"sheet",heading_level:1,block:sheetBlock,
        ...(structureV2 ? {structure_source:"sheet"} : {}),
        values:[{sheet_name:sheet.name,sheet_state:sheet.state}]});
      let firstRow = null;
      const headers = new Map();
      sheet.eachRow({includeEmpty:false}, (row,rowNumber) => {
        if (firstRow === null) firstRow = rowNumber;
        row.eachCell({includeEmpty:false}, cell => {
          const block = `${sheetBlock}.cell[${cell.address}]`;
          const text = String(cell.text ?? "");
          if (rowNumber === firstRow) headers.set(cell.col,{block,text});
          const header = headers.get(cell.col);
          const value = cell.value;
          const formula = value && typeof value === "object" ? value.formula ?? value.sharedFormula : undefined;
          const mergeAnchor = cell.isMerged ? cell.master.address : undefined;
          // Merged aliases describe their source anchor, without duplicating
          // its value in traversal/calculation input.
          const alias = mergeAnchor && mergeAnchor !== cell.address;
          add({text:alias ? "" : text,kind:"cell",block,
            ...(structureV2 ? {group_key:`${sheetBlock}.row[${rowNumber}]`} : {}),
            search_text:header && rowNumber !== firstRow ? header.text : "",
            dependencies:[...(header && rowNumber !== firstRow ? [header.block] : []),...(alias ? [`${sheetBlock}.cell[${mergeAnchor}]`] : [])],
            values:[{address:cell.address,type:cell.type,value:alias ? null : value,display_text:alias ? "" : text,
              ...(formula !== undefined ? {formula,cached_result:cell.result,cached_result_available:Object.hasOwn(value,"result"),formula_result_recalculated:false} : {}),
              display_text_source:"exceljs.cell.text",number_format:cell.numFmt ?? "",row_hidden:Boolean(row.hidden),column_hidden:Boolean(sheet.getColumn(cell.col).hidden),sheet_state:sheet.state,
              ...(mergeAnchor ? {merge_anchor:mergeAnchor,is_merge_alias:Boolean(alias)} : {}),...(cell.note ? {note:cell.note} : {})}]});
        });
      });
    });
    process.stdout.write(JSON.stringify({units,limitations:["The first populated row supplies contextual column labels; semantic header and unit roles are not inferred.","Formula results are stored workbook values, not recalculated. Charts, images, names and external links are not fully extracted."]}));
  } catch(error) {console.error(String(error?.message || error));process.exit(1);}
});
