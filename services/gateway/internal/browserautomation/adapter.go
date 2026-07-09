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
	ReadPage(ctx context.Context, url string, args map[string]any) (PageReadResult, error)
	Health(ctx context.Context) (Result, error)
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
	Pages              []any          `json:"pages,omitempty"`
	ScreenshotPath     string         `json:"screenshot_path,omitempty"`
	ScreenshotMarkdown string         `json:"screenshot_markdown,omitempty"`
	BrowserMode        string         `json:"browser_mode,omitempty"`
	Presentation       string         `json:"presentation,omitempty"`
	SurfaceVisible     bool           `json:"surface_visible,omitempty"`
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
	AuthChallengeDetected bool           `json:"auth_challenge_detected,omitempty"`
	SnapshotText          string         `json:"snapshot_text,omitempty"`
	Snapshot              any            `json:"snapshot,omitempty"`
	EvaluateOutput        any            `json:"evaluate_output,omitempty"`
	OpenOutput            any            `json:"open_output,omitempty"`
	Actions               []string       `json:"actions,omitempty"`
	ReadMode              string         `json:"read_mode,omitempty"`
	BrowserMode           string         `json:"browser_mode,omitempty"`
	Presentation          string         `json:"presentation,omitempty"`
	SurfaceVisible        bool           `json:"surface_visible,omitempty"`
	Provider              string         `json:"provider"`
	DurationMS            int64          `json:"duration_ms"`
	Untrusted             bool           `json:"untrusted"`
	Errors                map[string]any `json:"errors,omitempty"`
}

type ChromeDevToolsAdapter struct {
	cfg           config.Config
	mu            sync.Mutex
	session       *stdioSession
	hiddenSession *stdioSession
}

func NewAdapter(cfg config.Config) Adapter {
	return &ChromeDevToolsAdapter{cfg: cfg}
}

// Close shuts down the MCP subprocess (and the Chrome instance it manages) so
// gateway shutdown does not orphan them.
func (a *ChromeDevToolsAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resetAllSessionsLocked()
	return nil
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
	metadata := browserModeMetadata(args, defaultBrowserModeForTool(tool))
	if tool == "browser.snapshot" && shouldUseHiddenBrowserSession(metadata, args) {
		if strings.TrimSpace(stringArg(args, "url")) == "" {
			return Result{}, errors.New("autonomous hidden browser.snapshot requires url or browser_page_ref")
		}
		return a.hiddenSnapshot(ctx, started, args, metadata)
	}
	rawTool, rawArgs, err := mapTool(tool, args)
	if err != nil {
		return Result{}, err
	}
	hidden := shouldUseHiddenAutomationTool(tool, metadata, args)
	out, err := a.callToolWithSession(ctx, hidden, rawTool, rawArgs)
	if err != nil {
		return Result{}, err
	}
	normalized := normalizeOutput(tool, out)
	if err := mcpToolError(rawTool, normalized); err != nil {
		return Result{}, err
	}
	if hidden {
		normalized = a.withHiddenPageState(ctx, normalized)
	}
	return Result{
		Tool:           tool,
		RawTool:        rawTool,
		Arguments:      rawArgs,
		Output:         normalized,
		Text:           contentText(normalized),
		Pages:          pagesFromOutput(tool, normalized),
		BrowserMode:    metadata.BrowserMode,
		Presentation:   metadata.Presentation,
		SurfaceVisible: metadata.SurfaceVisible,
		Untrusted:      true,
		Provider:       browserProviderName(hidden),
		DurationMS:     time.Since(started).Milliseconds(),
	}, nil
}

func (a *ChromeDevToolsAdapter) withHiddenPageState(ctx context.Context, output any) any {
	pageState, err := a.callToolWithSession(ctx, true, "evaluate_script", map[string]any{
		"function": browserPageStateEvaluateFunction,
	})
	normalized := map[string]any{}
	if current, ok := output.(map[string]any); ok {
		normalized = cloneArgs(current)
	} else {
		normalized["raw_output"] = output
	}
	if err != nil {
		normalized["hidden_page_state_error"] = err.Error()
		return normalized
	}
	if toolErr := mcpToolError("evaluate_script", pageState); toolErr != nil {
		normalized["hidden_page_state_error"] = toolErr.Error()
		return normalized
	}
	normalized["hidden_page_state"] = pageState
	if payload := evaluatePayloadMap(pageState); payload != nil {
		normalized["current_url"] = firstStringValue(payload, "url", "href")
		normalized["current_title"] = firstStringValue(payload, "title")
		normalized["current_ready_state"] = firstStringValue(payload, "readyState", "ready_state")
	}
	return normalized
}

