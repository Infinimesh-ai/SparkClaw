package browserautomation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Keep the semantic fields already accepted by the workflow's bounded browser
// projection. Snapshot refs/URLs/selectors never become evidence fields.
func playwrightControlEvidence(node map[string]any) (map[string]any, bool) {
	out := map[string]any{}
	truncated := false
	for _, key := range []string{"text", "value"} {
		if value, ok := node[key].(string); ok {
			runes := []rune(value)
			if len(runes) > 2048 {
				value, truncated = string(runes[:2048]), true
			}
			out[key] = value
		}
	}
	for _, key := range []string{"checked", "selected", "expanded", "pressed"} {
		if value, ok := node[key].(bool); ok {
			out[key] = value
		}
	}
	if disabled, ok := node["disabled"].(bool); ok {
		out["enabled"] = !disabled
	}
	if firstStringValue(node, "role") == "combobox" || firstStringValue(node, "role") == "listbox" {
		var options []any
		var visit func(any)
		visit = func(value any) {
			switch item := value.(type) {
			case []any:
				for _, child := range item {
					visit(child)
				}
			case map[string]any:
				if firstStringValue(item, "role") == "option" {
					if len(options) >= 64 {
						truncated = true
						return
					}
					label := []rune(firstStringValue(item, "name", "text"))
					if len(label) > 512 {
						label, truncated = label[:512], true
					}
					options = append(options, map[string]any{"name": string(label), "selected": boolValue(item["selected"]), "enabled": !boolValue(item["disabled"])})
				} else {
					visit(item["children"])
				}
			}
		}
		visit(node["children"])
		if len(options) > 0 {
			out["options"] = options
		}
	}
	return out, truncated
}

func browserFrameEvidence(snapshot any) []any {
	var frames []any
	var visit func(any)
	visit = func(value any) {
		switch node := value.(type) {
		case []any:
			for _, child := range node {
				visit(child)
			}
		case map[string]any:
			if firstStringValue(node, "role") == "iframe" {
				frames = append(frames, browserFrameSemanticNode(node))
				return
			}
			visit(node["children"])
		}
	}
	visit(snapshot)
	return frames
}

func browserFrameSemanticNode(value any) any {
	switch node := value.(type) {
	case []any:
		children := make([]any, 0, len(node))
		for _, child := range node {
			children = append(children, browserFrameSemanticNode(child))
		}
		return children
	case map[string]any:
		result := map[string]any{}
		for _, key := range []string{"role", "name", "text", "value", "checked", "selected", "expanded", "pressed", "disabled"} {
			if field, ok := node[key]; ok {
				result[key] = field
			}
		}
		if children, ok := node["children"]; ok {
			result["children"] = browserFrameSemanticNode(children)
		}
		return result
	case string:
		return node
	default:
		return nil
	}
}

func digestBrowserRenderedContent(title, text string, snapshot any) string {
	frames := browserFrameEvidence(snapshot)
	if len(frames) == 0 {
		return digestBrowserStableContent(title, text)
	}
	raw, _ := json.Marshal([]any{title, text, frames})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
