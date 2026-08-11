package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var (
	docxDecisionPathPattern             = regexp.MustCompile(`(?i)document\.p\[(\d+)\]`)
	docxDecisionEnglishParagraphPattern = regexp.MustCompile(`(?i)\bparagraph\s*(?:#|number\s*|no\.?\s*)?(\d+)\b`)
	docxDecisionChineseParagraphPattern = regexp.MustCompile(`第\s*(\d+)\s*段`)
)

type docxDecisionEvidenceRecord struct {
	line      string
	bodyIndex int
}

type docxDecisionBlock struct {
	path       string
	group      string
	text       string
	bodyIndex  int
	projection map[string]any
}

func (r Runtime) workflowDOCXDecisionEvidence(ctx context.Context, run app.AgentRun, node app.WorkflowNode, entries []app.ToolDirectoryEntry) (string, error) {
	remaining := r.workflowStageEvidenceLimit()
	sections := []string{}
	for _, dependency := range node.DependsOn {
		state, ok := run.Workflow.Nodes[dependency]
		if !ok || len(state.ToolCallIDs) == 0 {
			continue
		}
		call, ref, err := r.resolveWorkflowEvidenceCall(run, workflowEvidenceRequirement{SourceNodeID: dependency})
		if err != nil {
			return "", err
		}
		output, artifactBytes, err := r.readArchivedToolObservation(ctx, run, call)
		if err != nil {
			return "", err
		}
		outputMap, ok := outputAsMap(output)
		if !ok {
			return "", errors.New("DOCX decision evidence is not a structured tool result")
		}

		projectionLimit := remaining
		var section string
		for projectionLimit > 0 {
			projection := projectDOCXDecisionEvidence(outputMap, run.Workflow.Route, entries, projectionLimit)
			if strings.TrimSpace(projection) == "" {
				projection = sliceDocumentStructuredEvidence(outputMap, projectionLimit)
			}
			if strings.TrimSpace(projection) == "" {
				break
			}
			section = formatProvisionedEvidenceSection(ref, call.Tool, workflowEvidenceStructured, projection)
			if len([]byte(section)) <= remaining {
				break
			}
			projectionLimit -= len([]byte(section)) - remaining
			section = ""
		}
		if strings.TrimSpace(section) == "" {
			return "", errors.New("required DOCX decision evidence exceeds the stage evidence budget")
		}
		sections = append(sections, section)
		used := len([]byte(section))
		remaining -= used
		r.store.AddAudit(app.AuditEvent{
			SessionID: run.SessionID,
			RunID:     run.ID,
			Actor:     "runtime",
			Type:      "workflow_step.evidence_provisioned",
			Summary:   "Provisioned target-aware DOCX evidence for operation selection",
			Fields: map[string]any{
				"source_ref": ref, "tool_call_id": call.ID, "tool": call.Tool,
				"mode": workflowEvidenceStructured, "projection": "docx_decision_v1",
				"provisioned_bytes": used, "total_artifact_bytes": artifactBytes,
			},
		})
	}
	if len(sections) == 0 {
		return "", errors.New("decision node has no completed persisted evidence source")
	}
	return strings.Join(sections, "\n\n"), nil
}

func projectDOCXDecisionEvidence(output map[string]any, route app.RouteDecision, entries []app.ToolDirectoryEntry, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	document, ok := anyMap(output["document"])
	if !ok || !strings.EqualFold(strings.TrimSpace(stringValue(document["format"])), app.DocumentFormatDOCX) {
		return ""
	}

	blocks := collectDOCXDecisionBlocks(document)
	anchorIndexes, prioritizingAnchors := matchDOCXDecisionAnchors(blocks, route)
	records := []docxDecisionEvidenceRecord{docxDecisionMetadataRecord(output, document)}
	seenBlocks := map[int]bool{}
	appendBlock := func(index int) {
		if index < 0 || index >= len(blocks) || seenBlocks[index] {
			return
		}
		seenBlocks[index] = true
		records = append(records, marshalDOCXDecisionRecord(blocks[index].projection, blocks[index].bodyIndex))
	}

	for _, index := range anchorIndexes {
		appendBlock(index)
	}
	for _, index := range anchorIndexes {
		for distance := 1; distance <= 2; distance++ {
			for _, neighbor := range []int{index - distance, index + distance} {
				if neighbor >= 0 && neighbor < len(blocks) && blocks[neighbor].group == blocks[index].group {
					appendBlock(neighbor)
				}
			}
		}
	}

	for _, index := range docxDecisionStorySamples(blocks) {
		appendBlock(index)
	}
	for _, entry := range entries {
		records = append(records, docxDecisionOperationRecord(entry))
	}
	if len(anchorIndexes) == 0 {
		for _, index := range docxDecisionHeadTailSamples(blocks) {
			appendBlock(index)
		}
	}
	for index := range blocks {
		appendBlock(index)
	}
	return packDOCXDecisionRecords(records, prioritizingAnchors, maxBytes)
}

