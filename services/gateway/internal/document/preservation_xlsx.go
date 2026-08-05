package document

import (
	"fmt"
	"regexp"
	"strings"
)

func verifyXLSXExpectedMutation(operation string, before, after Representation, edit EditRequest) (bool, error) {
	switch operation {
	case "update_cell":
		if !cellHasValue(after, stringValue(edit.Arguments["sheet"]), stringValue(edit.Arguments["cell"]), stringValue(edit.Arguments["value"])) {
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
		if !sheetRowAtIndexHasValues(after, stringValue(edit.Arguments["sheet"]), intValue(edit.Arguments["row"]), anySlice(edit.Arguments["values"])) {
			return true, fmt.Errorf("the target row does not contain the expected after-values")
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
	return equalFoldValue(block.Location["sheet"], stringValue(edit.Arguments["sheet"])) && intValue(block.Location["row_index"]) == intValue(edit.Arguments["row"]), true
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

func cellHasValue(document Representation, sheetName, address, expected string) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			for _, cell := range mapSlice(row["cells"]) {
				if strings.EqualFold(stringValue(cell["address"]), address) && stringValue(cell["value"]) == expected {
					return true
				}
			}
		}
	}
	return false
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

func sheetRowAtIndexHasValues(document Representation, sheetName string, index int, values []any) bool {
	for _, sheet := range document.Sheets {
		if !strings.EqualFold(stringValue(sheet["name"]), sheetName) {
			continue
		}
		for _, row := range mapSlice(sheet["rows"]) {
			if intValue(row["index"]) == index && rowHasValues(row, values) {
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
		if stringValue(cells[index]["value"]) != stringValue(value) {
			return false
		}
	}
	return true
}
