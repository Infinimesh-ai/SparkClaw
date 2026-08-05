package agent

import (
	"fmt"
	"strings"
)

func documentTableEvidence(document map[string]any) string {
	tables, ok := document["tables"].([]any)
	if !ok || len(tables) == 0 {
		return ""
	}
	lines := []string{}
	for i, item := range tables {
		if i >= 3 {
			break
		}
		table, ok := anyMap(item)
		if !ok {
			continue
		}
		rows, ok := table["rows"].([]any)
		if !ok || len(rows) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("table %d:", i+1))
		for j, row := range rows {
			if j >= 5 {
				break
			}
			rowValues := []string{}
			if cells, ok := row.([]any); ok {
				for _, cell := range cells {
					rowValues = append(rowValues, strings.TrimSpace(stringValue(cell)))
				}
			} else if rowMap, ok := anyMap(row); ok {
				if cells, ok := rowMap["cells"].([]any); ok {
					for _, cell := range cells {
						rowValues = append(rowValues, strings.TrimSpace(stringValue(cell)))
					}
				}
			}
			if len(rowValues) > 0 {
				lines = append(lines, strings.Join(rowValues, " | "))
			}
		}
	}
	return strings.Join(lines, "\n")
}
