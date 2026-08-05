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
	case "insert_row", "append_row":
		if sheetRowCount(after, stringValue(edit.Arguments["sheet"])) != sheetRowCount(before, stringValue(edit.Arguments["sheet"]))+1 ||
			!sheetContainsValues(after, stringValue(edit.Arguments["sheet"]), anySlice(edit.Arguments["values"])) {
			return true, fmt.Errorf("the inserted row was not found at the expected structural boundary")
		}
	case "delete_row":
		if sheetRowCount(after, stringValue(edit.Arguments["sheet"])) != sheetRowCount(before, stringValue(edit.Arguments["sheet"]))-1 {
			return true, fmt.Errorf("the deleted row count was not reflected in the structured output")
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

func sheetRowCount(document Representation, sheetName string) int {
	for _, sheet := range document.Sheets {
		if strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			return len(mapSlice(sheet["rows"]))
		}
	}
	return 0
}

func sheetContainsValues(document Representation, sheetName string, values []any) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			if rowHasValues(row, values) {
				return true
			}
		}
	}
	return false
}

func rowHasValues(row map[string]any, values []any) bool {
	cells := mapSlice(row["cells"])
	if len(cells) < len(values) {
		return false
	}
	for index, value := range values {
		if !sameJSON(cells[index]["raw_value"], value) {
			return false
		}
	}
	return true
}