func docxDecisionMetadataRecord(output, document map[string]any) docxDecisionEvidenceRecord {
	metadata := map[string]any{
		"record_type": "source", "untrusted": true,
		"format": document["format"],
	}
	for _, key := range []string{"truncated", "read_complete"} {
		if value, ok := output[key]; ok && usefulStructuredValue(value) {
			metadata[key] = value
		}
	}
	if enrichment, ok := anyMap(document["enrichment"]); ok {
		if coverage, ok := anyMap(enrichment["coverage"]); ok {
			metadata["coverage"] = coverage
		}
		if extensions, ok := anyMap(enrichment["extensions"]); ok {
			metadata["extensions"] = map[string]any{
				"status": extensions["status"], "unparsed_parts": extensions["unparsed_parts"],
			}
		}
	}
	if stats, ok := anyMap(document["stats"]); ok {
		metadata["stats"] = stats
	}
	return marshalDOCXDecisionRecord(metadata, 0)
}

func docxDecisionOperationRecord(entry app.ToolDirectoryEntry) docxDecisionEvidenceRecord {
	operation := strings.TrimSpace(entry.Capability.Qualifiers[app.CapabilityQualifierOperation])
	projection := map[string]any{
		"record_type": "eligible_operation", "entry_id": entry.ID, "operation": operation,
		"summary":     boundedDOCXDecisionText(entry.Summary, 600),
		"when_to_use": boundedDOCXDecisionText(entry.WhenToUse, 600),
	}
	if text := boundedDOCXDecisionText(entry.WhenNotToUse, 400); text != "" {
		projection["when_not_to_use"] = text
	}
	return marshalDOCXDecisionRecord(projection, 0)
}

func collectDOCXDecisionBlocks(document map[string]any) []docxDecisionBlock {
	blocks := []docxDecisionBlock{}
	seen := map[string]bool{}
	appendBlock := func(value any, defaultGroup, storyKind, storyPart string) {
		item, ok := anyMap(value)
		if !ok {
			return
		}
		text := strings.TrimSpace(stringValue(item["text"]))
		if text == "" || text == "<nil>" {
			return
		}
		location, _ := anyMap(item["location"])
		path := strings.TrimSpace(stringValue(firstNonNil(item["blockId"], location["path"])))
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		bodyIndex := intLikeValue(firstNonNil(location["paragraphIndex"], location["paragraph_index"], item["index"]))
		partKind := strings.TrimSpace(stringValue(firstNonNil(location["partKind"], location["part_kind"], item["part_kind"], storyKind)))
		group := defaultGroup
		if storyPart != "" {
			group = "story:" + storyPart
		} else if partKind != "" && partKind != "body" {
			group = partKind
		}
		projection := map[string]any{
			"record_type": "document_block", "candidate_id": path, "scope": partKind,
			"text": boundedDOCXDecisionText(text, 1600),
		}
		if storyPart != "" {
			projection["story_part"] = storyPart
		}
		for _, key := range []string{"type", "style", "level"} {
			if value, ok := item[key]; ok && usefulStructuredValue(value) {
				projection[key] = value
			}
		}
		blocks = append(blocks, docxDecisionBlock{
			path: path, group: group, text: text, bodyIndex: bodyIndex, projection: projection,
		})
	}

	for _, item := range documentAnySliceFromAny(document["evidence_blocks"]) {
		appendBlock(item, "body", "", "")
	}
	if len(blocks) == 0 {
		for _, item := range documentAnySliceFromAny(document["blocks"]) {
			appendBlock(item, "body", "", "")
		}
	}
	if len(blocks) == 0 {
		for _, item := range documentAnySliceFromAny(document["paragraphs"]) {
			appendBlock(item, "body", "", "")
		}
	}
	if enrichment, ok := anyMap(document["enrichment"]); ok {
		if extensions, ok := anyMap(enrichment["extensions"]); ok {
			for _, value := range documentAnySliceFromAny(extensions["story_parts"]) {
				story, ok := anyMap(value)
				if !ok {
					continue
				}
				kind := strings.TrimSpace(stringValue(story["kind"]))
				part := strings.TrimSpace(stringValue(story["part_name"]))
				for _, item := range documentAnySliceFromAny(story["blocks"]) {
					appendBlock(item, "story:"+part, kind, part)
				}
			}
		}
	}
	return blocks
}