func (a *ChromeDevToolsAdapter) ReadPage(ctx context.Context, targetURL string, args map[string]any) (PageReadResult, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	hidden := shouldUseHiddenBrowserSession(metadata, args)
	provider := browserProviderName(hidden)
	readMode := "browser_session"
	openAction := "new_page"
	if hidden {
		readMode = "hidden_browser_session"
		openAction = "new_hidden_page"
	}
	timeoutMS := intArg(args, "timeout_ms", a.cfg.Adapters.BrowserAutomation.TimeoutMS)
	if timeoutMS <= 0 {
		timeoutMS = 15000
	}
	readCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		readCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	}
	defer cancel()

	result := PageReadResult{
		URL:            targetURL,
		Provider:       provider,
		Untrusted:      true,
		Actions:        []string{},
		ReadMode:       readMode,
		Errors:         map[string]any{},
		BrowserMode:    metadata.BrowserMode,
		Presentation:   metadata.Presentation,
		SurfaceVisible: metadata.SurfaceVisible,
	}
	tools, listErr := a.listToolsWithSession(readCtx, hidden)
	if listErr != nil {
		result.Errors["tools/list"] = listErr.Error()
	}
	openOutput, err := a.callToolWithSession(readCtx, hidden, "new_page", map[string]any{
		"url":     targetURL,
		"timeout": timeoutMS,
	})
	if err != nil {
		return PageReadResult{}, err
	}
	if err := mcpToolError("new_page", openOutput); err != nil {
		return PageReadResult{}, err
	}
	result.OpenOutput = openOutput
	result.Actions = append(result.Actions, openAction)

	maxChars := intArg(args, "max_chars", 120000)
	if maxChars <= 0 {
		maxChars = 120000
	}
	if len(tools) > 0 && !containsToolName(tools, "evaluate_script") {
		result.Errors["evaluate_script"] = "tool unavailable"
	} else {
		evaluateOutput, evalErr := a.callToolWithSession(readCtx, hidden, "evaluate_script", map[string]any{
			"function": browserReadEvaluateFunction(maxChars),
		})
		if evalErr != nil {
			result.Errors["evaluate_script"] = evalErr.Error()
		} else if err := mcpToolError("evaluate_script", evaluateOutput); err != nil {
			result.Errors["evaluate_script"] = err.Error()
		} else {
			result.Actions = append(result.Actions, "evaluate_script")
			result.EvaluateOutput = evaluateOutput
			if payload := evaluatePayloadMap(evaluateOutput); payload != nil {
				result.FinalURL = firstStringValue(payload, "url", "href")
				result.Title = firstStringValue(payload, "title")
				result.HTML = firstStringValue(payload, "html")
				result.Text = firstStringValue(payload, "text", "innerText")
				result.ContentType = firstStringValue(payload, "contentType", "content_type")
				result.ReadyState = firstStringValue(payload, "readyState", "ready_state")
				result.Lang = firstStringValue(payload, "lang")
				result.Rendered = boolValue(payload["rendered"])
				result.HTMLLength = intValue(payload["htmlLength"])
				result.HTMLTruncated = boolValue(payload["htmlTruncated"])
				result.TextLength = intValue(payload["textLength"])
				result.ScrollHeight = intValue(payload["scrollHeight"])
				result.AuthChallengeDetected = boolValue(payload["authChallengeDetected"])
			}
		}
	}

	if readPageSnapshotRequested(args) {
		snapshotOutput, snapshotErr := a.callToolWithSession(readCtx, hidden, "take_snapshot", map[string]any{})
		if snapshotErr != nil {
			result.Errors["take_snapshot"] = snapshotErr.Error()
		} else if err := mcpToolError("take_snapshot", snapshotOutput); err != nil {
			result.Errors["take_snapshot"] = err.Error()
		} else {
			result.Actions = append(result.Actions, "take_snapshot")
			result.Snapshot = snapshotOutput
			result.SnapshotText = contentText(snapshotOutput)
		}
	}

	result.DurationMS = time.Since(started).Milliseconds()
	if result.FinalURL == "" {
		result.FinalURL = targetURL
	}
	if strings.TrimSpace(result.ContentType) == "" {
		result.ContentType = "text/html; source=browser"
	}
	if result.HTML == "" && result.SnapshotText == "" && len(result.Errors) > 0 {
		return PageReadResult{}, fmt.Errorf("browser page read failed: %v", result.Errors)
	}
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result, nil
}

