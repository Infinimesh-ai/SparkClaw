package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type AgentBrowserAdapter struct {
	cfg                config.Config
	mu                 sync.Mutex
	entry              *agentBrowserSessionEntry
	commandPath        string
	commandValidated   bool
	namespace          string
	nextGeneration     uint64
	observeStableState func(context.Context, *agentBrowserSession) (browserStableObservation, error)
}

// agentBrowserSessionEntry owns one live agent-browser session together with
// every piece of adapter state scoped to that session: the snapshot registry,
// tab observations, and the generation reported to callers.
type agentBrowserSessionEntry struct {
	adapter            *AgentBrowserAdapter
	profile            string
	presentation       string
	session            *agentBrowserSession
	generation         uint64
	snapshots          map[string]*agentBrowserSnapshotState
	activeSnapshotPage string
	nextSnapshotID     uint64
	noOpenTabs         bool
	observedTabs       []any
	observedTabsValid  bool
	freshSession       bool
}

func NewAdapter(cfg config.Config) Adapter {
	return &AgentBrowserAdapter{
		cfg:            cfg,
		namespace:      newAgentBrowserNamespace(),
		nextGeneration: uint64(time.Now().UTC().UnixMicro()),
	}
}

func (a *AgentBrowserAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resetSessionLocked()
	return nil
}

func (a *AgentBrowserAdapter) Health(ctx context.Context, args map[string]any) (Result, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	profileKey := a.browserProfileKey(args)
	a.mu.Lock()
	defer a.mu.Unlock()
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	if a.commandPath == "" {
		command, err := resolveAgentBrowserCommand(adapterCfg.Command)
		if err != nil {
			return Result{}, err
		}
		a.commandPath = command
	}
	profileOwned := a.entry != nil && a.entry.session.alive() && a.entry.profile == profileKey
	preflight := inspectBrowserEnvironment(
		ctx,
		adapterCfg,
		profileKey,
		a.commandPath,
		profileOwned,
		boolArg(args, "require_visible_environment"),
	)
	if preflight.providerVersionPinned {
		a.commandValidated = true
	}
	return Result{
		Tool:           "browser.status",
		RawTool:        "linux_environment_preflight",
		Arguments:      browserResultArguments(args),
		Output:         preflight.output(),
		Pages:          []any{},
		BrowserMode:    metadata.BrowserMode,
		Presentation:   metadata.Presentation,
		SurfaceVisible: false,
		Untrusted:      false,
		Provider:       "agent-browser",
		DurationMS:     time.Since(started).Milliseconds(),
	}, nil
}

func (a *AgentBrowserAdapter) Call(ctx context.Context, tool string, args map[string]any) (Result, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	hidden := shouldUseHiddenAutomationTool(tool, metadata, args)
	profileKey := a.browserProfileKey(args)

	a.mu.Lock()
	defer a.mu.Unlock()
	entry, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return Result{}, err
	}
	rawTool, _, output, err := entry.executeLocked(ctx, tool, args)
	if err != nil {
		return Result{}, err
	}
	if shouldAttachHiddenPageState(tool, hidden) {
		output = entry.withHiddenPageStateLocked(ctx, output)
	}
	output = entry.withSessionMetadataLocked(output, metadata)
	result := Result{
		Tool:               tool,
		RawTool:            rawTool,
		Arguments:          browserResultArguments(args),
		Output:             output,
		Text:               contentText(output),
		Pages:              pagesFromOutput(tool, output),
		BrowserMode:        metadata.BrowserMode,
		Presentation:       metadata.Presentation,
		SurfaceVisible:     metadata.SurfaceVisible,
		SessionGeneration:  entry.generation,
		ProviderSessionRef: entry.session.sessionName,
		Untrusted:          true,
		Provider:           browserProviderName(hidden),
		DurationMS:         time.Since(started).Milliseconds(),
	}
	if tool == "browser.screenshot" {
		result.ScreenshotPath = firstStringValue(mapValue(output), "path")
		if result.ScreenshotPath != "" {
			result.ScreenshotMarkdown = fmt.Sprintf("![browser screenshot](%s)", result.ScreenshotPath)
		}
	}
	return result, nil
}

