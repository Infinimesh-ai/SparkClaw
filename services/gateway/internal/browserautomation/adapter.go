package browserautomation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const mcpProtocolVersion = "2025-06-18"

type Adapter interface {
	Call(ctx context.Context, tool string, args map[string]any) (Result, error)
	Health(ctx context.Context) (Result, error)
}

type Result struct {
	Tool               string         `json:"tool"`
	RawTool            string         `json:"raw_tool,omitempty"`
	Arguments          map[string]any `json:"arguments,omitempty"`
	Output             any            `json:"output,omitempty"`
	Text               string         `json:"text,omitempty"`
	Pages              []any          `json:"pages,omitempty"`
	ScreenshotPath     string         `json:"screenshot_path,omitempty"`
	ScreenshotMarkdown string         `json:"screenshot_markdown,omitempty"`
	Untrusted          bool           `json:"untrusted"`
	Provider           string         `json:"provider"`
	DurationMS         int64          `json:"duration_ms"`
}

type ChromeDevToolsAdapter struct {
	cfg     config.Config
	mu      sync.Mutex
	session *stdioSession
}

func NewAdapter(cfg config.Config) Adapter {
	return &ChromeDevToolsAdapter{cfg: cfg}
}

func (a *ChromeDevToolsAdapter) Health(ctx context.Context) (Result, error) {
	started := time.Now()
	tools, err := a.listTools(ctx)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Tool:       "browser.status",
		Output:     map[string]any{"ok": true, "tool_count": len(tools), "tools": tools},
		Untrusted:  true,
		Provider:   "chrome-devtools-mcp",
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func (a *ChromeDevToolsAdapter) Call(ctx context.Context, tool string, args map[string]any) (Result, error) {
	started := time.Now()
	rawTool, rawArgs, err := mapTool(tool, args)
	if err != nil {
		return Result{}, err
	}
	out, err := a.callTool(ctx, rawTool, rawArgs)
	if err != nil {
		return Result{}, err
	}
	normalized := normalizeOutput(tool, out)
	if err := mcpToolError(rawTool, normalized); err != nil {
		return Result{}, err
	}
	return Result{
		Tool:       tool,
		RawTool:    rawTool,
		Arguments:  rawArgs,
		Output:     normalized,
		Text:       contentText(normalized),
		Pages:      pagesFromOutput(tool, normalized),
		Untrusted:  true,
		Provider:   "chrome-devtools-mcp",
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func mapTool(tool string, args map[string]any) (string, map[string]any, error) {
	args = cloneArgs(args)
	switch tool {
	case "browser.list_tabs":
		return "list_pages", rawArgsForTool("list_pages", args), nil
	case "browser.open":
		return "new_page", rawArgsForTool("new_page", args), nil
	case "browser.focus":
		return "select_page", rawArgsForTool("select_page", args), nil
	case "browser.close":
		return "close_page", rawArgsForTool("close_page", args), nil
	case "browser.navigate":
		return "navigate_page", rawArgsForTool("navigate_page", args), nil
	case "browser.snapshot":
		return "take_snapshot", rawArgsForTool("take_snapshot", args), nil
	case "browser.screenshot":
		return "take_screenshot", rawArgsForTool("take_screenshot", args), nil
	case "browser.wait":
		return "wait_for", rawArgsForTool("wait_for", args), nil
	case "browser.click":
		return "click", rawArgsForTool("click", args), nil
	case "browser.type":
		if shouldUseTypeText(args) {
			return "type_text", typeTextArgs(args), nil
		}
		return "fill", fillArgs(args), nil
	case "browser.select":
		return "fill", rawArgsForTool("fill", args), nil
	default:
		return "", nil, fmt.Errorf("unsupported browser automation tool %q", tool)
	}
}

func rawArgs(args map[string]any) map[string]any {
	out := cloneArgs(args)
	if _, ok := out["uid"]; !ok {
		if ref, ok := out["ref"]; ok {
			out["uid"] = ref
		}
	}
	delete(out, "reason")
	delete(out, "timeout_ms")
	delete(out, "target_kind")
	delete(out, "focused")
	delete(out, "current_focus")
	delete(out, "rich_text")
	delete(out, "mode")
	return out
}

func rawArgsForTool(rawTool string, args map[string]any) map[string]any {
	args = rawArgs(args)
	switch rawTool {
	case "take_snapshot":
		return onlyArgs(args, "verbose", "filePath")
	default:
		return args
	}
}

func onlyArgs(args map[string]any, allowed ...string) map[string]any {
	keep := map[string]bool{}
	for _, key := range allowed {
		keep[key] = true
	}
	out := map[string]any{}
	for key, value := range args {
		if keep[key] {
			out[key] = value
		}
	}
	return out
}

func fillArgs(args map[string]any) map[string]any {
	out := rawArgsForTool("fill", args)
	if _, ok := out["value"]; !ok {
		if text, ok := out["text"]; ok {
			out["value"] = text
		}
	}
	delete(out, "text")
	delete(out, "ref")
	return out
}

func typeTextArgs(args map[string]any) map[string]any {
	out := rawArgsForTool("type_text", args)
	delete(out, "uid")
	delete(out, "ref")
	delete(out, "value")
	return out
}

func normalizeOutput(tool string, output any) any {
	if tool != "browser.list_tabs" {
		return output
	}
	result, ok := output.(map[string]any)
	if !ok {
		return map[string]any{"raw_output": output, "pages": []any{}}
	}
	normalized := cloneArgs(result)
	if pages := extractPages(result); pages != nil {
		normalized["pages"] = pages
		return normalized
	}
	normalized["pages"] = []any{}
	return normalized
}

func mcpToolError(rawTool string, output any) error {
	result, ok := output.(map[string]any)
	if !ok || !boolArg(result, "isError") {
		return nil
	}
	text := contentText(result)
	if strings.TrimSpace(text) == "" {
		text = "tool returned isError=true"
	}
	return fmt.Errorf("%s failed: %s", rawTool, text)
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

func pagesFromOutput(tool string, output any) []any {
	if tool != "browser.list_tabs" {
		return nil
	}
	result, ok := output.(map[string]any)
	if !ok {
		return nil
	}
	return extractPages(result)
}

func extractPages(result map[string]any) []any {
	for _, key := range []string{"pages", "result"} {
		if pages, ok := result[key].([]any); ok {
			return pages
		}
	}
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			text, ok := obj["text"].(string)
			if !ok || strings.TrimSpace(text) == "" {
				continue
			}
			if pages := pagesFromText(text); pages != nil {
				return pages
			}
		}
	}
	return nil
}

func pagesFromText(text string) []any {
	text = strings.TrimSpace(text)
	var direct []any
	if err := json.Unmarshal([]byte(text), &direct); err == nil {
		return direct
	}
	var wrapped map[string]any
	if err := json.Unmarshal([]byte(text), &wrapped); err == nil {
		return extractPages(wrapped)
	}
	lines := strings.Split(text, "\n")
	pages := []any{}
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		if line == "" {
			continue
		}
		if strings.Contains(line, "http://") || strings.Contains(line, "https://") || strings.Contains(line, "about:") {
			pages = append(pages, map[string]any{"text": line})
		}
	}
	if len(pages) > 0 {
		return pages
	}
	return nil
}

func stringArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok || value == nil {
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
