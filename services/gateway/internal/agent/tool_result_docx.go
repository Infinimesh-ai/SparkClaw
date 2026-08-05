package agent

import (
	"fmt"
	"strings"
)

func documentParagraphEvidence(document map[string]any) string {
	paragraphs, ok := document["paragraphs"].([]any)
	if !ok || len(paragraphs) == 0 {
		return ""
	}
	lines := []string{}
	for i, item := range paragraphs {
		paragraph, ok := anyMap(item)
		if !ok {
			continue
		}
		text := strings.TrimSpace(stringValue(paragraph["text"]))
		if text == "" {
			continue
		}
		index := stringValue(paragraph["index"])
		if index == "" || index == "<nil>" {
			index = fmt.Sprintf("%d", i+1)
		}
		lines = append(lines, fmt.Sprintf("paragraph %s: %s", index, text))
	}
	return strings.Join(lines, "\n")
}