func (e *agentBrowserSessionEntry) executeLocked(ctx context.Context, tool string, args map[string]any) (string, map[string]any, any, error) {
	if tool != "browser.list_tabs" && tool != "browser.open" {
		e.clearObservedTabsLocked()
	}
	switch tool {
	case "browser.list_tabs":
		if e.noOpenTabs {
			e.rememberObservedTabsLocked(nil)
			return "agent_browser_tab_list", map[string]any{}, normalizedPagesOutput(nil), nil
		}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if err != nil {
			e.clearObservedTabsLocked()
			return "agent_browser_tab_list", map[string]any{}, nil, err
		}
		pages := normalizeAgentBrowserTabs(result.Data)
		e.rememberObservedTabsLocked(pages)
		return "agent_browser_tab_list", map[string]any{}, normalizedPagesOutput(pages), nil
	case "browser.open":
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, nil, errors.New("browser.open requires url")
		}
		rawTool, rawArgs, knownPages, err := e.openURLLocked(ctx, url, true)
		if err != nil {
			return rawTool, rawArgs, nil, err
		}
		e.invalidateSnapshotsLocked()
		e.noOpenTabs = false
		if knownPages != nil {
			e.rememberObservedTabsLocked(knownPages)
			return rawTool, rawArgs, normalizedPagesOutput(knownPages), nil
		}
		tabs, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if err != nil {
			e.clearObservedTabsLocked()
			return rawTool, rawArgs, nil, err
		}
		pages := normalizeAgentBrowserTabs(tabs.Data)
		e.rememberObservedTabsLocked(pages)
		return rawTool, rawArgs, normalizedPagesOutput(pages), nil
	case "browser.focus":
		tab, err := agentBrowserTabID(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"tab": tab}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_tab_switch", rawArgs)
		if err == nil {
			e.invalidateSnapshotsLocked()
		}
		output := agentBrowserOutput(result)
		output["page_id"] = "page_" + strings.TrimPrefix(tab, "t")
		output["selected"] = true
		return "agent_browser_tab_switch", rawArgs, output, err
	case "browser.close":
		tab, err := agentBrowserTabID(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"tab": tab}
		tabsBefore, listErr := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if listErr != nil {
			return "agent_browser_tab_list", nil, nil, listErr
		}
		lastTab := len(normalizeAgentBrowserTabs(tabsBefore.Data)) <= 1
		closeTool := "agent_browser_tab_close"
		closeArgs := rawArgs
		if lastTab {
			closeTool = "agent_browser_close"
			closeArgs = map[string]any{}
		}
		_, err = e.callAgentToolLocked(ctx, closeTool, closeArgs)
		if err != nil {
			return closeTool, closeArgs, nil, err
		}
		e.invalidateSnapshotsLocked()
		if lastTab {
			e.noOpenTabs = true
			return closeTool, closeArgs, normalizedPagesOutput(nil), nil
		}
		tabs, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if err != nil {
			return closeTool, closeArgs, nil, err
		}
		pages := normalizeAgentBrowserTabs(tabs.Data)
		e.rememberObservedTabsLocked(pages)
		return closeTool, closeArgs, normalizedPagesOutput(pages), nil
	case "browser.navigate":
		if err := e.selectRequestedTabLocked(ctx, args, true); err != nil {
			return "", nil, nil, err
		}
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, nil, errors.New("browser.navigate requires url")
		}
		rawArgs := map[string]any{"url": url}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_open", rawArgs)
		if err == nil {
			e.invalidateSnapshotsLocked()
			e.noOpenTabs = false
		}
		return "agent_browser_open", rawArgs, agentBrowserOutput(result), err
	case "browser.snapshot":
		if err := e.ensureSnapshotTargetLocked(ctx, args); err != nil {
			return "", nil, nil, err
		}
		rawArgs := agentBrowserSnapshotRawArgs()
		output, err := e.takeSnapshotLocked(ctx, args, rawArgs)
		return "agent_browser_snapshot", rawArgs, output, err
	case "browser.screenshot":
		if err := e.selectRequestedTabLocked(ctx, args, false); err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{}
		if boolArg(args, "full_page") {
			rawArgs["fullPage"] = true
		}
		if path := strings.TrimSpace(stringArg(args, "path")); path != "" {
			rawArgs["path"] = path
		}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_screenshot", rawArgs)
		output := agentBrowserOutput(result)
		if err == nil {
			output["content"] = agentBrowserImageContent(result.Content)
		}
		return "agent_browser_screenshot", rawArgs, output, err
	case "browser.wait":
		if err := e.selectRequestedTabLocked(ctx, args, false); err != nil {
			return "", nil, nil, err
		}
		if strings.EqualFold(strings.TrimSpace(stringArg(args, "mode")), "stable_state") {
			output, err := e.waitForStableStateLocked(ctx, args)
			return "agent_browser_stable_state", browserResultArguments(args), output, err
		}
		if value := strings.TrimSpace(stringArg(args, "text")); value != "" {
			rawArgs := map[string]any{"text": value}
			result, err := e.callAgentToolLocked(ctx, "agent_browser_wait_for_text", rawArgs)
			return "agent_browser_wait_for_text", rawArgs, agentBrowserOutput(result), err
		}
		if duration := intArg(args, "ms", intArg(args, "duration_ms", 0)); duration > 0 {
			rawArgs := map[string]any{"ms": duration}
			result, err := e.callAgentToolLocked(ctx, "agent_browser_wait_ms", rawArgs)
			return "agent_browser_wait_ms", rawArgs, agentBrowserOutput(result), err
		}
		rawArgs := map[string]any{"state": "domcontentloaded"}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_wait_for_load", rawArgs)
		return "agent_browser_wait_for_load", rawArgs, agentBrowserOutput(result), err
	case "browser.click":
		pageID, _, descriptor, state, err := e.resolveSnapshotRefLocked(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawRef, err := e.refreshSnapshotRefLocked(ctx, pageID, state, descriptor)
		if err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"selector": rawRef}
		beforeURL, _ := e.currentURLLocked(ctx)
		result, err := e.callAgentToolLocked(ctx, "agent_browser_click", rawArgs)
		if err != nil {
			return "agent_browser_click", rawArgs, nil, err
		}
		state.ActionTaken = true
		e.activeSnapshotPage = ""
		afterURL, _ := e.currentURLLocked(ctx)
		output := agentBrowserOutput(result)
		output["clicked"] = descriptor.ExternalRef
		output["snapshot_id"] = state.SnapshotID
		output["page_id"] = pageID
		output["fingerprint"] = descriptor.Fingerprint
		output["role"] = descriptor.Role
		output["accessible_name"] = descriptor.Name
		output["before_url"] = beforeURL
		output["url"] = afterURL
		output["url_changed"] = beforeURL != afterURL
		return "agent_browser_click", rawArgs, output, nil
	case "browser.type":
		text := stringArg(args, "text")
		if text == "" {
			text = stringArg(args, "value")
		}
		if hasElementRef(args) {
			pageID, _, descriptor, state, err := e.resolveSnapshotRefLocked(args)
			if err != nil {
				return "", nil, nil, err
			}
			rawRef, err := e.refreshSnapshotRefLocked(ctx, pageID, state, descriptor)
			if err != nil {
				return "", nil, nil, err
			}
			rawArgs := map[string]any{"selector": rawRef, "text": text}
			result, err := e.callAgentToolLocked(ctx, "agent_browser_fill", rawArgs)
			output := agentBrowserOutput(result)
			output["filled"] = descriptor.ExternalRef
			if err == nil {
				state.ActionTaken = true
				e.activeSnapshotPage = ""
				output["snapshot_id"] = state.SnapshotID
			}
			return "agent_browser_fill", rawArgs, output, err
		}
		if !shouldUseTypeText(args) {
			return "", nil, nil, errors.New("browser.type requires a snapshot ref or a focused-input mode")
		}
		rawArgs := map[string]any{"selector": ":focus", "text": text}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_type", rawArgs)
		return "agent_browser_type", rawArgs, agentBrowserOutput(result), err
	case "browser.select":
		pageID, _, descriptor, state, err := e.resolveSnapshotRefLocked(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawRef, err := e.refreshSnapshotRefLocked(ctx, pageID, state, descriptor)
		if err != nil {
			return "", nil, nil, err
		}
		values := browserSelectValues(args)
		if len(values) == 0 {
			return "", nil, nil, errors.New("browser.select requires value or values")
		}
		rawArgs := map[string]any{"selector": rawRef, "values": values}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_select", rawArgs)
		output := agentBrowserOutput(result)
		output["ref"] = descriptor.ExternalRef
		if err == nil {
			state.ActionTaken = true
			e.activeSnapshotPage = ""
			output["snapshot_id"] = state.SnapshotID
		}
		return "agent_browser_select", rawArgs, output, err
	default:
		return "", nil, nil, fmt.Errorf("unsupported browser automation tool %q", tool)
	}
}

