package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const playwrightExtensionSessionTTL = 10 * time.Minute

type PlaywrightController interface {
	Status(context.Context) browsercontrol.Status
	AcquireSession(context.Context, string, time.Duration, time.Duration) (browsercontrol.Session, error)
}

type PlaywrightExtensionAdapter struct {
	cfg        config.Config
	controller PlaywrightController

	mu                 sync.Mutex
	session            browsercontrol.Session
	sessionCancel      context.CancelFunc
	scope              string
	providerSessionRef string
	nextTaskID         uint64
	nextSnapshotID     uint64
	snapshots          map[string]*browserSnapshotState
	activeSnapshotPage string
}

func NewPlaywrightExtensionAdapter(cfg config.Config, controller PlaywrightController) Adapter {
	return &PlaywrightExtensionAdapter{
		cfg: cfg, controller: controller, snapshots: map[string]*browserSnapshotState{},
	}
}

func (a *PlaywrightExtensionAdapter) Health(ctx context.Context, args map[string]any) (Result, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	if a.controller == nil {
		return Result{}, errors.New("playwright extension controller is unavailable")
	}
	status := a.controller.Status(ctx)
	output := map[string]any{
		"ok":                    status.Configured && status.State == app.IntegrationStateReady,
		"status":                status.State,
		"configured":            status.Configured,
		"profile_id":            status.ProfileID,
		"credential_generation": status.CredentialGeneration,
		"versions":              status.Versions,
	}
	if status.ErrorCode != "" {
		output["error_code"] = status.ErrorCode
	}
	return Result{
		Tool: "browser.status", RawTool: "playwright_extension_status", Arguments: browserResultArguments(args),
		Output: output, Pages: []any{}, BrowserMode: metadata.BrowserMode, Presentation: metadata.Presentation,
		SurfaceVisible: false, Untrusted: false, Provider: "playwright-extension",
		DurationMS: time.Since(started).Milliseconds(),
	}, nil
}

func (a *PlaywrightExtensionAdapter) Call(ctx context.Context, tool string, args map[string]any) (Result, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.acquireSessionLocked(ctx, args); err != nil {
		return Result{}, err
	}
	rawTool, output, err := a.executeLocked(ctx, tool, args)
	if err != nil {
		return Result{}, err
	}
	output = a.withSessionMetadataLocked(output, metadata, args)
	lease := a.session.Lease()
	result := Result{
		Tool: tool, RawTool: rawTool, Arguments: browserResultArguments(args), Output: output,
		Text: contentText(output), Pages: pagesFromOutput(tool, output), BrowserMode: metadata.BrowserMode,
		Presentation: metadata.Presentation, SurfaceVisible: metadata.SurfaceVisible,
		SessionGeneration: uint64(lease.SessionGeneration), ProviderSessionRef: a.providerSessionRef,
		Untrusted: true, Provider: "playwright-extension", DurationMS: time.Since(started).Milliseconds(),
	}
	return result, nil
}

