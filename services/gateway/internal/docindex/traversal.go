package docindex

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
)

const MaxReadBytes = 1 << 20
const MaxOutlineNodes = 200

type NodeFragment struct {
	document.StructureNode
	ByteStart int  `json:"byte_start"`
	ByteEnd   int  `json:"byte_end"`
	Complete  bool `json:"complete"`
	WholeNode bool `json:"whole_node"`
}

type traversalCursor struct {
	Schema     int             `json:"v"`
	Source     string          `json:"source"`
	Generation string          `json:"generation"`
	Selection  string          `json:"selection"`
	Position   int             `json:"position"`
	Offset     int             `json:"offset"`
	Request    *rangeSelection `json:"request,omitempty"`
}

func (index *Index) cursor(raw, selection string) (traversalCursor, error) {
	selection = structureHash([]string{index.AccessScope, selection})
	value := traversalCursor{Schema: 1, Source: index.Manifest.SourceSHA256, Generation: index.Manifest.StructureGeneration, Selection: selection}
	if raw == "" {
		return value, nil
	}
	if len(raw) > 8192 {
		return value, errors.New("invalid document cursor")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 2 {
		return value, errors.New("invalid document cursor signature")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return value, errors.New("invalid document cursor signature")
	}
	mac := hmac.New(sha256.New, []byte(index.cursorSecret))
	mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return value, errors.New("stale or forged document cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return value, errors.New("invalid document cursor")
	}
	var supplied traversalCursor
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&supplied) != nil || supplied.Schema != 1 || supplied.Source != value.Source || supplied.Generation != value.Generation || supplied.Selection != selection || supplied.Position < 0 || supplied.Offset < 0 {
		return value, errors.New("stale or foreign document cursor; restart the selected traversal")
	}
	return supplied, nil
}
func (index *Index) encodeCursor(value traversalCursor) string {
	raw, _ := json.Marshal(value)
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(index.cursorSecret))
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func readBudget(value int) int {
	if value <= 0 {
		return 64 << 10
	}
	return min(value, MaxReadBytes)
}
func jsonBytes(value any) int { raw, _ := json.Marshal(value); return len(raw) }
func selectionKey(request RangeRequest) string {
	return structureHash(rangeSelectionOf(request))
}

