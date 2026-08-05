package document

import (
	"fmt"
	"regexp"
	"strings"
)

func verifyXLSXExpectedMutation(operation string, before, after Representation, edit EditRequest) (bool, error) {
	switch operation {
	case "update_cell":
		if !xlsxCellHasTypedValue(after, stringValue(edit.Arguments["sheet"]), stringValue(edit.Arguments["cell"]), edit.Arguments["value"]) {
			return true, fmt.Errorf("the target cell does not contain the expected after-value")
		}
	case "insert_row", "append_row", "delete_row":
		if err := verifyXLSXStructuralRowMutation(operation, before, after, edit); err != nil {
			return true, err
		}
	case "update_row":
		if err := verifyXLSXRowPrefixMutation(before, after, stringValue(edit.Arguments["sheet"]), intValue(edit.Arguments["row"]), anySlice(edit.Arguments["values"])); err != nil {
			return true, err
		}
	default:
		return false, nil
	}
	return true, nil
}

func xlsxMutationAllowsBlock(operation string, edit EditRequest, block Block) (bool, bool) {
	if operation != "update_row" {
		return false, false
	}
	return equalFoldValue(block.Location["sheet"], stringValue(edit.Arguments["sheet"])) &&
		intValue(block.Location["row_index"]) == intValue(edit.Arguments["row"]) &&
		intValue(block.Location["column_index"]) <= len(anySlice(edit.Arguments["values"])), true
}

func xlsxOperationChangesEntityIndexes(operation string) bool {
	switch operation {
	case "insert_row", "delete_row", "append_row":
		return true
	default:
		return false
	}
}

var mergedRangePattern = regexp.MustCompile(`^([A-Za-z]+)([0-9]+):([A-Za-z]+)([0-9]+)$`)

func mergedRangeBeforeCoordinates(value string, edit EditRequest) string {
	matches := mergedRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 5 {
		return value
	}
	startRow := intValue(matches[2])
	endRow := intValue(matches[4])
	row := intValue(edit.Arguments["row"])
	switch strings.ToLower(strings.TrimSpace(edit.Operation)) {
	case "insert_row":
		insertAt := row
		if strings.EqualFold(strings.TrimSpace(stringValue(edit.Arguments["position"])), "after") {
			insertAt++
		}
		if startRow >= insertAt {
			startRow--
			endRow--
		} else if endRow >= insertAt {
			endRow--
		}
	case "delete_row":
		if startRow >= row {
			startRow++
			endRow++
		} else if endRow >= row {
			endRow++
		}
	}
	return fmt.Sprintf("%s%d:%s%d", strings.ToUpper(matches[1]), startRow, strings.ToUpper(matches[3]), endRow)
}

func xlsxCellHasTypedValue(document Representation, sheetName, address string, expected any) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			for _, cell := range mapSlice(row["cells"]) {
				if strings.EqualFold(stringValue(cell["address"]), address) && sameJSON(cell["raw_value"], expected) && stringValue(cell["formula"]) == "" {
					return true
				}
			}
		}
	}
	return false
}

func verifyXLSXRowPrefixMutation(before, after Representation, sheetName string, index int, values []any) error {
	if len(values) == 0 {
		return fmt.Errorf("the target row update has no supplied values")
	}
	beforeRow, beforeFound := xlsxRepresentationRow(before, sheetName, index)
	afterRow, afterFound := xlsxRepresentationRow(after, sheetName, index)
	if !beforeFound || !afterFound {
		return fmt.Errorf("the target row was not found before and after the edit")
	}
	beforeCells := mapSlice(beforeRow["cells"])
	afterCells := mapSlice(afterRow["cells"])
	for offset, value := range values {
		column := offset + 1
		cell, found := xlsxCellByColumn(afterCells, column)
		if !found || !sameJSON(cell["raw_value"], value) || stringValue(cell["formula"]) != "" {
			return fmt.Errorf("the target row cell at column %d does not contain the expected typed after-value", column)
		}
	}
	for _, beforeCell := range beforeCells {
		column := intValue(beforeCell["column"])
		if column <= len(values) {
			continue
		}
		afterCell, found := xlsxCellByColumn(afterCells, column)
		if !found || stringValue(beforeCell["source_hash"]) == "" || stringValue(beforeCell["source_hash"]) != stringValue(afterCell["source_hash"]) {
			return fmt.Errorf("the trailing cell at column %d changed outside the supplied row prefix", column)
		}
	}
	return nil
}

func xlsxRepresentationRow(representation Representation, sheetName string, index int) (map[string]any, bool) {
	for _, sheet := range representation.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			if intValue(row["index"]) == index {
				return row, true
			}
		}
	}
	return nil, false
}

func xlsxCellByColumn(cells []map[string]any, column int) (map[string]any, bool) {
	for _, cell := range cells {
		if intValue(cell["column"]) == column {
			return cell, true
		}
	}
	return nil, false
}