func (a *AgentBrowserAdapter) ensureSessionLocked(ctx context.Context, hidden bool, profileKey string) (*agentBrowserSessionEntry, error) {
	presentation := "visible"
	if hidden {
		presentation = "hidden"
	}
	if a.entry != nil && a.entry.session.alive() && a.entry.profile == profileKey && a.entry.presentation == presentation {
		return a.entry, nil
	}
	preserveEmptyTabs := a.entry != nil && a.entry.noOpenTabs && a.entry.profile == profileKey && a.entry.presentation == presentation
	a.resetSessionLocked()
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	if a.commandPath == "" {
		command, err := resolveAgentBrowserCommand(adapterCfg.Command)
		if err != nil {
			return nil, err
		}
		a.commandPath = command
	}
	if !a.commandValidated {
		if err := validateAgentBrowserVersion(ctx, a.commandPath, adapterCfg.StartupTimeoutMS); err != nil {
			return nil, err
		}
		a.commandValidated = true
	}
	startupCtx, cancel := context.WithTimeout(ctx, time.Duration(adapterStartupTimeoutMS(adapterCfg.StartupTimeoutMS))*time.Millisecond)
	defer cancel()
	session, err := newAgentBrowserSession(startupCtx, adapterCfg, a.commandPath, a.namespace, hidden, profileKey)
	if err != nil {
		return nil, err
	}
	if err := session.initialize(startupCtx); err != nil {
		session.abort()
		return nil, fmt.Errorf("start agent-browser MCP session: %w", err)
	}
	a.nextGeneration++
	entry := &agentBrowserSessionEntry{
		adapter:      a,
		profile:      profileKey,
		presentation: presentation,
		session:      session,
		generation:   a.nextGeneration,
		snapshots:    map[string]*agentBrowserSnapshotState{},
		noOpenTabs:   preserveEmptyTabs,
		freshSession: true,
	}
	a.entry = entry
	return entry, nil
}