func (index *Index) readNodes(request RangeRequest) (RangeResult, error) {
	var restoreErr error
	request, restoreErr = index.restoreRange(request)
	if restoreErr != nil {
		return RangeResult{}, restoreErr
	}
	if err := index.ValidateVersion(request.SourceSHA256, request.StructureGeneration); err != nil {
		return RangeResult{}, err
	}
	selectors := 0
	for _, selected := range []bool{len(request.NodeIDs) > 0, request.All, len(request.ChunkIDs) > 0, request.PageStart > 0, request.Section != ""} {
		if selected {
			selectors++
		}
	}
	if selectors != 1 {
		return RangeResult{}, errors.New("select exactly one of node_ids, chunk_ids, all, section or page range")
	}
	// A continuation retains the actual neighbour choice of its first read.
	if request.Cursor != "" && len(request.ChunkIDs) > 0 && request.Context > 0 {
		candidate := request
		candidate.Context = 0
		if _, err := index.cursor(request.Cursor, selectionKey(candidate)); err == nil {
			request = candidate
		}
	}
	// Optional neighbours cannot displace the requested nodes.
	if request.Cursor == "" && len(request.ChunkIDs) > 0 && request.Context > 0 {
		expanded, err := index.selectedNodes(request)
		if err != nil {
			return RangeResult{}, err
		}
		cost := 0
		for _, i := range expanded {
			cost += jsonBytes(index.Nodes[i]) + len(index.Nodes[i].Text) + 128
		}
		if cost > readBudget(request.MaxBytes) {
			request.Context = 0
		}
	}
	ordinals, err := index.selectedNodes(request)
	if err != nil {
		return RangeResult{}, err
	}
	ranges := map[int][2]int{}
	for _, ordinal := range ordinals {
		ranges[ordinal] = [2]int{0, len(index.Nodes[ordinal].Text)}
	}
	if len(request.ChunkIDs) > 0 {
		chunks, err := index.rangeOrdinals(request)
		if err != nil {
			return RangeResult{}, err
		}
		byID := index.nodeMap()
		ranges = map[int][2]int{}
		for _, chunkOrdinal := range chunks {
			chunk := index.Chunks[chunkOrdinal]
			ordinal := byID[chunk.NodeID]
			prior, ok := ranges[ordinal]
			if !ok {
				ranges[ordinal] = [2]int{chunk.ByteStart, chunk.ByteEnd}
			} else {
				if chunk.ByteStart > prior[1] {
					return RangeResult{}, errors.New("noncontiguous chunks within one node require separate range reads")
				}
				ranges[ordinal] = [2]int{min(prior[0], chunk.ByteStart), max(prior[1], chunk.ByteEnd)}
			}
		}
	}
	cursor, err := index.cursor(request.Cursor, selectionKey(request))
	if err != nil {
		return RangeResult{}, err
	}
	if cursor.Position >= len(ordinals) {
		return RangeResult{}, errors.New("document cursor is outside the selected range")
	}
	result := RangeResult{SourceSHA256: index.Manifest.SourceSHA256, StructureGeneration: index.Manifest.StructureGeneration, Spans: []RangeSpan{}, Nodes: []NodeFragment{}, Dependencies: []document.StructureNode{}}
	selection := rangeSelectionOf(request)
	cursor.Request = &selection
	reserve := result
	reserve.NextCursor = index.encodeCursor(cursor)
	reserve.NextChunkID = "c00000000"
	reserve.Truncated = true
	reserve.Coverage = document.StructureCoverage{Scope: "selected_nodes", Status: "partial", Required: len(ordinals), Covered: len(ordinals), Missing: len(ordinals), DenominatorKnown: true, TaskCoverage: "not_assessed", UnrecognizedPageCount: len(ordinals), MissingIDsTruncated: true, UnrecognizedPagesTruncated: true}
	for _, ordinal := range ordinals[:min(len(ordinals), 8)] {
		reserve.Coverage.MissingNodeIDs = append(reserve.Coverage.MissingNodeIDs, index.Nodes[ordinal].ID)
		reserve.Coverage.UnrecognizedPages = append(reserve.Coverage.UnrecognizedPages, MaxSourcePages)
	}
	budget := readBudget(request.MaxBytes) - jsonBytes(reserve) - 64
	byID := index.nodeMap()
	chunksByNode := map[string][]Chunk{}
	for _, chunk := range index.Chunks {
		chunksByNode[chunk.NodeID] = append(chunksByNode[chunk.NodeID], chunk)
	}
	dependencies := map[string]bool{}
	used := 0
	position, offset := cursor.Position, cursor.Offset
	if request.Cursor == "" {
		offset = ranges[ordinals[position]][0]
	}
	for position < len(ordinals) {
		node := index.Nodes[ordinals[position]]
		selectedRange := ranges[ordinals[position]]
		if offset < selectedRange[0] || offset > selectedRange[1] || offset < len(node.Text) && !utf8.RuneStart(node.Text[offset]) {
			return RangeResult{}, errors.New("document cursor is not on a source text boundary")
		}
		mandatory := []document.StructureNode{}
		dependencyBytes := 0
		required, err := index.dependencyClosure(node, byID)
		if err != nil {
			return RangeResult{}, err
		}
		for _, i := range required {
			dependency := index.Nodes[i]
			if !dependencies[dependency.ID] {
				mandatory = append(mandatory, dependency)
				dependencyBytes += jsonBytes(dependency)
			}
		}
		fragment := NodeFragment{StructureNode: node, ByteStart: offset, ByteEnd: selectedRange[1], Complete: true, WholeNode: offset == 0 && selectedRange[1] == len(node.Text)}
		fragment.Text = node.Text[offset:selectedRange[1]]
		header := spanHeader(index.Manifest.Format, Chunk{ID: node.ID, Anchor: Anchor{Page: node.Locator.Page, Block: node.Locator.Block}}) + "\n"
		cost := jsonBytes(fragment) + jsonBytes(header+fragment.Text) + dependencyBytes + 128
		if used+cost > budget {
			if len(result.Nodes) > 0 {
				break
			}
			// Typed values must remain atomic. A too-large typed cell or mandatory
			// header is an explicit resource limitation, never a fabricated value.
			if len(node.Values) > 0 || dependencyBytes > budget/2 {
				return RangeResult{}, fmt.Errorf("selected node and required typed context exceed max_bytes; increase the read budget (node %s)", node.ID)
			}
			fragment.Text = ""
			fragment.Complete = false
			room := (budget - jsonBytes(fragment) - len(header) - dependencyBytes - 128) / 12 // JSON escaping + text projection worst case.
			if room < utf8.UTFMax {
				return RangeResult{}, errors.New("max_bytes is too small for a node fragment and its locator")
			}
			fragment.Text = boundedPrefix(node.Text[offset:selectedRange[1]], room)
			fragment.ByteEnd = offset + len(fragment.Text)
			fragment.Complete = fragment.ByteEnd == selectedRange[1]
			fragment.WholeNode = fragment.ByteStart == 0 && fragment.ByteEnd == len(node.Text)
			cost = jsonBytes(fragment) + jsonBytes(header+fragment.Text) + dependencyBytes + 128
		}
		used += cost
		for _, dependency := range mandatory {
			result.Dependencies = append(result.Dependencies, dependency)
			dependencies[dependency.ID] = true
		}
		result.Nodes = append(result.Nodes, fragment)
		if result.Text != "" {
			result.Text += "\n"
		}
		result.Text += header + fragment.Text
		chunkIDs := []string{}
		for _, chunk := range chunksByNode[node.ID] {
			if chunk.ByteEnd > fragment.ByteStart && chunk.ByteStart < fragment.ByteEnd {
				chunkIDs = append(chunkIDs, chunk.ID)
			}
		}
		result.Spans = append(result.Spans, RangeSpan{ChunkIDs: chunkIDs, Anchor: Anchor{Page: node.Locator.Page, Block: node.Locator.Block}})
		if !fragment.Complete {
			offset = fragment.ByteEnd
			break
		}
		position++
		offset = 0
		if position < len(ordinals) {
			offset = ranges[ordinals[position]][0]
		}
	}
	if position < len(ordinals) {
		cursor.Position, cursor.Offset = position, offset
		selected := rangeSelectionOf(request)
		cursor.Request = &selected
		result.NextCursor = index.encodeCursor(cursor)
		result.Truncated = true
		for _, chunk := range chunksByNode[index.Nodes[ordinals[position]].ID] {
			if chunk.ByteEnd > offset {
				result.NextChunkID = chunk.ID
				break
			}
		}
	}
	coverage := document.StructureCoverage{Scope: "selected_nodes", Status: "complete", DenominatorKnown: true, Required: len(ordinals), TaskCoverage: "not_assessed"}
	for selected, ordinal := range ordinals {
		node := index.Nodes[ordinal]
		available := node.Availability == document.NodeAvailable
		required, err := index.dependencyClosure(node, byID)
		if err != nil {
			available = false
		} else {
			for _, i := range required {
				if index.Nodes[i].Availability != document.NodeAvailable {
					available = false
				}
			}
		}
		if selected < position && available {
			coverage.Covered++
		} else {
			coverage.Missing++
			if len(coverage.MissingNodeIDs) < 8 {
				coverage.MissingNodeIDs = append(coverage.MissingNodeIDs, node.ID)
			} else {
				coverage.MissingIDsTruncated = true
			}
		}
		if node.Availability == document.NodeUnrecognized && node.Locator.Page > 0 {
			coverage.UnrecognizedPageCount++
			if len(coverage.UnrecognizedPages) < 8 {
				coverage.UnrecognizedPages = append(coverage.UnrecognizedPages, node.Locator.Page)
			} else {
				coverage.UnrecognizedPagesTruncated = true
			}
		}
	}
	if coverage.Missing > 0 {
		coverage.Status = "partial"
	}
	result.Coverage = coverage
	if jsonBytes(result) > readBudget(request.MaxBytes) {
		return RangeResult{}, errors.New("max_bytes is too small for the read metadata and selected fragments")
	}
	return result, nil
}

