// Generic string helpers shared by the grounded summary builders.
package agent

import (
	"strings"
)

func indentBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func cleanOptionalString(value any) string {
	text := strings.TrimSpace(stringValue(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func boundedContentLines(content string, maxLines, maxChars int) []string {
	if maxLines <= 0 {
		maxLines = 6
	}
	if maxChars <= 0 {
		maxChars = 900
	}
	out := []string{}
	used := 0
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			continue
		}
		if len([]rune(line)) > 220 {
			line = trimForEpisode(line, 220)
		}
		lineLen := len([]rune(line))
		if used+lineLen > maxChars && len(out) > 0 {
			break
		}
		out = append(out, line)
		used += lineLen
		if len(out) >= maxLines {
			break
		}
	}
	return out
}

func quoteInline(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "\"\""
	}
	value = strings.ReplaceAll(value, "\"", "\\\"")
	return "\"" + value + "\""
}
