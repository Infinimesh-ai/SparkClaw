package agent

import (
	"fmt"
	"strings"
)

func documentPageEvidence(document map[string]any) string {
	pages, ok := document["pages"].([]any)
	if !ok || len(pages) == 0 {
		return ""
	}
	lines := []string{}
	for i, item := range pages {
		if i >= 5 {
			break
		}
		page, ok := anyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(stringValue(page["text"]))
		if text == "" {
			continue
		}
		pageNumber := stringValue(page["page"])
		if pageNumber == "" || pageNumber == "<nil>" {
			pageNumber = fmt.Sprintf("%d", i+1)
		}
		lines = append(lines, fmt.Sprintf("page %s: %s", pageNumber, text))
	}
	return strings.Join(lines, "\n")
}
