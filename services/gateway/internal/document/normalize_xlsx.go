package document

import (
	"fmt"
	"strconv"
)

func normalizeSheets(documentID string, values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for sheetIndex, value := range values {
		sheet := cloneMap(value)
		name := stringValue(sheet["name"])
		index := intValue(sheet["index"])
		if index <= 0 {
			index = sheetIndex + 1
			sheet["index"] = index
		}
		sheetID := stableID("sheet", documentID+"\x00"+fmt.Sprintf("%d:%s", index, name))
		sheet["id"] = sheetID
		rows := mapSlice(sheet["rows"])
		for rowOffset, row := range rows {
			rowIndex := intValue(row["index"])
			if rowIndex <= 0 {
				rowIndex = rowOffset + 1
				row["index"] = rowIndex
			}
			row["id"] = stableID("row", sheetID+"\x00"+strconv.Itoa(rowIndex))
			cells := mapSlice(row["cells"])
			for cellOffset, cell := range cells {
				address := stringValue(cell["address"])
				if address == "" {
					address = fmt.Sprintf("R%dC%d", rowIndex, cellOffset+1)
				}
				cell["id"] = stableID("cell", sheetID+"\x00"+address)
				cell["location"] = map[string]any{
					"part": "workbook", "block_type": "cell", "sheet": name, "sheet_index": index,
					"row_index": rowIndex, "column_index": intValue(cell["column"]), "cell": address,
					"path": fmt.Sprintf("workbook.sheet[%s].cell[%s]", name, address),
				}
			}
			row["cells"] = cells
		}
		sheet["rows"] = rows
		out = append(out, sheet)
	}
	return out
}
