package docindex

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	chunkTargetRunes  = 1000
	chunkOverlapRunes = 100
	maxHeadingRunes   = 200
)

// assembler turns ordered units into chunks and outline nodes. Headings
// always start a new chunk; long runs of text are split with a small overlap
// so a sentence cut at a boundary stays searchable.
type assembler struct {
	chunks      []Chunk
	nodes       []OutlineNode
	headingPath []string
	buffer      strings.Builder
	bufferRunes int
	// overlapOnly marks a buffer holding only text carried from the previous
	// chunk; it is never emitted on its own.
	overlapOnly bool
	startPage   int
	lastPage    int
	firstBlock  string
	textBytes   int
}

func (a *assembler) add(unit Unit) {
	// NFKC folds compatibility forms that PDF text layers often carry, such
	// as Kangxi radicals (⽅) for ideographs (方) and full-width letters.
	text := strings.TrimSpace(norm.NFKC.String(unit.Text))
	if text == "" {
		return
	}
	a.textBytes += len(text)
	if unit.HeadingLevel > 0 {
		a.flush(false)
		title := trimRunes(strings.Join(strings.Fields(text), " "), maxHeadingRunes)
		level := unit.HeadingLevel
		if level > len(a.headingPath)+1 {
			level = len(a.headingPath) + 1
		}
		a.headingPath = append(a.headingPath[:level-1], title)
		a.nodes = append(a.nodes, OutlineNode{
			ID: fmt.Sprintf("n%04d", len(a.nodes)+1), Title: title, Level: level, Page: unit.Page, ChunkStart: len(a.chunks),
		})
	}
	runes := []rune(text)
	for len(runes) > 0 {
		room := chunkTargetRunes - a.bufferRunes
		if room <= 0 {
			a.flush(true)
			continue
		}
		take := min(room, len(runes))
		if take < len(runes) && !a.overlapOnly && a.bufferRunes > 0 && len(runes) <= chunkTargetRunes-chunkOverlapRunes {
			// The whole unit fits the next chunk: start it there unsplit.
			a.flush(true)
			continue
		}
		a.append(string(runes[:take]), unit)
		runes = runes[take:]
	}
}

func (a *assembler) append(text string, unit Unit) {
	if a.bufferRunes == 0 {
		a.startPage = unit.Page
		a.firstBlock = unit.Block
	} else {
		a.buffer.WriteString("\n")
		a.bufferRunes++
	}
	if unit.Page > 0 {
		a.lastPage = unit.Page
		if a.startPage == 0 {
			a.startPage = unit.Page
		}
	}
	a.buffer.WriteString(text)
	a.bufferRunes += utf8.RuneCountInString(text)
	a.overlapOnly = false
}

func (a *assembler) flush(overlap bool) {
	if a.bufferRunes == 0 || a.overlapOnly {
		a.buffer.Reset()
		a.bufferRunes, a.overlapOnly = 0, false
		a.startPage, a.firstBlock = 0, ""
		return
	}
	text := a.buffer.String()
	anchor := Anchor{Page: a.startPage, HeadingPath: append([]string(nil), a.headingPath...), Block: a.firstBlock}
	if a.lastPage > a.startPage {
		anchor.PageEnd = a.lastPage
	}
	a.chunks = append(a.chunks, Chunk{ID: fmt.Sprintf("c%05d", len(a.chunks)+1), Text: text, Anchor: anchor})
	a.buffer.Reset()
	a.bufferRunes = 0
	a.startPage, a.firstBlock = 0, ""
	if overlap {
		runes := []rune(text)
		tail := string(runes[max(0, len(runes)-chunkOverlapRunes):])
		a.startPage = a.lastPage
		a.buffer.WriteString(tail)
		a.bufferRunes = utf8.RuneCountInString(tail)
		a.overlapOnly = true
	}
}

// finish closes the last chunk, places format-level outline entries (PDF
// bookmarks) on chunks by page, and computes every node's chunk range.
func (a *assembler) finish(info SourceInfo) ([]Chunk, []OutlineNode) {
	a.flush(false)
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
		title := trimRunes(strings.Join(strings.Fields(entry.Title), " "), maxHeadingRunes)
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
