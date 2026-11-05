package docindex

import (
	"fmt"
	"sort"
)

const (
	chunkTargetRunes = 1000
	maxHeadingRunes  = 200
)

// assembler turns ordered units into chunks and outline nodes. Headings
// always start a new chunk; long runs of text are split with a small overlap
// so a sentence cut at a boundary stays searchable.
type assembler struct {
	chunks      []Chunk
	nodes       []OutlineNode
	headingPath []string
	textBytes   int
}

// Canonical source units are never normalized or overlapped. Search applies its
// own normalization, and chunk ranges always address exact bytes in one node.
func (a *assembler) add(unit Unit) {
	text := unit.Text
	if text == "" {
		return
	}
	a.textBytes += len(text)
	if unit.HeadingLevel > 0 {
		level := min(unit.HeadingLevel, len(a.headingPath)+1)
		title := unit.Title
		if title == "" {
			title = text
		}
		title = trimRunes(title, maxHeadingRunes)
		a.headingPath = append(a.headingPath[:level-1], title)
		id := unit.NodeID
		if id == "" {
			id = fmt.Sprintf("n%04d", len(a.nodes)+1)
		}
		a.nodes = append(a.nodes, OutlineNode{ID: id, Title: title, Level: level, Page: unit.Page, ChunkStart: len(a.chunks)})
	}
	offset := 0
	for len(text) > 0 {
		cut, count := len(text), 0
		for byteAt := range text {
			if count == chunkTargetRunes {
				cut = byteAt
				break
			}
			count++
		}
		piece := text[:cut]
		a.chunks = append(a.chunks, Chunk{ID: fmt.Sprintf("c%05d", len(a.chunks)+1), NodeID: unit.NodeID, ByteStart: offset, ByteEnd: offset + len(piece), Text: piece, SearchText: func() string {
			if offset == 0 {
				return boundedPrefix(unit.SearchText, 4096)
			}
			return ""
		}(), Anchor: Anchor{Page: unit.Page, HeadingPath: append([]string(nil), a.headingPath...), Block: unit.Block}})
		offset += len(piece)
		text = text[len(piece):]
	}
}

// finish closes the last chunk, places format-level outline entries (PDF
// bookmarks) on chunks by page, and computes every node's chunk range.
func (a *assembler) finish(info SourceInfo) ([]Chunk, []OutlineNode) {
	if len(a.nodes) == 0 && len(info.Outline) > 0 {
		a.nodes, a.chunks = pageOutline(info.Outline, a.chunks)
	}
	for index := range a.nodes {
		if a.nodes[index].Page <= 0 {
			continue
		}
		end := info.Pages
		for next := index + 1; next < len(a.nodes); next++ {
			if a.nodes[next].Level <= a.nodes[index].Level {
				end = a.nodes[next].Page - 1
				break
			}
		}
		a.nodes[index].PageEnd = max(end, a.nodes[index].Page)
	}
	for index := range a.nodes {
		end := len(a.chunks)
		for next := index + 1; next < len(a.nodes); next++ {
			if a.nodes[next].Level <= a.nodes[index].Level {
				end = a.nodes[next].ChunkStart
				break
			}
		}
		a.nodes[index].ChunkEnd = max(end, a.nodes[index].ChunkStart)
	}
	return a.chunks, a.nodes
}

// pageOutline turns page-addressed outline entries into nodes and stamps each
// chunk with the heading path in force on its first page.
func pageOutline(entries []OutlineEntry, chunks []Chunk) ([]OutlineNode, []Chunk) {
	ordered := append([]OutlineEntry(nil), entries...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Page < ordered[j].Page })
	nodes := make([]OutlineNode, 0, len(ordered))
	path := []string{}
	next := 0
	for _, entry := range ordered {
		title := trimRunes(entry.Title, maxHeadingRunes)
		if title == "" {
			continue
		}
		level := max(1, min(entry.Level, len(path)+1))
		for next < len(chunks) && chunkLastPage(chunks[next]) < entry.Page {
			next++
		}
		nodes = append(nodes, OutlineNode{
			ID: fmt.Sprintf("n%04d", len(nodes)+1), Title: title, Level: level, Page: entry.Page, ChunkStart: next,
		})
		path = append(path[:level-1], title)
	}
	path = path[:0]
	cursor := 0
	for index := range chunks {
		for cursor < len(nodes) && nodes[cursor].Page <= max(chunks[index].Anchor.Page, 1) {
			path = append(path[:nodes[cursor].Level-1], nodes[cursor].Title)
			cursor++
		}
		chunks[index].Anchor.HeadingPath = append([]string(nil), path...)
	}
	return nodes, chunks
}

func chunkLastPage(chunk Chunk) int {
	return max(chunk.Anchor.Page, chunk.Anchor.PageEnd)
}

func trimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
