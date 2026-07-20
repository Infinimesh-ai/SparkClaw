package document

import (
	"fmt"
	"strings"
)

const (
	LocatorDocument  = "document"
	LocatorExactText = "exact_text"
	LocatorBlock     = "block"
	LocatorParagraph = "paragraph"
	LocatorCell      = "cell"
	LocatorRow       = "row"
	LocatorSheet     = "sheet"
	LocatorSlide     = "slide"
	LocatorPages     = "pages"
)

func Locate(document Representation, request LocatorRequest) ([]Match, error) {
	kind := strings.ToLower(strings.TrimSpace(request.Kind))
	if kind == "" {
		kind = LocatorExactText
	}
	matches := []Match{}
	switch kind {
	case LocatorDocument:
		matches = append(matches, Match{BlockID: document.ID, Kind: LocatorDocument, Location: map[string]any{"path": "document"}})
	case LocatorExactText:
		query := request.Text
		if query == "" {
			return nil, &PipelineError{Code: CodeTargetNotFound, Stage: StageLocate, Format: document.Format, Detail: "exact target text is empty"}
		}
		for _, block := range document.Blocks {
			count := strings.Count(block.Text, query)
			for occurrence := 1; occurrence <= count; occurrence++ {
				matches = append(matches, Match{BlockID: block.ID, Kind: block.Kind, Text: query, Location: cloneMap(block.Location), Occurrence: occurrence})
			}
		}
	case LocatorBlock:
		for _, block := range document.Blocks {
			if request.BlockID != "" && block.ID == request.BlockID || request.LocationPath != "" && stringValue(block.Location["path"]) == request.LocationPath {
				matches = append(matches, matchFromBlock(block))
			}
		}
	case LocatorParagraph:
		for _, block := range document.Blocks {
			if intValue(block.Location["paragraph_index"]) == request.ParagraphIndex && request.ParagraphIndex > 0 {
				matches = append(matches, matchFromBlock(block))
			}
		}
	case LocatorCell:
		for _, block := range document.Blocks {
			if equalFoldValue(block.Location["sheet"], request.Sheet) && equalFoldValue(block.Location["cell"], request.Cell) {
				matches = append(matches, matchFromBlock(block))
			}
		}
		if len(matches) == 0 {
			if sheetID := namedEntityID(document.Sheets, request.Sheet); sheetID != "" && strings.TrimSpace(request.Cell) != "" {
				location := map[string]any{"part": "workbook", "block_type": "cell", "sheet": request.Sheet, "cell": request.Cell, "path": fmt.Sprintf("workbook.sheet[%s].cell[%s]", request.Sheet, request.Cell)}
				matches = append(matches, Match{BlockID: stableID("cell", sheetID+"\x00"+strings.ToUpper(request.Cell)), Kind: LocatorCell, Location: location})
			}
		}
	case LocatorRow:
		if rowID := namedRowEntityID(document.Sheets, request.Sheet, request.Row); rowID != "" {
			matches = append(matches, Match{BlockID: rowID, Kind: LocatorRow, Location: map[string]any{"sheet": request.Sheet, "row_index": request.Row, "path": fmt.Sprintf("workbook.sheet[%s].row[%d]", request.Sheet, request.Row)}})
		}
	case LocatorSheet:
		if sheetID := namedEntityID(document.Sheets, request.Sheet); sheetID != "" {
			matches = append(matches, Match{BlockID: sheetID, Kind: LocatorSheet, Location: map[string]any{"sheet": request.Sheet, "path": fmt.Sprintf("workbook.sheet[%s]", request.Sheet)}})
		}
	case LocatorSlide:
		if hasEntityIndex(document.Slides, request.SlideIndex) {
			matches = append(matches, Match{BlockID: entityID(document.Slides, request.SlideIndex), Kind: LocatorSlide, Location: map[string]any{"slide_index": request.SlideIndex, "path": fmt.Sprintf("presentation.slide[%d]", request.SlideIndex)}})
		}
	case LocatorPages:
		for _, pageIndex := range request.PageIndexes {
			if pageIndex <= 0 || !hasEntityIndex(document.Pages, pageIndex) {
				continue
			}
			matches = append(matches, Match{BlockID: entityID(document.Pages, pageIndex), Kind: "page", Location: map[string]any{"page_index": pageIndex, "path": fmt.Sprintf("document.page[%d]", pageIndex)}})
		}
	default:
		return nil, &PipelineError{Code: CodeMutationUnsupported, Stage: StageLocate, Format: document.Format, Detail: fmt.Sprintf("locator kind %q is unsupported", kind)}
	}
	if len(matches) == 0 {
		return nil, &PipelineError{Code: CodeTargetNotFound, Stage: StageLocate, Format: document.Format, Detail: "the requested target was not found in the complete structured document"}
	}
	if request.ExpectedMatches > 0 && len(matches) != request.ExpectedMatches {
		return nil, &PipelineError{
			Code: CodeMatchCountMismatch, Stage: StageConstrain, Format: document.Format,
			Detail: fmt.Sprintf("expected %d target matches, found %d", request.ExpectedMatches, len(matches)),
		}
	}
	if len(matches) > 1 && !request.AllowMultiple && request.ExpectedMatches <= 1 {
		return nil, &PipelineError{
			Code: CodeTargetAmbiguous, Stage: StageConstrain, Format: document.Format,
			Detail: fmt.Sprintf("target matched %d locations; an exact count or stable location is required", len(matches)),
		}
	}
	return matches, nil
}

func matchFromBlock(block Block) Match {
	return Match{BlockID: block.ID, Kind: block.Kind, Text: block.Text, Location: cloneMap(block.Location)}
}

func equalFoldValue(value any, expected string) bool {
	return expected != "" && strings.EqualFold(strings.TrimSpace(stringValue(value)), strings.TrimSpace(expected))
}

func hasEntityIndex(values []map[string]any, expected int) bool {
	return entityID(values, expected) != ""
}

func entityID(values []map[string]any, expected int) string {
	for _, item := range values {
		if intValue(item["index"]) == expected {
			return stringValue(item["id"])
		}
	}
	return ""
}

func namedEntityID(values []map[string]any, expected string) string {
	for _, item := range values {
		if strings.EqualFold(strings.TrimSpace(stringValue(item["name"])), strings.TrimSpace(expected)) {
			return stringValue(item["id"])
		}
	}
	return ""
}

func namedRowEntityID(sheets []map[string]any, sheetName string, rowIndex int) string {
	if rowIndex <= 0 {
		return ""
	}
	for _, sheet := range sheets {
		if !strings.EqualFold(strings.TrimSpace(stringValue(sheet["name"])), strings.TrimSpace(sheetName)) {
			continue
		}
		return entityID(mapSlice(sheet["rows"]), rowIndex)
	}
	return ""
}
