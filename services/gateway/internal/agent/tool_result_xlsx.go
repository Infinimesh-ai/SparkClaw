package agent

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const xlsxSheetEvidenceSchemaVersion = "xlsx_sheet_evidence_v1"

var (
	xlsxCellAnchorPattern = regexp.MustCompile(`(?i)\b[A-Z]{1,3}([1-9][0-9]*)\b`)
	xlsxEnglishRowPattern = regexp.MustCompile(`(?i)\brow\s*#?\s*([1-9][0-9]*)\b`)
	xlsxChineseRowPattern = regexp.MustCompile(`第\s*([1-9][0-9]*)\s*行`)
)

type xlsxEvidenceCandidate struct {
	sheet     int
	row       map[string]any
	priority  int
	mandatory bool
}

func xlsxSheetEvidenceProjection(output map[string]any, ownerRequest string, maxBytes int) string {
	document, ok := anyMap(output["document"])
	if !ok || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), "xlsx") || maxBytes <= 0 {
		return ""
	}
	sourceComplete := fileReadComplete(output)
	source := map[string]any{}
	for _, key := range []string{"path", "rel_path", "kind", "content_type", "source_bytes", "bytes", "truncated"} {
		if value, exists := output[key]; exists && usefulStructuredValue(value) {
			source[key] = value
		}
	}
	rawSheets := anySlice(document["sheets"])
	projectedSheets := make([]map[string]any, 0, len(rawSheets))
	totalRows, totalCells := 0, 0
	for offset, rawSheet := range rawSheets {
		sheet, ok := anyMap(rawSheet)
		if !ok {
			continue
		}
		index := intLikeValue(sheet["index"])
		if index <= 0 {
			index = offset + 1
		}
		projectedSheets = append(projectedSheets, map[string]any{
			"name": stringValue(sheet["name"]), "index": index,
			"state":       firstNonNil(sheet["state"], xlsxSheetState(sheet)),
			"source_hash": stringValue(sheet["source_hash"]), "rows": []any{},
		})
		for _, rawRow := range anySlice(sheet["rows"]) {
			row, ok := anyMap(rawRow)
			if !ok {
				continue
			}
			totalRows++
			totalCells += len(anySlice(row["cells"]))
		}
	}
	projection := map[string]any{
		"schema_version":     xlsxSheetEvidenceSchemaVersion,
		"source_complete":    sourceComplete,
		"selection_complete": true,
		"source":             source,
		"sheets":             xlsxSheetMapsAsAny(projectedSheets),
		"omitted":            map[string]any{"sheets": 0, "rows": totalRows, "cells": totalCells, "reason": ""},
	}
	baseRaw, _ := json.Marshal(projection)
	if len(baseRaw) > maxBytes {
		return xlsxMinimalEvidenceProjection(sourceComplete, len(projectedSheets), totalRows, totalCells, maxBytes)
	}

	candidates := xlsxEvidenceCandidates(rawSheets, ownerRequest)
	includedRows, includedCells := 0, 0
	for _, candidate := range candidates {
		rows := projectedSheets[candidate.sheet]["rows"].([]any)
		projectedRow := xlsxProjectEvidenceRow(candidate.row)
		projectedSheets[candidate.sheet]["rows"] = append(rows, projectedRow)
		rowCells := len(anySlice(projectedRow["cells"]))
		xlsxUpdateProjectionOmissions(projection, totalRows, totalCells, includedRows+1, includedCells+rowCells)
		raw, _ := json.Marshal(projection)
		if len(raw) <= maxBytes {
			includedRows++
			includedCells += rowCells
			continue
		}
		projectedSheets[candidate.sheet]["rows"] = rows
		xlsxUpdateProjectionOmissions(projection, totalRows, totalCells, includedRows, includedCells)
		if candidate.mandatory {
			projection["selection_complete"] = false
		}
	}
	for _, sheet := range projectedSheets {
		rows := sheet["rows"].([]any)
		sort.SliceStable(rows, func(i, j int) bool {
			left, _ := anyMap(rows[i])
			right, _ := anyMap(rows[j])
			return intLikeValue(left["index"]) < intLikeValue(right["index"])
		})
	}
	if projection["selection_complete"] == false {
		projection["omitted"].(map[string]any)["reason"] = "required_target_exceeds_budget"
	}
	raw, _ := json.Marshal(projection)
	if len(raw) > maxBytes {
		projection["omitted"].(map[string]any)["reason"] = ""
		raw, _ = json.Marshal(projection)
	}
	if len(raw) > maxBytes {
		return xlsxMinimalEvidenceProjection(sourceComplete, len(projectedSheets), totalRows, totalCells, maxBytes)
	}
	return string(raw)
}

func xlsxSheetState(sheet map[string]any) string {
	if boolLikeValue(sheet["hidden"]) {
		return "hidden"
	}
	return "visible"
}