func (a *ChromeDevToolsAdapter) hiddenSnapshot(ctx context.Context, started time.Time, args map[string]any, metadata browserModeFields) (Result, error) {
	targetURL := strings.TrimSpace(stringArg(args, "url"))
	if targetURL == "" {
		return Result{}, errors.New("autonomous hidden browser.snapshot requires url")
	}
	readArgs := cloneArgs(args)
	readArgs["include_snapshot"] = true
	readArgs["browser_mode"] = metadata.BrowserMode
	readArgs["presentation"] = metadata.Presentation
	readArgs["surface_visible"] = metadata.SurfaceVisible
	page, err := a.ReadPage(ctx, targetURL, readArgs)
	if err != nil {
		return Result{}, err
	}
	if isAboutBlank(page.FinalURL) && !isAboutBlank(targetURL) {
		return Result{}, fmt.Errorf("hidden browser snapshot failed: target %q remained at %q", targetURL, page.FinalURL)
	}
	snapshotText := strings.TrimSpace(page.SnapshotText)
	if snapshotText == "" {
		if len(page.Errors) > 0 {
			return Result{}, fmt.Errorf("hidden browser snapshot failed: %v", page.Errors)
		}
		snapshotText = strings.TrimSpace(page.Text)
	}
	if snapshotText == "" {
		return Result{}, errors.New("hidden browser snapshot returned no page structure")
	}
	output := map[string]any{
		"tool":                    "browser.snapshot",
		"raw_tool":                "hidden_read_take_snapshot",
		"url":                     page.URL,
		"final_url":               page.FinalURL,
		"title":                   page.Title,
		"text":                    snapshotText,
		"snapshot":                page.Snapshot,
		"browser_mode":            page.BrowserMode,
		"presentation":            page.Presentation,
		"surface_visible":         page.SurfaceVisible,
		"read_mode":               page.ReadMode,
		"browser_provider":        page.Provider,
		"browser_actions":         page.Actions,
		"browser_ready_state":     page.ReadyState,
		"browser_lang":            page.Lang,
		"browser_html_length":     page.HTMLLength,
		"browser_text_length":     page.TextLength,
		"browser_scroll_height":   page.ScrollHeight,
		"auth_challenge_detected": page.AuthChallengeDetected,
		"untrusted":               true,
	}
	if len(page.Errors) > 0 {
		output["browser_session_warnings"] = page.Errors
	}
	return Result{
		Tool:           "browser.snapshot",
		RawTool:        "hidden_read_take_snapshot",
		Arguments:      cloneArgs(args),
		Output:         output,
		Text:           snapshotText,
		BrowserMode:    page.BrowserMode,
		Presentation:   page.Presentation,
		SurfaceVisible: page.SurfaceVisible,
		Untrusted:      true,
		Provider:       page.Provider,
		DurationMS:     time.Since(started).Milliseconds(),
	}, nil
}

func isAboutBlank(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "about:blank")
}

func readPageSnapshotRequested(args map[string]any) bool {
	return boolArg(args, "include_snapshot") || boolArg(args, "take_snapshot") || boolArg(args, "snapshot")
}

func shouldUseHiddenBrowserSession(metadata browserModeFields, args map[string]any) bool {
	if metadata.BrowserMode != "autonomous" || metadata.Presentation != "hidden" || metadata.SurfaceVisible {
		return false
	}
	if boolArg(args, "visible_browser") || boolArg(args, "disable_hidden_browser") {
		return false
	}
	return true
}

func shouldUseHiddenAutomationTool(tool string, metadata browserModeFields, args map[string]any) bool {
	if !shouldUseHiddenBrowserSession(metadata, args) {
		return false
	}
	switch tool {
	case "browser.open", "browser.navigate", "browser.click", "browser.wait":
		return true
	default:
		return false
	}
}

func browserProviderName(hidden bool) string {
	if hidden {
		return "chrome-devtools-mcp-headless"
	}
	return "chrome-devtools-mcp"
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
		if mode == "collaborative" {
			presentation = "visible"
		} else {
			presentation = "hidden"
		}
	}
	surfaceVisible := mode == "collaborative"
	if _, ok := args["surface_visible"]; ok {
		surfaceVisible = boolArg(args, "surface_visible")
	}
	return browserModeFields{
		BrowserMode:    mode,
		Presentation:   presentation,
		SurfaceVisible: surfaceVisible,
	}
}