type OutlineRequest struct {
	Cursor, SourceSHA256, StructureGeneration string
	MaxBytes, Limit                           int
}
type OutlineResult struct {
	Nodes               []document.StructureNode   `json:"nodes"`
	NextCursor          string                     `json:"next_cursor,omitempty"`
	Coverage            document.StructureCoverage `json:"coverage"`
	SourceSHA256        string                     `json:"source_sha256"`
	StructureGeneration string                     `json:"structure_generation"`
}

func (index *Index) OutlinePage(request OutlineRequest) (OutlineResult, error) {
	if err := index.ValidateVersion(request.SourceSHA256, request.StructureGeneration); err != nil {
		return OutlineResult{}, err
	}
	cursor, err := index.cursor(request.Cursor, "outline")
	if err != nil {
		return OutlineResult{}, err
	}
	if cursor.Position > len(index.Nodes) || cursor.Offset != 0 {
		return OutlineResult{}, errors.New("invalid outline cursor position")
	}
	limit := request.Limit
	if limit <= 0 {
		limit = MaxOutlineNodes
	}
	limit = min(limit, MaxOutlineNodes)
	result := OutlineResult{Nodes: []document.StructureNode{}, SourceSHA256: index.Manifest.SourceSHA256, StructureGeneration: index.Manifest.StructureGeneration}
	reserve := result
	reserve.NextCursor = index.encodeCursor(cursor)
	reserve.Coverage = document.StructureCoverage{Scope: "structure_discovery", Status: "partial", DenominatorKnown: true, Required: len(index.Nodes), Covered: len(index.Nodes), Missing: len(index.Nodes), TaskCoverage: "not_assessed"}
	budget := readBudget(request.MaxBytes) - jsonBytes(reserve) - 32
	used, position := 0, cursor.Position
	for position < len(index.Nodes) && len(result.Nodes) < limit {
		node := index.Nodes[position]
		node.Text = ""
		node.Values = nil
		node.Title = trimRunes(node.Title, maxHeadingRunes)
		cost := jsonBytes(node)
		if used+cost > budget {
			if len(result.Nodes) == 0 {
				return OutlineResult{}, errors.New("max_bytes is too small for an outline node")
			}
			break
		}
		result.Nodes = append(result.Nodes, node)
		used += cost
		position++
	}
	result.Coverage = document.StructureCoverage{Scope: "structure_discovery", Status: "complete", DenominatorKnown: true, Required: len(index.Nodes), Covered: position, Missing: len(index.Nodes) - position, TaskCoverage: "not_assessed"}
	if position < len(index.Nodes) {
		cursor.Position = position
		result.NextCursor = index.encodeCursor(cursor)
		result.Coverage.Status = "partial"
	}
	return result, nil
}