func (e *agentBrowserSessionEntry) callAgentToolLocked(ctx context.Context, name string, args map[string]any) (agentBrowserToolResult, error) {
	result, err := e.session.callTool(ctx, name, args)
	e.freshSession = false
	if err != nil && !isAgentBrowserActionError(err) {
		e.session.abort()
		e.invalidateSnapshotsLocked()
		e.clearObservedTabsLocked()
		if e.adapter.entry == e {
			e.adapter.entry = nil
		}
	}
	return result, err
}

func (a *AgentBrowserAdapter) resetSessionLocked() {
	if a.entry != nil {
		a.entry.session.close()
		a.entry = nil
	}
}

func (a *AgentBrowserAdapter) browserProfileKey(args map[string]any) string {
	ownerID := strings.TrimSpace(stringArg(args, "owner_id"))
	if ownerID == "" {
		ownerID = "owner"
	}
	profileID := strings.TrimSpace(stringArg(args, "browser_profile_id"))
	if profileID == "" {
		profileID = strings.TrimSpace(a.cfg.Tools.BrowserAutomation.Profile)
	}
	if profileID == "" {
		profileID = "default"
	}
	return ownerID + "\x00" + profileID
}

func (e *agentBrowserSessionEntry) openURLLocked(ctx context.Context, url string, reuseBlank bool) (string, map[string]any, []any, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil, nil, errors.New("browser URL is required")
	}
	if e.shouldOpenFreshVisibleSessionDirect(reuseBlank) {
		args := map[string]any{"url": url}
		_, err := e.callAgentToolLocked(ctx, "agent_browser_open", args)
		if err == nil {
			err = e.preserveFreshVisibleURLFragmentLocked(ctx, url)
		}
		if err == nil {
			e.noOpenTabs = false
		}
		return "agent_browser_open", args, nil, err
	}
	if e.noOpenTabs {
		args := map[string]any{"url": url}
		_, err := e.callAgentToolLocked(ctx, "agent_browser_open", args)
		if err == nil {
			e.noOpenTabs = false
		}
		return "agent_browser_open", args, nil, err
	}
	if reuseBlank {
		pages, observed := e.takeObservedTabsLocked()
		if !observed {
			tabs, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
			if err != nil {
				return "agent_browser_tab_list", nil, nil, err
			}
			pages = normalizeAgentBrowserTabs(tabs.Data)
		}
		if canReuseAgentBrowserBlankPage(pages) {
			args := map[string]any{"url": url}
			_, err := e.callAgentToolLocked(ctx, "agent_browser_open", args)
			if err != nil {
				return "agent_browser_open", args, nil, err
			}
			openedPages := cloneAgentBrowserPages(pages)
			page := mapValue(openedPages[0])
			page["url"] = url
			page["title"] = ""
			page["selected"] = true
			return "agent_browser_open", args, openedPages, nil
		}
	}
	args := map[string]any{"url": url}
	_, err := e.callAgentToolLocked(ctx, "agent_browser_tab_new", args)
	return "agent_browser_tab_new", args, nil, err
}

