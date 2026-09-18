package emailmanagement

import "strings"

var legacyCSSMarkers = []string{"@font-face", ".ExternalClass", "#outlook", "mso-table-", "-webkit-text-size-adjust"}

// Older HTML-only representations were converted to text before style blocks
// were removed. Preserve the stored representation, but exclude its bounded CSS
// preamble from every model input just as the conversation reader excludes it.
func emailModelBodyText(value string) string {
	offsets := []int{}
	for _, marker := range legacyCSSMarkers {
		if offset := strings.Index(value, marker); offset >= 0 {
			offsets = append(offsets, offset)
		}
	}
	if len(offsets) < 2 {
		return value
	}
	start := offsets[0]
	for _, offset := range offsets[1:] {
		if offset < start {
			start = offset
		}
	}
	cursor, end := start, start
	for cursor < len(value) {
		relativeOpen := strings.IndexByte(value[cursor:], '{')
		if relativeOpen < 0 {
			break
		}
		open := cursor + relativeOpen
		if !legacyCSSHeader(value[cursor:open]) {
			break
		}
		depth, close := 0, -1
		for index := open; index < len(value); index++ {
			switch value[index] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					close = index
				}
			}
			if close >= 0 {
				break
			}
		}
		if close < 0 {
			break
		}
		end = close + 1
		cursor = end
		for cursor < len(value) && strings.ContainsRune(" \t\r\n", rune(value[cursor])) {
			cursor++
		}
	}
	if end == start {
		return value
	}
	lines := strings.Split(value[:start]+value[end:], "\n")
	for index := range lines {
		lines[index] = strings.TrimSpace(lines[index])
	}
	return strings.TrimSpace(collapseBlankLines(strings.Join(lines, "\n")))
}

func legacyCSSHeader(value string) bool {
	lines := strings.FieldsFunc(value, func(r rune) bool { return r == '\r' || r == '\n' })
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		first := line[0]
		if strings.ContainsRune(".@#[*:", rune(first)) {
			continue
		}
		if first < 'a' || first > 'z' {
			return false
		}
		for _, r := range strings.TrimSuffix(line, ",") {
			if r != '-' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
				return false
			}
		}
	}
	return true
}

func collapseBlankLines(value string) string {
	for strings.Contains(value, "\n\n\n") {
		value = strings.ReplaceAll(value, "\n\n\n", "\n\n")
	}
	return value
}