func xlsxSheetMapsAsAny(sheets []map[string]any) []any {
	out := make([]any, 0, len(sheets))
	for _, sheet := range sheets {
		out = append(out, sheet)
	}
	return out
}

func xlsxMinimalEvidenceProjection(sourceComplete bool, sheets, rows, cells, maxBytes int) string {
	projection := map[string]any{
		"schema_version":     xlsxSheetEvidenceSchemaVersion,
		"source_complete":    sourceComplete,
		"selection_complete": false,
		"sheets":             []any{},
		"omitted":            map[string]any{"sheets": sheets, "rows": rows, "cells": cells, "reason": "manifest_exceeds_budget"},
	}
	raw, _ := json.Marshal(projection)
	if len(raw) <= maxBytes {
		return string(raw)
	}
	minimal := map[string]any{"schema_version": xlsxSheetEvidenceSchemaVersion, "selection_complete": false}
	raw, _ = json.Marshal(minimal)
	if len(raw) > maxBytes {
		return ""
	}
	return string(raw)
}

func xlsxEvidenceCandidates(rawSheets []any, ownerRequest string) []xlsxEvidenceCandidate {
	request := strings.ToLower(strings.TrimSpace(ownerRequest))
	cellRows, explicitRows := xlsxRequestedRows(ownerRequest)
	namedSheets := xlsxNamedSheetIndexes(rawSheets, request)
	restrictToNamed := len(namedSheets) > 0
	type candidateKey struct{ sheet, row int }
	selected := map[candidateKey]xlsxEvidenceCandidate{}
	add := func(sheetIndex int, row map[string]any, priority int, mandatory bool) {
		key := candidateKey{sheet: sheetIndex, row: intLikeValue(row["index"])}
		if key.row <= 0 {
			return
		}
		current, exists := selected[key]
		if !exists || priority < current.priority || (mandatory && !current.mandatory) {
			selected[key] = xlsxEvidenceCandidate{sheet: sheetIndex, row: row, priority: priority, mandatory: mandatory || current.mandatory}
		}
	}
	mandatoryRows := map[candidateKey]bool{}
	endRequested := xlsxEndBoundaryRequested(request)
	for sheetIndex, rawSheet := range rawSheets {
		sheet, ok := anyMap(rawSheet)
		if !ok {
			continue
		}
		rows := anySlice(sheet["rows"])
		relevant := !restrictToNamed || namedSheets[sheetIndex]
		for _, rawRow := range rows {
			row, ok := anyMap(rawRow)
			if !ok {
				continue
			}
			rowIndex := intLikeValue(row["index"])
			mandatory := relevant && (cellRows[rowIndex] || explicitRows[rowIndex] || xlsxRowMatchesRequest(row, request))
			if mandatory {
				add(sheetIndex, row, 1, true)
				mandatoryRows[candidateKey{sheetIndex, rowIndex}] = true
			}
		}
		if !relevant {
			continue
		}
		for index, rawRow := range rows {
			row, ok := anyMap(rawRow)
			if !ok {
				continue
			}
			if index < 2 {
				add(sheetIndex, row, 3, false)
			}
			if endRequested && index >= len(rows)-2 {
				add(sheetIndex, row, 4, false)
			}
		}
		for key := range mandatoryRows {
			if key.sheet != sheetIndex {
				continue
			}
			for _, rawRow := range rows {
				row, ok := anyMap(rawRow)
				if ok && absInt(intLikeValue(row["index"])-key.row) == 1 {
					add(sheetIndex, row, 5, false)
				}
			}
		}
		for _, rawRow := range rows {
			if row, ok := anyMap(rawRow); ok {
				add(sheetIndex, row, 6, false)
			}
		}
	}
	out := make([]xlsxEvidenceCandidate, 0, len(selected))
	for _, candidate := range selected {
		out = append(out, candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].priority != out[j].priority {
			return out[i].priority < out[j].priority
		}
		if out[i].sheet != out[j].sheet {
			return out[i].sheet < out[j].sheet
		}
		return intLikeValue(out[i].row["index"]) < intLikeValue(out[j].row["index"])
	})
	return out
}

func xlsxRequestedRows(ownerRequest string) (map[int]bool, map[int]bool) {
	cellRows := map[int]bool{}
	for _, match := range xlsxCellAnchorPattern.FindAllStringSubmatch(ownerRequest, -1) {
		if len(match) == 2 {
			row, _ := strconv.Atoi(match[1])
			cellRows[row] = row > 0
		}
	}
	explicitRows := map[int]bool{}
	for _, pattern := range []*regexp.Regexp{xlsxEnglishRowPattern, xlsxChineseRowPattern} {
		for _, match := range pattern.FindAllStringSubmatch(ownerRequest, -1) {
			if len(match) == 2 {
				row, _ := strconv.Atoi(match[1])
				explicitRows[row] = row > 0
			}
		}
	}
	return cellRows, explicitRows
}