func matchDOCXDecisionAnchors(blocks []docxDecisionBlock, route app.RouteDecision) ([]int, []string) {
	requestedIndexes := map[int]bool{}
	requestedPaths := map[string]bool{}
	query := route.Slots.Query
	for _, factKey := range []string{"document_location", "target_location", "location"} {
		if path := strings.TrimSpace(route.Facts[factKey]); path != "" {
			requestedPaths[strings.ToLower(path)] = true
			query += " " + path
		}
	}
	for _, pattern := range []*regexp.Regexp{docxDecisionPathPattern, docxDecisionEnglishParagraphPattern, docxDecisionChineseParagraphPattern} {
		for _, match := range pattern.FindAllStringSubmatch(query, -1) {
			if index, err := strconv.Atoi(match[1]); err == nil && index > 0 {
				requestedIndexes[index] = true
			}
		}
	}
	quotes := quotedDOCXDecisionAnchors(query)
	matched := []int{}
	anchors := []string{}
	seenIndex := map[int]bool{}
	seenAnchor := map[string]bool{}
	appendAnchor := func(index int, label string) {
		if !seenIndex[index] {
			seenIndex[index] = true
			matched = append(matched, index)
		}
		if label != "" && !seenAnchor[label] {
			seenAnchor[label] = true
			anchors = append(anchors, label)
		}
	}
	for index, block := range blocks {
		if requestedIndexes[block.bodyIndex] && block.bodyIndex > 0 {
			appendAnchor(index, "paragraph:"+strconv.Itoa(block.bodyIndex))
		}
		if requestedPaths[strings.ToLower(block.path)] {
			appendAnchor(index, "location:"+block.path)
		}
		normalizedText := normalizeDOCXDecisionText(block.text)
		for _, quote := range quotes {
			if strings.Contains(normalizedText, quote.normalized) {
				appendAnchor(index, "quote:"+quote.original)
			}
		}
	}
	sort.Ints(matched)
	return matched, anchors
}

type docxDecisionQuote struct {
	original   string
	normalized string
}

func quotedDOCXDecisionAnchors(text string) []docxDecisionQuote {
	pairs := [][2]rune{{'"', '"'}, {'\'', '\''}, {'“', '”'}, {'‘', '’'}, {'「', '」'}, {'『', '』'}}
	runes := []rune(text)
	quotes := []docxDecisionQuote{}
	seen := map[string]bool{}
	for _, pair := range pairs {
		for start := 0; start < len(runes); start++ {
			if runes[start] != pair[0] {
				continue
			}
			for end := start + 1; end < len(runes); end++ {
				if runes[end] != pair[1] {
					continue
				}
				original := strings.TrimSpace(string(runes[start+1 : end]))
				normalized := normalizeDOCXDecisionText(original)
				if len([]rune(normalized)) >= 2 && !seen[normalized] {
					seen[normalized] = true
					quotes = append(quotes, docxDecisionQuote{original: original, normalized: normalized})
				}
				start = end
				break
			}
		}
	}
	return quotes
}

func normalizeDOCXDecisionText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func docxDecisionStorySamples(blocks []docxDecisionBlock) []int {
	first := map[string]int{}
	last := map[string]int{}
	groups := []string{}
	for index, block := range blocks {
		if !strings.HasPrefix(block.group, "story:") {
			continue
		}
		if _, ok := first[block.group]; !ok {
			first[block.group] = index
			groups = append(groups, block.group)
		}
		last[block.group] = index
	}
	indexes := []int{}
	for _, group := range groups {
		indexes = append(indexes, first[group])
		if last[group] != first[group] {
			indexes = append(indexes, last[group])
		}
	}
	return indexes
}