type rangeSelection struct {
	NodeIDs            []string `json:"node_ids,omitempty"`
	All                bool     `json:"all,omitempty"`
	ChunkIDs           []string `json:"chunk_ids,omitempty"`
	PageStart          int      `json:"page_start,omitempty"`
	PageEnd            int      `json:"page_end,omitempty"`
	Section            string   `json:"section,omitempty"`
	Context            int      `json:"context,omitempty"`
	IncludeDescendants bool     `json:"include_descendants,omitempty"`
}

func rangeSelectionOf(request RangeRequest) rangeSelection {
	return rangeSelection{NodeIDs: request.NodeIDs, All: request.All, ChunkIDs: request.ChunkIDs, PageStart: request.PageStart, PageEnd: request.PageEnd, Section: request.Section, Context: request.Context, IncludeDescendants: request.IncludeDescendants}
}
func (index *Index) restoreRange(request RangeRequest) (RangeRequest, error) {
	if request.Cursor == "" || request.All || len(request.NodeIDs) > 0 || len(request.ChunkIDs) > 0 || request.PageStart > 0 || request.Section != "" {
		return request, nil
	}
	if len(request.Cursor) > 8192 {
		return request, errors.New("invalid document cursor")
	}
	parts := strings.Split(request.Cursor, ".")
	if len(parts) != 2 {
		return request, errors.New("invalid document cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return request, err
	}
	var cursor traversalCursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.Request == nil {
		return request, errors.New("cursor has no frozen range")
	}
	selected := cursor.Request
	request.NodeIDs, request.All, request.ChunkIDs, request.PageStart, request.PageEnd, request.Section, request.Context = selected.NodeIDs, selected.All, selected.ChunkIDs, selected.PageStart, selected.PageEnd, selected.Section, selected.Context
	request.IncludeDescendants = selected.IncludeDescendants
	if _, err := index.cursor(request.Cursor, selectionKey(request)); err != nil {
		return request, err
	}
	return request, nil
}
func (index *Index) dependencyClosure(node document.StructureNode, byID map[string]int) ([]int, error) {
	out := []int{}
	seen := map[string]bool{}
	visiting := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return errors.New("cyclic document dependency")
		}
		if seen[id] {
			return nil
		}
		ordinal, ok := byID[id]
		if !ok {
			return fmt.Errorf("required document dependency %s is unavailable", id)
		}
		visiting[id] = true
		for _, dependency := range index.Nodes[ordinal].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		seen[id] = true
		out = append(out, ordinal)
		return nil
	}
	for _, id := range node.Dependencies {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}
