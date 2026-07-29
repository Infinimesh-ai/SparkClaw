package browserautomation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Adapter interface {
	Call(ctx context.Context, tool string, args map[string]any) (Result, error)
	ReadPage(ctx context.Context, url string, args map[string]any) (PageReadResult, error)
	Health(ctx context.Context, args map[string]any) (Result, error)
	// Close terminates any subprocess the adapter spawned. It must be safe to
	// call multiple times and when no session was ever started.
	Close() error
}

type Result struct {
	Tool               string         `json:"tool"`
	RawTool            string         `json:"raw_tool,omitempty"`
	Arguments          map[string]any `json:"arguments,omitempty"`
	Output             any            `json:"output,omitempty"`
	Text               string         `json:"text,omitempty"`
	Pages              []any          `json:"pages"`
	ScreenshotPath     string         `json:"screenshot_path,omitempty"`
	ScreenshotMarkdown string         `json:"screenshot_markdown,omitempty"`
	BrowserMode        string         `json:"browser_mode,omitempty"`
	Presentation       string         `json:"presentation,omitempty"`
	SurfaceVisible     bool           `json:"surface_visible,omitempty"`
	SessionGeneration  uint64         `json:"session_generation,omitempty"`
	ProviderSessionRef string         `json:"provider_session_ref,omitempty"`
	Untrusted          bool           `json:"untrusted"`
	Provider           string         `json:"provider"`
	DurationMS         int64          `json:"duration_ms"`
}

type PageReadResult struct {
	URL                   string         `json:"url"`
	FinalURL              string         `json:"final_url,omitempty"`
	Title                 string         `json:"title,omitempty"`
	HTML                  string         `json:"html,omitempty"`
	Text                  string         `json:"text,omitempty"`
	ContentType           string         `json:"content_type,omitempty"`
	ReadyState            string         `json:"ready_state,omitempty"`
	Lang                  string         `json:"lang,omitempty"`
	Rendered              bool           `json:"rendered"`
	HTMLLength            int            `json:"html_length,omitempty"`
	HTMLTruncated         bool           `json:"html_truncated,omitempty"`
	TextLength            int            `json:"text_length,omitempty"`
	ScrollHeight          int            `json:"scroll_height,omitempty"`
	AuthState             string         `json:"auth_state,omitempty"`
	AuthConfidence        string         `json:"auth_confidence,omitempty"`
	AuthSignals           []string       `json:"auth_signals,omitempty"`
	AuthChallengeDetected bool           `json:"auth_challenge_detected,omitempty"`
	SnapshotText          string         `json:"snapshot_text,omitempty"`
	Snapshot              any            `json:"snapshot,omitempty"`
	Actions               []string       `json:"actions,omitempty"`
	ReadSource            string         `json:"read_source,omitempty"`
	TextTruncated         bool           `json:"text_truncated,omitempty"`
	ReadMode              string         `json:"read_mode,omitempty"`
	BrowserMode           string         `json:"browser_mode,omitempty"`
	Presentation          string         `json:"presentation,omitempty"`
	SurfaceVisible        bool           `json:"surface_visible,omitempty"`
	SessionGeneration     uint64         `json:"session_generation,omitempty"`
	ProviderSessionRef    string         `json:"provider_session_ref,omitempty"`
	Provider              string         `json:"provider"`
	DurationMS            int64          `json:"duration_ms"`
	Untrusted             bool           `json:"untrusted"`
	Errors                map[string]any `json:"errors,omitempty"`
}

func isAboutBlank(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "about:blank")
}

func shouldUseHiddenBrowserSession(metadata browserModeFields, args map[string]any) bool {
	if metadata.Presentation != "hidden" || metadata.SurfaceVisible {
		return false
	}
	if boolArg(args, "visible_browser") || boolArg(args, "disable_hidden_browser") {
		return false
	}
	return true
}

func shouldUseHiddenAutomationTool(tool string, metadata browserModeFields, args map[string]any) bool {
	return tool != "browser.status" && shouldUseHiddenBrowserSession(metadata, args)
}

func browserProviderName(hidden bool) string {
	if hidden {
		return "agent-browser-headless"
	}
	return "agent-browser-visible"
}