func docxDecisionHeadTailSamples(blocks []docxDecisionBlock) []int {
	body := []int{}
	for index, block := range blocks {
		if block.group == "body" {
			body = append(body, index)
		}
	}
	indexes := []int{}
	for index := 0; index < len(body) && index < 2; index++ {
		indexes = append(indexes, body[index])
	}
	for index := max(2, len(body)-2); index < len(body); index++ {
		indexes = append(indexes, body[index])
	}
	return indexes
}

func marshalDOCXDecisionRecord(value map[string]any, bodyIndex int) docxDecisionEvidenceRecord {
	raw, _ := json.Marshal(value)
	return docxDecisionEvidenceRecord{line: string(raw), bodyIndex: bodyIndex}
}

func packDOCXDecisionRecords(records []docxDecisionEvidenceRecord, anchors []string, maxBytes int) string {
	if len(records) == 0 || maxBytes <= 0 {
		return ""
	}
	selected := []int{}
	selectedBytes := 0
	baseSummary := docxDecisionProjectionSummary(records, nil, anchors, 0)
	reserve := len(baseSummary) + min(256, maxBytes/8)
	available := maxBytes - reserve
	if available < 0 {
		available = 0
	}
	for index, record := range records {
		cost := len([]byte(record.line))
		if len(selected) > 0 {
			cost++
		}
		if cost <= available-selectedBytes {
			selected = append(selected, index)
			selectedBytes += cost
		}
	}
	for {
		text := renderDOCXDecisionProjection(records, selected, anchors)
		if len([]byte(text)) <= maxBytes {
			return text
		}
		if len(selected) == 0 {
			return ""
		}
		selected = selected[:len(selected)-1]
	}
}

func renderDOCXDecisionProjection(records []docxDecisionEvidenceRecord, selected []int, anchors []string) string {
	summary := docxDecisionProjectionSummary(records, selected, anchors, 0)
	lines := make([]string, 0, len(selected)+1)
	lines = append(lines, summary)
	for _, index := range selected {
		lines = append(lines, records[index].line)
	}
	text := strings.Join(lines, "\n")
	for attempts := 0; attempts < 4; attempts++ {
		nextSummary := docxDecisionProjectionSummary(records, selected, anchors, len([]byte(text)))
		if nextSummary == lines[0] {
			break
		}
		lines[0] = nextSummary
		text = strings.Join(lines, "\n")
	}
	return text
}

func docxDecisionProjectionSummary(records []docxDecisionEvidenceRecord, selected []int, anchors []string, bytesUsed int) string {
	selectedSet := map[int]bool{}
	for _, index := range selected {
		selectedSet[index] = true
	}
	summary := map[string]any{
		"projection": "docx_decision_v1", "selected_records": len(selected),
		"omitted_records": len(records) - len(selected), "bytes_used": bytesUsed,
		"prioritizing_anchors": anchors, "omitted_ranges": omittedDOCXDecisionRanges(records, selectedSet),
	}
	raw, _ := json.Marshal(summary)
	return string(raw)
}

func omittedDOCXDecisionRanges(records []docxDecisionEvidenceRecord, selected map[int]bool) []string {
	indexes := []int{}
	seen := map[int]bool{}
	for index, record := range records {
		if selected[index] || record.bodyIndex <= 0 || seen[record.bodyIndex] {
			continue
		}
		seen[record.bodyIndex] = true
		indexes = append(indexes, record.bodyIndex)
	}
	sort.Ints(indexes)
	ranges := []string{}
	for start := 0; start < len(indexes); {
		end := start
		for end+1 < len(indexes) && indexes[end+1] == indexes[end]+1 {
			end++
		}
		if start == end {
			ranges = append(ranges, fmt.Sprintf("document.p[%d]", indexes[start]))
		} else {
			ranges = append(ranges, fmt.Sprintf("document.p[%d]..document.p[%d]", indexes[start], indexes[end]))
		}
		start = end + 1
	}
	return ranges
}

func boundedDOCXDecisionText(text string, maxBytes int) string {
	text = strings.TrimSpace(text)
	if len([]byte(text)) <= maxBytes {
		return text
	}
	return boundedUTF8Prefix([]byte(text), maxBytes)
}