func xlsxNamedSheetIndexes(rawSheets []any, request string) map[int]bool {
	namedSheets := map[int]bool{}
	for index, rawSheet := range rawSheets {
		sheet, ok := anyMap(rawSheet)
		if !ok {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(stringValue(sheet["name"])))
		if name != "" && strings.Contains(request, name) {
			namedSheets[index] = true
		}
	}
	return namedSheets
}

func xlsxRowMatchesRequest(row map[string]any, request string) bool {
	if request == "" {
		return false
	}
	for _, rawCell := range anySlice(row["cells"]) {
		cell, ok := anyMap(rawCell)
		if !ok {
			continue
		}
		for _, field := range []string{"display_text", "raw_value", "formula"} {
			value := strings.ToLower(strings.TrimSpace(stringValue(cell[field])))
			if len([]rune(value)) >= 2 && strings.Contains(request, value) {
				return true
			}
		}
	}
	return false
}

func xlsxEndBoundaryRequested(request string) bool {
	for _, marker := range []string{"append", "end of", "last row", "bottom", "末尾", "最后一行", "尾部", "追加"} {
		if strings.Contains(request, marker) {
			return true
		}
	}
	return false
}

func xlsxProjectEvidenceRow(row map[string]any) map[string]any {
	cells := []any{}
	for _, rawCell := range anySlice(row["cells"]) {
		cell, ok := anyMap(rawCell)
		if !ok {
			continue
		}
		cells = append(cells, map[string]any{
			"address": stringValue(cell["address"]), "column": intLikeValue(cell["column"]),
			"value_kind": stringValue(cell["value_kind"]), "raw_value": cell["raw_value"],
			"display_text": stringValue(cell["display_text"]), "formula": stringValue(cell["formula"]),
			"number_format": stringValue(cell["number_format"]), "hidden": boolLikeValue(cell["hidden"]),
			"style_hash": stringValue(cell["style_hash"]), "merge_anchor": stringValue(cell["merge_anchor"]),
			"source_hash": stringValue(cell["source_hash"]),
		})
	}
	return map[string]any{
		"index": intLikeValue(row["index"]), "hidden": boolLikeValue(row["hidden"]),
		"source_hash": stringValue(row["source_hash"]), "cells": cells,
	}
}

func xlsxUpdateProjectionOmissions(projection map[string]any, totalRows, totalCells, includedRows, includedCells int) {
	omitted := projection["omitted"].(map[string]any)
	omitted["rows"] = max(0, totalRows-includedRows)
	omitted["cells"] = max(0, totalCells-includedCells)
}

func xlsxEvidenceSelectionState(text string) (bool, bool) {
	var projection map[string]any
	if json.Unmarshal([]byte(text), &projection) != nil || projection["schema_version"] != xlsxSheetEvidenceSchemaVersion {
		return false, false
	}
	return boolLikeValue(projection["selection_complete"]), true
}

func xlsxEvidenceAuditFields(text string) map[string]any {
	var projection map[string]any
	if json.Unmarshal([]byte(text), &projection) != nil || projection["schema_version"] != xlsxSheetEvidenceSchemaVersion {
		return nil
	}
	selectedSheets, selectedRows, selectedCells := 0, 0, 0
	for _, rawSheet := range anySlice(projection["sheets"]) {
		sheet, ok := anyMap(rawSheet)
		if !ok {
			continue
		}
		selectedSheets++
		for _, rawRow := range anySlice(sheet["rows"]) {
			row, ok := anyMap(rawRow)
			if !ok {
				continue
			}
			selectedRows++
			selectedCells += len(anySlice(row["cells"]))
		}
	}
	omitted, _ := anyMap(projection["omitted"])
	return map[string]any{
		"evidence_schema_version": xlsxSheetEvidenceSchemaVersion,
		"selection_complete":      boolLikeValue(projection["selection_complete"]),
		"selected_sheets":         selectedSheets, "selected_rows": selectedRows, "selected_cells": selectedCells,
		"omitted_sheets": intLikeValue(omitted["sheets"]), "omitted_rows": intLikeValue(omitted["rows"]), "omitted_cells": intLikeValue(omitted["cells"]),
	}
}

func xlsxDocumentReadEvidence(output map[string]any, ownerRequest string, maxBytes int) []toolEvidence {
	text := xlsxSheetEvidenceProjection(output, ownerRequest, maxBytes)
	if text == "" {
		return nil
	}
	fields := xlsxEvidenceAuditFields(text)
	omitted := intLikeValue(fields["omitted_rows"]) > 0 || intLikeValue(fields["omitted_cells"]) > 0
	return []toolEvidence{{
		Kind: "document.xlsx_sheets", Text: text, Excerpt: omitted, Omitted: omitted,
		SourceTruncated: !fileReadComplete(output), ReadComplete: fileReadComplete(output),
	}}
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