func (e *agentBrowserSessionEntry) shouldOpenFreshVisibleSessionDirect(reuseBlank bool) bool {
	return reuseBlank && e.freshSession && e.presentation == "visible"
}

func (e *agentBrowserSessionEntry) preserveFreshVisibleURLFragmentLocked(ctx context.Context, targetURL string) error {
	target, err := url.Parse(strings.TrimSpace(targetURL))
	if err != nil || target.Fragment == "" {
		return nil
	}
	if _, err := e.waitForStableStateLocked(ctx, map[string]any{
		"expected_url":    browserURLOrigin(target),
		"allow_no_change": true,
	}); err != nil {
		return err
	}
	currentURL, err := e.currentURLLocked(ctx)
	if err != nil {
		return err
	}
	reboundURL, ok := rebaseFreshVisibleURLRoute(targetURL, currentURL)
	if !ok {
		return nil
	}
	_, err = e.callAgentToolLocked(ctx, "agent_browser_open", map[string]any{"url": reboundURL})
	return err
}

func browserURLOrigin(parsed *url.URL) string {
	if parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host, Path: "/"}).String()
}

func rebaseFreshVisibleURLRoute(targetRaw, currentRaw string) (string, bool) {
	target, targetErr := url.Parse(strings.TrimSpace(targetRaw))
	current, currentErr := url.Parse(strings.TrimSpace(currentRaw))
	if targetErr != nil || currentErr != nil || target.Fragment == "" ||
		!browserURLsShareOrigin(target, current) ||
		target.Path == current.Path && target.Fragment == current.Fragment {
		return "", false
	}
	current.Path = target.Path
	current.RawPath = target.RawPath
	current.Fragment = target.Fragment
	current.RawFragment = target.RawFragment
	return current.String(), true
}

func browserURLsShareOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || left.Hostname() == "" || right.Hostname() == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		left.Port() == right.Port()
}

func canReuseAgentBrowserBlankPage(pages []any) bool {
	return len(pages) == 1 && isAboutBlank(firstStringValue(mapValue(pages[0]), "url"))
}

func (e *agentBrowserSessionEntry) rememberObservedTabsLocked(pages []any) {
	e.observedTabs = cloneAgentBrowserPages(pages)
	e.observedTabsValid = true
}

func (e *agentBrowserSessionEntry) takeObservedTabsLocked() ([]any, bool) {
	if !e.observedTabsValid {
		return nil, false
	}
	pages := e.observedTabs
	e.clearObservedTabsLocked()
	return pages, true
}

func (e *agentBrowserSessionEntry) clearObservedTabsLocked() {
	e.observedTabs = nil
	e.observedTabsValid = false
}

func cloneAgentBrowserPages(pages []any) []any {
	cloned := make([]any, 0, len(pages))
	for _, raw := range pages {
		cloned = append(cloned, cloneArgs(mapValue(raw)))
	}
	return cloned
}

func (e *agentBrowserSessionEntry) selectRequestedTabLocked(ctx context.Context, args map[string]any, invalidate bool) error {
	if strings.TrimSpace(stringArg(args, "page_id")) == "" {
		return nil
	}
	tab, err := agentBrowserTabID(args)
	if err != nil {
		return err
	}
	if _, err := e.callAgentToolLocked(ctx, "agent_browser_tab_switch", map[string]any{"tab": tab}); err != nil {
		return err
	}
	if invalidate {
		e.invalidateSnapshotsLocked()
	}
	return nil
}

func (e *agentBrowserSessionEntry) ensureSnapshotTargetLocked(ctx context.Context, args map[string]any) error {
	if pageID := strings.TrimSpace(stringArg(args, "page_id")); pageID != "" {
		return e.selectRequestedTabLocked(ctx, args, false)
	}
	if url := strings.TrimSpace(stringArg(args, "url")); url != "" {
		_, _, _, err := e.openURLLocked(ctx, url, true)
		if err == nil {
			e.invalidateSnapshotsLocked()
		}
		return err
	}
	return nil
}

func (e *agentBrowserSessionEntry) ensureSnapshotPageActiveLocked(pageID string) error {
	if e.activeSnapshotPage != pageID {
		return fmt.Errorf("stale or inactive snapshot for %s; take a new browser.snapshot", pageID)
	}
	return nil
}

func (e *agentBrowserSessionEntry) currentURLLocked(ctx context.Context) (string, error) {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_get_url", nil)
	if err != nil {
		return "", err
	}
	data := mapValue(result.Data)
	return firstStringValue(data, "url", "value", "result"), nil
}

func (e *agentBrowserSessionEntry) currentTitleLocked(ctx context.Context) (string, error) {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_get_title", nil)
	if err != nil {
		return "", err
	}
	data := mapValue(result.Data)
	return firstStringValue(data, "title", "value", "result"), nil
}

func (e *agentBrowserSessionEntry) currentPageIDLocked(ctx context.Context) string {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
	if err != nil {
		return ""
	}
	for _, page := range normalizeAgentBrowserTabs(result.Data) {
		values := mapValue(page)
		if boolValue(values["selected"]) {
			return firstStringValue(values, "page_id")
		}
	}
	return ""
}

func shouldAttachHiddenPageState(tool string, hidden bool) bool {
	return hidden && tool != "browser.close" && tool != "browser.list_tabs"
}

func (e *agentBrowserSessionEntry) withHiddenPageStateLocked(ctx context.Context, output any) any {
	normalized := map[string]any{}
	if current, ok := output.(map[string]any); ok {
		normalized = cloneArgs(current)
	} else {
		normalized["raw_output"] = output
	}
	url, urlErr := e.currentURLLocked(ctx)
	title, titleErr := e.currentTitleLocked(ctx)
	state := map[string]any{"url": url, "title": title}
	if urlErr != nil {
		state["url_error"] = urlErr.Error()
	}
	if titleErr != nil {
		state["title_error"] = titleErr.Error()
	}
	normalized["hidden_page_state"] = state
	normalized["current_url"] = url
	normalized["current_title"] = title
	return normalized
}

