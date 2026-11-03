package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	sampledEvidenceMinWindowBytes = 2048
	sampledEvidenceMaxWindows     = 64
	// sampledEvidenceMarkerBytes reserves room for one omission marker per
	// window.
	sampledEvidenceMarkerBytes = 48
)

// sampledDocumentEvidence returns text unchanged when it fits budget bytes.
// Otherwise it keeps evenly spaced windows across the whole document instead
// of only its opening, so a summary of a truncated document still sees every
// part of it. Windows start at a line, paragraph or page boundary when one is
// near, and omitted spans are marked with their byte counts.
func sampledDocumentEvidence(text string, budget int) string {
	if len(text) <= budget {
		return text
	}
	if budget < 2*sampledEvidenceMinWindowBytes {
		return boundedUTF8Prefix([]byte(text), budget)
	}
	windows := min(sampledEvidenceMaxWindows, budget/sampledEvidenceMinWindowBytes)
	share := budget/windows - sampledEvidenceMarkerBytes
	// The first window starts at the beginning and the last ends at the end.
	step := (len(text) - share) / (windows - 1)
	var out strings.Builder
	covered := 0
	for index := 0; index < windows; index++ {
		offset := index * step
		if index == windows-1 {
			offset = sampledTailStart(text, share)
		}
		start := max(covered, sampledWindowStart(text, offset, step/2))
		window := sampledWindow(text[start:], share)
		if window == "" {
			continue
		}
		if start > covered {
			out.WriteString(sampledOmissionMarker(start - covered))
		}
		out.WriteString(window)
		covered = start + len(window)
	}
	if covered < len(text) {
		out.WriteString(sampledOmissionMarker(len(text) - covered))
	}
	return boundedUTF8Prefix([]byte(out.String()), budget)
}

// sampledWindowStart moves offset forward to just past the next blank line
// (a paragraph or PDF page break) within reach bytes, else past the next
// newline, else to the next rune start.
func sampledWindowStart(text string, offset, reach int) int {
	if offset <= 0 {
		return 0
	}
	if limit := min(len(text), offset+reach); offset < limit {
		if paragraph := strings.Index(text[offset:limit], "\n\n"); paragraph >= 0 {
			return offset + paragraph + 2
		}
		if newline := strings.IndexByte(text[offset:limit], '\n'); newline >= 0 {
			return offset + newline + 1
		}
	}
	for offset < len(text) && !utf8.RuneStart(text[offset]) {
		offset++
	}
	return offset
}

// sampledTailStart is the start of the last window: the blank line or
// newline nearest before the final share bytes, so the window ends with the
// document.
func sampledTailStart(text string, share int) int {
	offset := len(text) - share
	if paragraph := strings.Index(text[offset:], "\n\n"); paragraph >= 0 && paragraph < share/2 {
		return offset + paragraph
	}
	return offset
}

// sampledWindow takes at most share bytes, ending at the last newline when it
// falls in the final quarter of the window.
func sampledWindow(text string, share int) string {
	window := boundedUTF8Prefix([]byte(text), share)
	if len(window) < len(text) {
		if newline := strings.LastIndexByte(window, '\n'); newline >= len(window)*3/4 {
			window = window[:newline+1]
		}
	}
	return window
}

func sampledOmissionMarker(bytes int) string {
	return fmt.Sprintf("\n[... %d bytes omitted ...]\n", bytes)
}