func (a *PlaywrightExtensionAdapter) ReadPage(ctx context.Context, targetURL string, args map[string]any) (PageReadResult, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.acquireSessionLocked(ctx, args); err != nil {
		return PageReadResult{}, err
	}
	actions := []string{}
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	if boolArg(args, "reuse_active_page") {
		info, err := a.executeControllerLocked(ctx, "page.info", optionalPageArgs(pageID))
		if err != nil {
			return PageReadResult{}, err
		}
		page := mapValue(info["page"])
		if !sameBrowserReadURL(firstStringValue(page, "url"), targetURL) {
			return PageReadResult{}, fmt.Errorf("active managed page URL does not match bound read URL %q", targetURL)
		}
		pageID = firstStringValue(page, "page_id")
		actions = append(actions, "playwright_mcp.page.info")
	} else {
		opened, err := a.openURLLocked(ctx, targetURL)
		if err != nil {
			return PageReadResult{}, err
		}
		pageID = selectedPageID(opened)
		actions = append(actions, "playwright_mcp.page.navigate")
	}
	if err := a.waitForReadyLocked(ctx, pageID, intArg(args, "timeout_ms", a.cfg.Adapters.BrowserAutomation.TimeoutMS)); err != nil {
		return PageReadResult{}, err
	}
	actions = append(actions, "playwright_mcp.page.info")
	maximum := intArg(args, "max_chars", 120000)
	read, err := a.executeControllerLocked(ctx, "page.read", map[string]any{"page_id": pageID, "max_chars": maximum})
	if err != nil {
		return PageReadResult{}, err
	}
	page := mapValue(read["page"])
	if page == nil {
		return PageReadResult{}, errors.New("playwright extension returned no readable page state")
	}
	actions = append(actions, "playwright_mcp.page.read")
	auth := map[string]any{"text": firstStringValue(page, "text")}
	if snapshot, snapshotErr := a.executeControllerLocked(ctx, "page.snapshot", map[string]any{"page_id": pageID}); snapshotErr == nil {
		refs := buildBrowserSnapshotRefs(playwrightSnapshotRefs(snapshot["snapshot"]), "")
		auth = inferBrowserSnapshotAuth(auth, firstStringValue(page, "title"), firstStringValue(page, "url"), refs)
		actions = append(actions, "playwright_mcp.page.snapshot")
	}
	// A stale-session error on the ignored snapshot call releases the session;
	// the read is then unusable rather than a nil dereference.
	if a.session == nil {
		return PageReadResult{}, errors.New("playwright extension session was lost while reading the page")
	}
	lease := a.session.Lease()
	result := PageReadResult{
		URL: targetURL, FinalURL: firstNonEmptyBrowserString(firstStringValue(page, "url"), targetURL),
		Title: firstStringValue(page, "title"), HTML: firstStringValue(page, "html"), Text: firstStringValue(page, "text"),
		ContentType: "text/html; source=playwright-extension", ReadyState: firstStringValue(page, "ready_state"),
		Lang: firstStringValue(page, "lang"), Rendered: true, HTMLLength: len([]rune(firstStringValue(page, "html"))),
		TextLength: intValue(page["text_length"]), ScrollHeight: intValue(page["scroll_height"]),
		TextTruncated: boolValue(page["text_truncated"]), Actions: actions, ReadSource: "playwright-extension-rendered-dom",
		ReadMode: "browser_session", BrowserMode: metadata.BrowserMode, Presentation: metadata.Presentation,
		SurfaceVisible: metadata.SurfaceVisible, SessionGeneration: uint64(lease.SessionGeneration),
		ProviderSessionRef: a.providerSessionRef, Provider: "playwright-extension", Untrusted: true,
		DurationMS: time.Since(started).Milliseconds(),
	}
	applyPageReadAuth(&result, auth)
	if strings.TrimSpace(result.Text) == "" {
		return PageReadResult{}, errors.New("playwright extension returned no readable page content")
	}
	return result, nil
}

