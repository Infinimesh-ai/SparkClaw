package document

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

func Normalize(metadata Metadata, strategy, content string, raw map[string]any) (Representation, error) {
	if strings.TrimSpace(metadata.Format) == "" {
		return Representation{}, fmt.Errorf("detected format is required")
	}
	documentID := stableID("document", metadata.Relative+"\x00"+metadata.Format)
	representation := Representation{
		SchemaVersion: "document_read_v1", RepresentationVersion: "structured_document_v1",
		ID: documentID, Format: metadata.Format, Source: stringValue(raw["source"]), Metadata: metadata,
		Strategy: StrategyMetadata{
			Name: strategy, Mode: "full", Reason: "small_file_complete_read", Complete: true, Extensible: true,
			MaxSourceBytes: SmallFileMaxBytes, MaxExtractedBytes: SmallExtractedMaxBytes,
		},
		ContentScope: map[string]any{"kind": "full_document", "complete": true},
		Blocks:       []Block{}, Paragraphs: []map[string]any{}, Tables: []map[string]any{}, Sheets: []map[string]any{},
		Slides: []map[string]any{}, Sections: []map[string]any{}, Pages: []map[string]any{}, Stats: map[string]any{},
	}
	if representation.Source == "" {
		representation.Source = sourceForFormat(metadata.Format)
	}
	for key, value := range mapValue(raw["stats"]) {
		representation.Stats[key] = value
	}
	representation.Stats["complete"] = true
	representation.Stats["source_bytes"] = metadata.Size
	representation.Stats["extracted_bytes"] = len([]byte(content))

	representation.Paragraphs = normalizeParagraphs(documentID, mapSlice(raw["paragraphs"]))
	representation.Tables = normalizeEntitySlice(documentID, "table", mapSlice(raw["tables"]), "index")
	representation.Sheets = normalizeSheets(documentID, mapSlice(raw["sheets"]))
	representation.Slides = normalizeSlides(documentID, mapSlice(raw["slides"]))
	representation.Pages = normalizePages(documentID, mapSlice(raw["pages"]))
	representation.Sections = normalizeEntitySlice(documentID, "section", mapSlice(raw["sections"]), "index")
	representation.Blocks = normalizeBlocks(documentID, mapSlice(raw["blocks"]))

	if len(representation.Blocks) == 0 {
		switch metadata.Format {
		case "xlsx":
			representation.Blocks = blocksFromSheets(documentID, representation.Sheets)
		case "pptx":
			representation.Blocks = blocksFromSlides(documentID, representation.Slides)
		case "pdf":
			representation.Blocks = blocksFromPages(documentID, representation.Pages)
		}
	}
	if len(representation.Sections) == 0 {
		representation.Sections = deriveSections(documentID, representation.Blocks, representation.Paragraphs)
	}
	representation.Stats["blocks"] = len(representation.Blocks)
	representation.Stats["paragraphs"] = len(representation.Paragraphs)
	representation.Stats["tables"] = len(representation.Tables)
	representation.Stats["sheets"] = len(representation.Sheets)
	representation.Stats["slides"] = len(representation.Slides)
	representation.Stats["sections"] = len(representation.Sections)
	representation.Stats["pages"] = len(representation.Pages)
	return representation, nil
}

func normalizeBlocks(documentID string, values []map[string]any) []Block {
	out := make([]Block, 0, len(values))
	for index, value := range values {
		location := cloneMap(mapValue(value["location"]))
		if len(location) == 0 {
			location = map[string]any{"part": "document", "block_index": index + 1, "path": fmt.Sprintf("document.block[%d]", index+1)}
		}
		kind := firstString(location["block_type"], value["kind"])
		if kind == "" {
			kind = "block"
		}
		key := firstString(value["id"], location["path"])
		if key == "" {
			key = fmt.Sprintf("%s:%d", kind, index+1)
		}
		format := map[string]any{}
		for _, field := range []string{"style", "level", "type"} {
			if item, ok := value[field]; ok {
				format[field] = item
			}
		}
		out = append(out, Block{ID: stableID("block", documentID+"\x00"+key), Kind: kind, Text: stringValue(value["text"]), Location: location, Format: format})
	}
	return out
}