func defaultBrowserModeForTool(tool string) string {
	if tool == "browser.read" {
		return "autonomous"
	}
	return "collaborative"
}

func containsToolName(tools []string, name string) bool {
	for _, tool := range tools {
		if tool == name {
			return true
		}
	}
	return false
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
	delete(out, "browser_mode")
	delete(out, "presentation")
	delete(out, "surface_visible")
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

func browserReadEvaluateFunction(maxChars int) string {
	if maxChars <= 0 {
		maxChars = 120000
	}
	return fmt.Sprintf(browserReadEvaluateFunctionTemplate, maxChars)
}

const browserReadEvaluateFunctionTemplate = `async () => {
  const limit = %d;
  const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
  let lastHeight = -1;
  for (let i = 0; i < 4; i++) {
    const height = Math.max(
      document.body ? document.body.scrollHeight : 0,
      document.documentElement ? document.documentElement.scrollHeight : 0
    );
    if (height === lastHeight) {
      break;
    }
    lastHeight = height;
    window.scrollTo(0, height);
    await wait(350);
  }
  window.scrollTo(0, 0);
  await wait(150);
  const html = document.documentElement ? document.documentElement.outerHTML : "";
  const text = document.body ? document.body.innerText : "";
  const title = document.title || "";
  const lang = document.documentElement ? (document.documentElement.lang || "") : "";
  const loginText = (title + "\n" + text.slice(0, 5000)).toLowerCase();
  const authChallengeDetected = Boolean(
    document.querySelector('input[type="password"], input[name*="password" i], input[id*="password" i]') ||
    /登录|登入|验证码|扫码|sign in|log in|password|captcha|verification code/.test(loginText)
  );
  return {
    url: location.href,
    title,
    lang,
    readyState: document.readyState,
    contentType: document.contentType || "text/html",
    rendered: true,
    html: html.slice(0, limit),
    htmlLength: html.length,
    htmlTruncated: html.length > limit,
    text: text.slice(0, limit),
    textLength: text.length,
    scrollHeight: Math.max(
      document.body ? document.body.scrollHeight : 0,
      document.documentElement ? document.documentElement.scrollHeight : 0
    ),
    authChallengeDetected
  };
}`

const browserPageStateEvaluateFunction = `() => ({
  url: location.href,
  title: document.title || "",
  readyState: document.readyState
})`

func evaluatePayloadMap(output any) map[string]any {
	if payload := mapValue(output); payload != nil {
		for _, key := range []string{"result", "value", "json", "data"} {
			if nested := mapValue(payload[key]); nested != nil {
				return nested
			}
			if parsed := parseJSONMap(stringValue(payload[key])); parsed != nil {
				return parsed
			}
		}
		if looksLikePageReadPayload(payload) {
			return payload
		}
	}
	if parsed := parseJSONMap(contentText(output)); parsed != nil {
		if nested := mapValue(parsed["result"]); nested != nil {
			return nested
		}
		if nested := mapValue(parsed["value"]); nested != nil {
			return nested
		}
		return parsed
	}
	return nil
}

func looksLikePageReadPayload(value map[string]any) bool {
	for _, key := range []string{"html", "text", "url", "title", "readyState"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	return false
}

func parseJSONMap(value string) map[string]any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if parsed := parseJSONMapDirect(value); parsed != nil {
		return parsed
	}
	for _, candidate := range fencedJSONCandidates(value) {
		if parsed := parseJSONMapDirect(candidate); parsed != nil {
			return parsed
		}
	}
	if candidate := firstJSONObjectCandidate(value); candidate != "" {
		return parseJSONMapDirect(candidate)
	}
	return nil
}

func parseJSONMapDirect(value string) map[string]any {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return nil
	}
	return mapValue(decoded)
}

func fencedJSONCandidates(value string) []string {
	parts := strings.Split(value, "```")
	if len(parts) < 3 {
		return nil
	}
	candidates := []string{}
	for i := 1; i < len(parts); i += 2 {
		block := strings.TrimSpace(parts[i])
		block = strings.TrimPrefix(block, "json")
		block = strings.TrimPrefix(block, "JSON")
		block = strings.TrimSpace(block)
		if block != "" {
			candidates = append(candidates, block)
		}
	}
	return candidates
}

func firstJSONObjectCandidate(value string) string {
	start := strings.IndexByte(value, '{')
	end := strings.LastIndexByte(value, '}')
	if start < 0 || end <= start {
		return ""
	}
	return value[start : end+1]
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
