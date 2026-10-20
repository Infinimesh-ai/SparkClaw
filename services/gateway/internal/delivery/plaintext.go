package delivery

import (
	"regexp"
	"slices"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Only the supported trailing disclosure block is expanded. Arbitrary HTML,
// incomplete blocks and nested disclosures stay literal, as in the Web client.
var trailingDisclosure = regexp.MustCompile(`(?s)^(.+?)\n\n<details>\n<summary>[^<>\n]*</summary>\n\n(.+)\n</details>\s*$`)
var disclosureSourceLink = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)

func plainTextDeliveryContent(content app.MessageContent) app.MessageContent {
	content.Parts = slices.Clone(content.Parts)
	for index, part := range content.Parts {
		if part.Kind != app.MessagePartText {
			continue
		}
		match := trailingDisclosure.FindStringSubmatch(part.Text)
		if len(match) != 3 || strings.Contains(match[1], "<details>") ||
			strings.Contains(match[2], "<details>") || strings.Contains(match[2], "</details>") {
			continue
		}
		details := disclosureSourceLink.ReplaceAllString(match[2], "$1：$2")
		content.Parts[index].Text = strings.TrimSpace(match[1]) + "\n\n" + strings.TrimSpace(details)
	}
	return content
}