func normalizeParagraphs(documentID string, values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for index, value := range values {
		item := cloneMap(value)
		paragraphIndex := intValue(item["index"])
		if paragraphIndex <= 0 {
			paragraphIndex = index + 1
			item["index"] = paragraphIndex
		}
		location := cloneMap(mapValue(item["location"]))
		if len(location) == 0 {
			location = map[string]any{"part": "document", "block_type": "paragraph", "paragraph_index": paragraphIndex, "path": fmt.Sprintf("document.p[%d]", paragraphIndex)}
			item["location"] = location
		}
		item["id"] = stableID("paragraph", documentID+"\x00"+firstString(location["path"], paragraphIndex))
		out = append(out, item)
	}
	return out
}

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

func normalizeSlides(documentID string, values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for offset, value := range values {
		slide := cloneMap(value)
		index := intValue(slide["index"])
		if index <= 0 {
			index = offset + 1
			slide["index"] = index
		}
		slide["id"] = stableID("slide", documentID+"\x00"+strconv.Itoa(index))
		out = append(out, slide)
	}
	return out
}

func normalizePages(documentID string, values []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for offset, value := range values {
		page := cloneMap(value)
		index := intValue(page["index"])
		if index <= 0 {
			index = offset + 1
			page["index"] = index
		}
		page["id"] = stableID("page", documentID+"\x00"+strconv.Itoa(index))
		page["location"] = map[string]any{"part": "document", "block_type": "page", "page_index": index, "path": fmt.Sprintf("document.page[%d]", index)}
		out = append(out, page)
	}
	return out
}

func normalizeEntitySlice(documentID, kind string, values []map[string]any, indexKey string) []map[string]any {
	out := make([]map[string]any, 0, len(values))
	for offset, value := range values {
		item := cloneMap(value)
		index := intValue(item[indexKey])
		if index <= 0 {
			index = offset + 1
			item[indexKey] = index
		}
		item["id"] = stableID(kind, documentID+"\x00"+strconv.Itoa(index))
		out = append(out, item)
	}
	return out
}

func blocksFromSheets(documentID string, sheets []map[string]any) []Block {
	out := []Block{}
	for _, sheet := range sheets {
		for _, row := range mapSlice(sheet["rows"]) {
			for _, cell := range mapSlice(row["cells"]) {
				text := stringValue(cell["value"])
				if strings.TrimSpace(text) == "" {
					continue
				}
				location := cloneMap(mapValue(cell["location"]))
				out = append(out, Block{ID: stableID("block", documentID+"\x00"+stringValue(location["path"])), Kind: "cell", Text: text, Location: location})
			}
		}
	}
	return out
}

func blocksFromSlides(documentID string, slides []map[string]any) []Block {
	out := []Block{}
	for _, slide := range slides {
		slideIndex := intValue(slide["index"])
		for _, item := range mapSlice(slide["items"]) {
			shapeIndex := intValue(item["shape_index"])
			typ := stringValue(item["type"])
			if typ == "text" {
				location := map[string]any{"part": "presentation", "block_type": "shape_text", "slide_index": slideIndex, "shape_index": shapeIndex, "path": fmt.Sprintf("presentation.slide[%d].shape[%d]", slideIndex, shapeIndex)}
				out = append(out, Block{ID: stableID("block", documentID+"\x00"+stringValue(location["path"])), Kind: "shape_text", Text: stringValue(item["text"]), Location: location})
			}
			if typ == "table" {
				for _, row := range mapSlice(item["rows"]) {
					rowIndex := intValue(row["index"])
					for cellIndex, cell := range anySlice(row["cells"]) {
						location := map[string]any{"part": "presentation", "block_type": "table_cell", "slide_index": slideIndex, "shape_index": shapeIndex, "row_index": rowIndex, "cell_index": cellIndex + 1, "path": fmt.Sprintf("presentation.slide[%d].shape[%d].row[%d].cell[%d]", slideIndex, shapeIndex, rowIndex, cellIndex+1)}
						out = append(out, Block{ID: stableID("block", documentID+"\x00"+stringValue(location["path"])), Kind: "table_cell", Text: stringValue(cell), Location: location})
					}
				}
			}
		}
	}
	return out
}