func agentBrowserTabID(args map[string]any) (string, error) {
	raw := strings.TrimSpace(stringArg(args, "page_id"))
	raw = strings.TrimPrefix(strings.ToLower(raw), "page_")
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return "", fmt.Errorf("browser page_id %q must identify a numeric agent-browser tab", raw)
	}
	return "t" + strconv.Itoa(value), nil
}

func normalizeAgentBrowserTabs(data any) []any {
	root := mapValue(data)
	rawTabs, _ := root["tabs"].([]any)
	pages := make([]any, 0, len(rawTabs))
	for _, raw := range rawTabs {
		tab := mapValue(raw)
		tabID := firstStringValue(tab, "tabId", "tab_id", "id")
		index := strings.TrimPrefix(strings.ToLower(tabID), "t")
		if _, err := strconv.Atoi(index); err != nil {
			continue
		}
		pages = append(pages, map[string]any{
			"page_id":  "page_" + index,
			"tab_id":   tabID,
			"url":      firstStringValue(tab, "url"),
			"title":    firstStringValue(tab, "title"),
			"selected": boolValue(tab["active"]),
			"type":     firstStringValue(tab, "type"),
			"label":    firstStringValue(tab, "label"),
		})
	}
	return pages
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

func (e *agentBrowserSessionEntry) withSessionMetadataLocked(output any, metadata browserModeFields) map[string]any {
	normalized := map[string]any{}
	if current, ok := output.(map[string]any); ok {
		normalized = cloneArgs(current)
	} else if output != nil {
		normalized["raw_output"] = output
	}
	normalized["session_generation"] = e.generation
	normalized["provider_session_ref"] = e.session.sessionName
	normalized["presentation"] = metadata.Presentation
	ownerID, profileID := splitBrowserProfileKey(e.profile)
	normalized["owner_id"] = ownerID
	normalized["profile_id"] = profileID
	if pages, ok := normalized["pages"].([]any); ok {
		annotated := make([]any, 0, len(pages))
		for _, raw := range pages {
			page := cloneArgs(mapValue(raw))
			page["session_generation"] = e.generation
			page["provider_session_ref"] = e.session.sessionName
			page["presentation"] = metadata.Presentation
			page["owner_id"] = ownerID
			page["profile_id"] = profileID
			annotated = append(annotated, page)
		}
		normalized["pages"] = annotated
	}
	if snapshot := mapValue(normalized["snapshot"]); snapshot != nil {
		snapshot = cloneArgs(snapshot)
		snapshot["session_generation"] = e.generation
		snapshot["provider_session_ref"] = e.session.sessionName
		snapshot["presentation"] = metadata.Presentation
		snapshot["owner_id"] = ownerID
		snapshot["profile_id"] = profileID
		normalized["snapshot"] = snapshot
	}
	return normalized
}

func splitBrowserProfileKey(key string) (string, string) {
	ownerID, profileID, found := strings.Cut(key, "\x00")
	if !found {
		return "", strings.TrimSpace(key)
	}
	return strings.TrimSpace(ownerID), strings.TrimSpace(profileID)
}

func agentBrowserOutput(result agentBrowserToolResult) map[string]any {
	out := map[string]any{}
	if data := mapValue(result.Data); data != nil {
		out = cloneArgs(data)
	} else if result.Data != nil {
		out["data"] = result.Data
	}
	if len(result.Content) > 0 {
		out["content"] = result.Content
	}
	return out
}

func agentBrowserImageContent(content []any) []any {
	images := make([]any, 0, len(content))
	for _, item := range content {
		if firstStringValue(mapValue(item), "type") == "image" {
			images = append(images, item)
		}
	}
	return images
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