func verifyXLSXStructuralRowMutation(operation string, before, after Representation, edit EditRequest) error {
	sheetName := stringValue(edit.Arguments["sheet"])
	beforeRows := xlsxRepresentationRows(before, sheetName)
	afterRows := xlsxRepresentationRows(after, sheetName)
	if len(beforeRows) == 0 {
		return fmt.Errorf("the target worksheet has no structured rows before the edit")
	}
	switch operation {
	case "append_row":
		if len(afterRows) != len(beforeRows)+1 {
			return fmt.Errorf("the appended row count was not reflected in the structured output")
		}
		for _, beforeRow := range beforeRows {
			afterRow, ok := xlsxRowByRepresentationIndex(afterRows, intValue(beforeRow["index"]))
			if !ok || stringValue(beforeRow["source_hash"]) != stringValue(afterRow["source_hash"]) {
				return fmt.Errorf("an existing row changed while appending")
			}
		}
		appendIndex := xlsxLastContentRow(beforeRows) + 1
		appended, ok := xlsxRowByRepresentationIndex(afterRows, appendIndex)
		if !ok || !xlsxRowHasTypedPrefix(appended, anySlice(edit.Arguments["values"])) {
			return fmt.Errorf("the appended row was not found at the structured sheet boundary")
		}
		return nil
	case "insert_row":
		if len(afterRows) != len(beforeRows)+1 {
			return fmt.Errorf("the inserted row count was not reflected in the structured output")
		}
		insertAt := intValue(edit.Arguments["row"])
		if strings.EqualFold(stringValue(edit.Arguments["position"]), "after") {
			insertAt++
		}
		inserted, ok := xlsxRowByRepresentationIndex(afterRows, insertAt)
		if !ok || !xlsxRowHasTypedPrefix(inserted, anySlice(edit.Arguments["values"])) {
			return fmt.Errorf("the inserted row was not found at the evidence-bound position")
		}
		for _, beforeRow := range beforeRows {
			beforeIndex := intValue(beforeRow["index"])
			afterIndex := beforeIndex
			if beforeIndex >= insertAt {
				afterIndex++
			}
			afterRow, found := xlsxRowByRepresentationIndex(afterRows, afterIndex)
			if !found || !xlsxRowsSemanticallyEqual(beforeRow, afterRow) {
				return fmt.Errorf("an unrelated row changed while inserting at row %d", insertAt)
			}
		}
		return nil
	case "delete_row":
		if len(afterRows) != len(beforeRows)-1 {
			return fmt.Errorf("the deleted row count was not reflected in the structured output")
		}
		deleteAt := intValue(edit.Arguments["row"])
		if _, ok := xlsxRowByRepresentationIndex(beforeRows, deleteAt); !ok {
			return fmt.Errorf("the evidence-bound deleted row was not present before the edit")
		}
		for _, beforeRow := range beforeRows {
			beforeIndex := intValue(beforeRow["index"])
			if beforeIndex == deleteAt {
				continue
			}
			afterIndex := beforeIndex
			if beforeIndex > deleteAt {
				afterIndex--
			}
			afterRow, found := xlsxRowByRepresentationIndex(afterRows, afterIndex)
			if !found || !xlsxRowsSemanticallyEqual(beforeRow, afterRow) {
				return fmt.Errorf("an unrelated row changed while deleting row %d", deleteAt)
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported XLSX structural verification operation %q", operation)
	}
}

func xlsxRepresentationRows(representation Representation, sheetName string) []map[string]any {
	for _, sheet := range representation.Sheets {
		if strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			return mapSlice(sheet["rows"])
		}
	}
	return nil
}

func xlsxRowByRepresentationIndex(rows []map[string]any, index int) (map[string]any, bool) {
	for _, row := range rows {
		if intValue(row["index"]) == index {
			return row, true
		}
	}
	return nil, false
}

func xlsxRowHasTypedPrefix(row map[string]any, values []any) bool {
	if len(values) == 0 {
		return false
	}
	cells := mapSlice(row["cells"])
	for offset, value := range values {
		cell, ok := xlsxCellByColumn(cells, offset+1)
		if !ok || !sameJSON(cell["raw_value"], value) || stringValue(cell["formula"]) != "" {
			return false
		}
	}
	for _, cell := range cells {
		if intValue(cell["column"]) > len(values) && (cell["raw_value"] != nil || stringValue(cell["formula"]) != "") {
			return false
		}
	}
	return true
}

func xlsxRowsSemanticallyEqual(before, after map[string]any) bool {
	if !sameJSON(before["hidden"], after["hidden"]) || !sameJSON(before["height"], after["height"]) {
		return false
	}
	beforeCells := mapSlice(before["cells"])
	afterCells := mapSlice(after["cells"])
	if len(beforeCells) != len(afterCells) {
		return false
	}
	for _, beforeCell := range beforeCells {
		afterCell, ok := xlsxCellByColumn(afterCells, intValue(beforeCell["column"]))
		if !ok {
			return false
		}
		for _, field := range []string{"value_kind", "raw_value", "formula", "number_format", "hidden", "style_hash"} {
			if !sameJSON(beforeCell[field], afterCell[field]) {
				return false
			}
		}
	}
	return true
}

func xlsxLastContentRow(rows []map[string]any) int {
	last := 0
	for _, row := range rows {
		for _, cell := range mapSlice(row["cells"]) {
			if cell["raw_value"] != nil || stringValue(cell["formula"]) != "" || stringValue(cell["display_text"]) != "" {
				last = max(last, intValue(row["index"]))
				break
			}
		}
	}
	return last
}