func blocksFromPages(documentID string, pages []map[string]any) []Block {
	out := make([]Block, 0, len(pages))
	for _, page := range pages {
		location := cloneMap(mapValue(page["location"]))
		out = append(out, Block{ID: stableID("block", documentID+"\x00"+stringValue(location["path"])), Kind: "page", Text: stringValue(page["text"]), Location: location})
	}
	return out
}

func deriveSections(documentID string, blocks []Block, paragraphs []map[string]any) []map[string]any {
	out := []map[string]any{}
	for _, paragraph := range paragraphs {
		style := strings.ToLower(stringValue(paragraph["style"]))
		if !strings.HasPrefix(style, "heading") {
			continue
		}
		location := cloneMap(mapValue(paragraph["location"]))
		out = append(out, map[string]any{"id": stableID("section", documentID+"\x00"+stringValue(location["path"])), "index": len(out) + 1, "title": stringValue(paragraph["text"]), "location": location})
	}
	if len(out) > 0 {
		return out
	}
	for _, block := range blocks {
		trimmed := strings.TrimSpace(block.Text)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		if title != "" {
			out = append(out, map[string]any{"id": stableID("section", documentID+"\x00"+block.ID), "index": len(out) + 1, "title": title, "block_id": block.ID, "location": block.Location})
		}
	}
	return out
}

func sourceForFormat(format string) string {
	switch format {
	case "text":
		return "plain_text"
	case "docx":
		return "python_docx"
	case "xlsx":
		return "exceljs"
	case "pptx":
		return "python_pptx"
	case "pdf":
		return "pypdf"
	default:
		return ""
	}
}

func stableID(kind, value string) string {
	digest := sha256.Sum256([]byte(value))
	return kind + "_" + hex.EncodeToString(digest[:8])
}

func firstString(values ...any) string {
	for _, value := range values {
		if text := strings.TrimSpace(stringValue(value)); text != "" {
			return text
		}
	}
	return ""
}

func stringValue(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case fmt.Stringer:
		return current.String()
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	case int:
		return strconv.Itoa(current)
	case nil:
		return ""
	default:
		return fmt.Sprint(current)
	}
}

func intValue(value any) int {
	switch current := value.(type) {
	case int:
		return current
	case int64:
		return int(current)
	case float64:
		return int(current)
	case string:
		parsed, _ := strconv.Atoi(strings.TrimSpace(current))
		return parsed
	default:
		return 0
	}
}

func mapValue(value any) map[string]any {
	if current, ok := value.(map[string]any); ok && current != nil {
		return current
	}
	return map[string]any{}
}

func mapSlice(value any) []map[string]any {
	switch current := value.(type) {
	case []map[string]any:
		out := make([]map[string]any, len(current))
		for index, item := range current {
			out[index] = cloneMap(item)
		}
		return out
	case []any:
		out := []map[string]any{}
		for _, item := range current {
			if object, ok := item.(map[string]any); ok {
				out = append(out, cloneMap(object))
			}
		}
		return out
	default:
		return []map[string]any{}
	}
}

func anySlice(value any) []any {
	switch current := value.(type) {
	case []any:
		return current
	case []string:
		out := make([]any, len(current))
		for index, item := range current {
			out[index] = item
		}
		return out
	default:
		return nil
	}
}

func cloneMap(source map[string]any) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