func (a *PlaywrightExtensionAdapter) executeLocked(ctx context.Context, tool string, args map[string]any) (string, map[string]any, error) {
	switch tool {
	case "browser.list_tabs":
		output, err := a.executeControllerLocked(ctx, "tabs.list", map[string]any{})
		return "playwright_mcp.tabs.list", normalizedPagesOutput(extractPages(output)), err
	case "browser.open":
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, errors.New("browser.open requires url")
		}
		output, err := a.openURLLocked(ctx, url)
		return "playwright_mcp.tabs.new", normalizedPagesOutput(extractPages(output)), err
	case "browser.focus":
		pageID, err := playwrightPageIDArg(args)
		if err != nil {
			return "", nil, err
		}
		output, err := a.executeControllerLocked(ctx, "tabs.handoff", map[string]any{"page_id": pageID})
		if err == nil {
			a.invalidateSnapshotsLocked()
		}
		page := cloneArgs(mapValue(output["page"]))
		page["selected"] = true
		return "playwright_mcp.tabs.handoff", page, err
	case "browser.close":
		pageID, err := playwrightPageIDArg(args)
		if err != nil {
			return "", nil, err
		}
		listed, listErr := a.executeControllerLocked(ctx, "tabs.list", map[string]any{})
		if listErr != nil {
			return "playwright_mcp.tabs.list", nil, listErr
		}
		if len(extractPages(listed)) == 1 {
			if _, err := a.executeControllerLocked(ctx, "tabs.new", map[string]any{}); err != nil {
				return "playwright_mcp.tabs.new", nil, err
			}
		}
		output, err := a.executeControllerLocked(ctx, "tabs.close", map[string]any{"page_id": pageID})
		if err == nil {
			a.invalidateSnapshotsLocked()
		}
		return "playwright_mcp.tabs.close", normalizedPagesOutput(extractPages(output)), err
	case "browser.navigate":
		pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, errors.New("browser.navigate requires url")
		}
		input := map[string]any{"url": url}
		if pageID != "" {
			input["page_id"] = pageID
		}
		output, err := a.executeControllerLocked(ctx, "page.navigate", input)
		if err == nil {
			a.invalidateSnapshotsLocked()
		}
		return "playwright_mcp.page.navigate", output, err
	case "browser.snapshot":
		if url := strings.TrimSpace(stringArg(args, "url")); url != "" {
			if _, err := a.openURLLocked(ctx, url); err != nil {
				return "playwright_mcp.page.navigate", nil, err
			}
		}
		output, err := a.takeSnapshotLocked(ctx, args)
		return "playwright_mcp.page.snapshot", output, err
	case "browser.screenshot":
		output, err := a.takeScreenshotLocked(ctx, args)
		return "playwright_mcp.page.screenshot", output, err
	case "browser.wait":
		if strings.EqualFold(strings.TrimSpace(stringArg(args, "mode")), "stable_state") {
			output, err := a.waitForStableStateLocked(ctx, args)
			return "playwright_mcp.page.stable_state", output, err
		}
		input := optionalPageArgs(normalizePlaywrightPageID(stringArg(args, "page_id")))
		if text := strings.TrimSpace(stringArg(args, "text")); text != "" {
			input["text"] = text
		} else if duration := intArg(args, "ms", intArg(args, "duration_ms", 0)); duration > 0 {
			input["duration_ms"] = duration
		} else {
			pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
			err := a.waitForReadyLocked(ctx, pageID, intArg(args, "timeout_ms", a.cfg.Adapters.BrowserAutomation.TimeoutMS))
			return "playwright_mcp.page.info", map[string]any{"status": "ready"}, err
		}
		output, err := a.executeControllerLocked(ctx, "page.wait", input)
		return "playwright_mcp.page.wait", output, err
	case "browser.click":
		output, err := a.clickLocked(ctx, args)
		return "playwright_mcp.page.click", output, err
	case "browser.type":
		output, raw, err := a.typeLocked(ctx, args)
		return raw, output, err
	case "browser.select":
		output, err := a.selectLocked(ctx, args)
		return "playwright_mcp.page.select", output, err
	default:
		return "", nil, fmt.Errorf("unsupported browser automation tool %q", tool)
	}
}

func (a *PlaywrightExtensionAdapter) openURLLocked(ctx context.Context, targetURL string) (map[string]any, error) {
	listed, err := a.executeControllerLocked(ctx, "tabs.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	pages := extractPages(listed)
	if len(pages) == 1 {
		page := mapValue(pages[0])
		if isAboutBlank(firstStringValue(page, "url")) {
			_, err := a.executeControllerLocked(ctx, "page.navigate", map[string]any{
				"page_id": firstStringValue(page, "page_id"), "url": targetURL,
			})
			if err != nil {
				return nil, err
			}
			a.invalidateSnapshotsLocked()
			refreshed, err := a.executeControllerLocked(ctx, "tabs.list", map[string]any{})
			return refreshed, err
		}
	}
	output, err := a.executeControllerLocked(ctx, "tabs.new", map[string]any{"url": targetURL})
	if err == nil {
		a.invalidateSnapshotsLocked()
	}
	return output, err
}

func playwrightPageIDArg(args map[string]any) (string, error) {
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	if pageID == "" {
		return "", errors.New("browser page_id is required")
	}
	return pageID, nil
}

func normalizePlaywrightPageID(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return ""
	}
	if _, err := strconv.Atoi(value); err == nil {
		return "page_" + value
	}
	if strings.HasPrefix(value, "page_") {
		if _, err := strconv.Atoi(strings.TrimPrefix(value, "page_")); err == nil {
			return value
		}
	}
	return value
}

func optionalPageArgs(pageID string) map[string]any {
	if pageID == "" {
		return map[string]any{}
	}
	return map[string]any{"page_id": pageID}
}

func mergePageArgs(pageID string, extra map[string]any) map[string]any {
	result := cloneArgs(extra)
	if pageID != "" {
		result["page_id"] = pageID
	}
	return result
}

func selectedPageID(output map[string]any) string {
	for _, raw := range extractPages(output) {
		page := mapValue(raw)
		if boolValue(page["selected"]) {
			return firstStringValue(page, "page_id")
		}
	}
	return ""
}
