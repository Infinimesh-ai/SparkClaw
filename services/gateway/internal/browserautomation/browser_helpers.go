package browserautomation

import (
	"fmt"
	"net/url"
	"strings"
)

func browserURLsShareOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || left.Hostname() == "" || right.Hostname() == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		left.Port() == right.Port()
}

func normalizedPagesOutput(pages []any) map[string]any {
	lines := []string{fmt.Sprintf("%d browser tab(s)", len(pages))}
	for _, raw := range pages {
		page := mapValue(raw)
		lines = append(lines, fmt.Sprintf("- page_id=%s selected=%t url=%s title=%q",
			firstStringValue(page, "page_id"), boolValue(page["selected"]),
			firstStringValue(page, "url"), firstStringValue(page, "title")))
	}
	return map[string]any{"pages": pages, "text": strings.Join(lines, "\n")}
}

func splitBrowserProfileKey(key string) (string, string) {
	ownerID, profileID, found := strings.Cut(key, "\x00")
	if !found {
		return "", strings.TrimSpace(key)
	}
	return strings.TrimSpace(ownerID), strings.TrimSpace(profileID)
}

func browserSelectValues(args map[string]any) []string {
	if raw, ok := args["values"].([]any); ok {
		values := make([]string, 0, len(raw))
		for _, item := range raw {
			if value := strings.TrimSpace(stringValue(item)); value != "" {
				values = append(values, value)
			}
		}
		if len(values) > 0 {
			return values
		}
	}
	if raw, ok := args["values"].([]string); ok && len(raw) > 0 {
		return append([]string{}, raw...)
	}
	if value := strings.TrimSpace(stringArg(args, "value")); value != "" {
		return []string{value}
	}
	return nil
}

func browserResultArguments(args map[string]any) map[string]any {
	result := cloneArgs(args)
	delete(result, "reason")
	return result
}