type browserModeFields struct {
	BrowserMode    string
	Presentation   string
	SurfaceVisible bool
}

func browserModeMetadata(args map[string]any, fallbackMode string) browserModeFields {
	mode := strings.ToLower(strings.TrimSpace(stringArg(args, "browser_mode")))
	if mode != "autonomous" && mode != "collaborative" {
		mode = strings.ToLower(strings.TrimSpace(fallbackMode))
	}
	if mode != "autonomous" && mode != "collaborative" {
		mode = "autonomous"
	}
	presentation := strings.ToLower(strings.TrimSpace(stringArg(args, "presentation")))
	if presentation != "hidden" && presentation != "visible" {
		if boolArg(args, "visible_browser") || boolArg(args, "disable_hidden_browser") {
			presentation = "visible"
		} else {
			presentation = "hidden"
		}
	}
	surfaceVisible := presentation == "visible"
	if _, ok := args["surface_visible"]; ok {
		surfaceVisible = boolArg(args, "surface_visible")
	}
	return browserModeFields{
		BrowserMode:    mode,
		Presentation:   presentation,
		SurfaceVisible: surfaceVisible,
	}
}

func shouldUseTypeText(args map[string]any) bool {
	if hasElementRef(args) {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(stringArg(args, "mode")))
	if mode == "type_text" || mode == "focused" || mode == "chat" || mode == "search" || mode == "rich_text" {
		return true
	}
	if boolArg(args, "focused") || boolArg(args, "current_focus") || boolArg(args, "rich_text") {
		return true
	}
	targetKind := strings.ToLower(strings.TrimSpace(stringArg(args, "target_kind")))
	return targetKind == "chat" || targetKind == "search" || targetKind == "rich_text"
}

func hasElementRef(args map[string]any) bool {
	return strings.TrimSpace(stringArg(args, "uid")) != "" || strings.TrimSpace(stringArg(args, "ref")) != ""
}

func cloneArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	return out
}

func contentText(output any) string {
	result, ok := output.(map[string]any)
	if !ok {
		return ""
	}
	if text, ok := result["text"].(string); ok {
		return text
	}
	content, ok := result["content"].([]any)
	if !ok {
		return ""
	}
	parts := []string{}
	for _, item := range content {
		obj, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if text, ok := obj["text"].(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func mapValue(value any) map[string]any {
	if value == nil {
		return nil
	}
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
}

func firstStringValue(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if text := strings.TrimSpace(stringValue(values[key])); text != "" && text != "<nil>" {
			return text
		}
	}
	return ""
}

func firstStringSliceValue(values ...any) []string {
	for _, value := range values {
		var items []any
		switch typed := value.(type) {
		case []any:
			items = typed
		case []string:
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if item = strings.TrimSpace(item); item != "" {
					out = append(out, item)
				}
			}
			if len(out) > 0 {
				return out
			}
		}
		out := make([]string, 0, len(items))
		for _, item := range items {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				out = append(out, text)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func intArg(args map[string]any, key string, fallback int) int {
	if args == nil {
		return fallback
	}
	value, ok := args[key]
	if !ok {
		return fallback
	}
	parsed := intValue(value)
	if parsed == 0 {
		return fallback
	}
	return parsed
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case float32:
		return int(v)
	case json.Number:
		parsed, _ := v.Int64()
		return int(parsed)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &parsed); err == nil {
			return parsed
		}
	}
	return 0
}

func boolValue(value any) bool {
	if b, ok := value.(bool); ok {
		return b
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(value)), "true")
}

func pagesFromOutput(tool string, output any) []any {
	if tool != "browser.list_tabs" && tool != "browser.open" {
		return nil
	}
	result, ok := output.(map[string]any)
	if !ok {
		return nil
	}
	return extractPages(result)
}

func extractPages(result map[string]any) []any {
	if pages, ok := result["pages"].([]any); ok {
		return pages
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
		return ""
	}
	return stringValue(value)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func boolArg(args map[string]any, key string) bool {
	value, ok := args[key]
	if !ok {
		return false
	}
	if b, ok := value.(bool); ok {
		return b
	}
	return strings.EqualFold(fmt.Sprint(value), "true")
}

var ErrDisabled = errors.New("browser automation is disabled")
