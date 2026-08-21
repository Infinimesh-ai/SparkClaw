package toolhub

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

func (h *ToolHub) browserAutomationHealth(ctx context.Context, args map[string]any, sessionID string) (Result, error) {
	callArgs, metadata, err := h.browserAutomationCallArgs(ctx, "browser.status", args, sessionID)
	if err != nil {
		return Result{}, err
	}
	result, err := h.browser.Health(ctx, callArgs)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(result.BrowserMode) == "" {
		result.BrowserMode = metadata.BrowserMode
	}
	if strings.TrimSpace(result.Presentation) == "" {
		result.Presentation = metadata.Presentation
	}
	result.Pages = nonNilBrowserPages(result.Pages)
	result.SurfaceVisible = result.SurfaceVisible || metadata.SurfaceVisible
	return Result{Output: result}, nil
}

func (h *ToolHub) browserAutomationTool(ctx context.Context, name string, args map[string]any, sessionID string) (Result, error) {
	callArgs, metadata, err := h.browserAutomationCallArgs(ctx, name, args, sessionID)
	if err != nil {
		return Result{}, err
	}
	result, err := h.browser.Call(ctx, name, callArgs)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(result.BrowserMode) == "" {
		result.BrowserMode = metadata.BrowserMode
	}
	if strings.TrimSpace(result.Presentation) == "" {
		result.Presentation = metadata.Presentation
	}
	result.Pages = nonNilBrowserPages(result.Pages)
	result.SurfaceVisible = result.SurfaceVisible || metadata.SurfaceVisible
	if name == "browser.screenshot" {
		h.attachBrowserScreenshot(ctx, &result)
	}
	return Result{Output: result}, nil
}

func (h *ToolHub) browserAutomationCallArgs(ctx context.Context, name string, args map[string]any, sessionID string) (map[string]any, browserModeMetadata, error) {
	callArgs, metadata := browserAutomationArgsWithMode(name, args)
	if strings.TrimSpace(stringArg(callArgs, "owner_id", "")) == "" {
		ownerID := ""
		if h.store != nil && strings.TrimSpace(sessionID) != "" {
			session, ok, err := h.store.GetSession(ctx, sessionID)
			if err != nil {
				return nil, browserModeMetadata{}, fmt.Errorf("resolve browser automation session owner: %w", err)
			}
			if ok {
				ownerID = strings.TrimSpace(session.OwnerID)
			}
		}
		callArgs["owner_id"] = firstNonEmptyString(ownerID, app.DefaultOwnerID)
	}
	if strings.TrimSpace(stringArg(callArgs, "browser_profile_id", "")) == "" {
		callArgs["browser_profile_id"] = firstNonEmptyString(strings.TrimSpace(h.cfg.Tools.BrowserAutomation.Profile), "default")
	}
	return callArgs, metadata, nil
}

func nonNilBrowserPages(pages []any) []any {
	if pages == nil {
		return []any{}
	}
	return pages
}

func browserAutomationArgsWithMode(name string, args map[string]any) (map[string]any, browserModeMetadata) {
	metadata := browserModeMetadataFromArgs(args, "autonomous")
	out := map[string]any{}
	for key, value := range args {
		out[key] = value
	}
	if strings.TrimSpace(stringArg(out, "browser_mode", "")) == "" {
		out["browser_mode"] = metadata.BrowserMode
	}
	if strings.TrimSpace(stringArg(out, "presentation", "")) == "" {
		out["presentation"] = metadata.Presentation
	}
	if _, ok := out["surface_visible"]; !ok {
		out["surface_visible"] = metadata.SurfaceVisible
	}
	return out, metadata
}

func (h *ToolHub) attachBrowserScreenshot(ctx context.Context, result *browserautomation.Result) {
	if result == nil {
		return
	}
	raw, contentType, ok := browserScreenshotBytes(result.Output)
	if !ok || len(raw) == 0 {
		return
	}
	root := strings.TrimSpace(h.cfg.Workspaces.DefaultRoot)
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, ".sparkclaw", "screenshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		addBrowserScreenshotSaveError(result, err)
		return
	}
	ext := ".png"
	if strings.Contains(strings.ToLower(contentType), "jpeg") || strings.Contains(strings.ToLower(contentType), "jpg") {
		ext = ".jpg"
	}
	name := fmt.Sprintf("browser-screenshot-%s%s", time.Now().UTC().Format("20060102-150405"), ext)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		addBrowserScreenshotSaveError(result, err)
		return
	}
	output := ensureBrowserAutomationOutputMap(result)
	output["screenshot_path"] = path
	output["screenshot_content_type"] = contentType
	output["screenshot_bytes"] = len(raw)
	output["screenshot_markdown"] = fmt.Sprintf("![browser screenshot](%s)", path)
	result.ScreenshotPath = path
	result.ScreenshotMarkdown = fmt.Sprintf("![browser screenshot](%s)", path)
	resultText := result.Text
	if strings.TrimSpace(resultText) != "" {
		resultText += "\n"
	}
	result.Text = resultText + "screenshot_path=" + path
}

func addBrowserScreenshotSaveError(result *browserautomation.Result, err error) {
	output := ensureBrowserAutomationOutputMap(result)
	output["screenshot_save_error"] = err.Error()
}

func ensureBrowserAutomationOutputMap(result *browserautomation.Result) map[string]any {
	output, ok := result.Output.(map[string]any)
	if !ok || output == nil {
		output = map[string]any{"raw_output": result.Output}
		result.Output = output
	}
	return output
}

func browserScreenshotBytes(output any) ([]byte, string, bool) {
	result, ok := output.(map[string]any)
	if !ok {
		return nil, "", false
	}
	if raw, contentType, ok := screenshotBytesFromMap(result); ok {
		return raw, contentType, true
	}
	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if raw, contentType, ok := screenshotBytesFromMap(obj); ok {
				return raw, contentType, true
			}
		}
	}
	return nil, "", false
}

func screenshotBytesFromMap(obj map[string]any) ([]byte, string, bool) {
	data := firstString(obj, "data", "base64", "image", "bytes")
	if strings.TrimSpace(data) == "" {
		return nil, "", false
	}
	contentType := firstString(obj, "mimeType", "mime_type", "content_type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "image/png"
	}
	raw, err := base64.StdEncoding.DecodeString(stripDataURLPrefix(data))
	if err != nil {
		return nil, "", false
	}
	return raw, contentType, true
}

func firstString(obj map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := browserAutomationStringValue(obj[key]); strings.TrimSpace(value) != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}

func browserAutomationStringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func stripDataURLPrefix(value string) string {
	value = strings.TrimSpace(value)
	if comma := strings.Index(value, ","); strings.HasPrefix(value, "data:") && comma >= 0 {
		return value[comma+1:]
	}
	return value
}
