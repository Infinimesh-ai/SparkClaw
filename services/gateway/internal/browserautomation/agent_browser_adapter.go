package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type AgentBrowserAdapter struct {
	cfg                config.Config
	mu                 sync.Mutex
	session            *agentBrowserSession
	activeProfile      string
	activePresentation string
	commandPath        string
	commandValidated   bool
	namespace          string
	snapshots          map[string]*agentBrowserSnapshotState
	activeSnapshotPage string
	nextSnapshotID     uint64
	noOpenTabs         bool
	observedTabs       []any
	observedTabsValid  bool
}

func NewAdapter(cfg config.Config) Adapter {
	return &AgentBrowserAdapter{
		cfg:       cfg,
		namespace: newAgentBrowserNamespace(),
		snapshots: map[string]*agentBrowserSnapshotState{},
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
	hidden := shouldUseHiddenBrowserSession(metadata, args)
	profileKey := a.browserProfileKey(args)
	a.mu.Lock()
	defer a.mu.Unlock()
	session, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return Result{}, err
	}
	tabs, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
	if err != nil {
		return Result{}, err
	}
	pages := normalizeAgentBrowserTabs(tabs.Data)
	a.rememberObservedTabsLocked(pages)
	output := map[string]any{
		"ok":               true,
		"status":           "ok",
		"provider":         "agent-browser",
		"version":          session.version,
		"protocol_version": agentBrowserProtocolVersion,
		"session":          session.sessionName,
		"tabs":             pages,
	}
	return Result{
		Tool:           "browser.status",
		RawTool:        "agent_browser_tab_list",
		Arguments:      browserResultArguments(args),
		Output:         output,
		Pages:          pagesFromOutput("browser.list_tabs", output),
		BrowserMode:    metadata.BrowserMode,
		Presentation:   metadata.Presentation,
		SurfaceVisible: metadata.SurfaceVisible,
		Untrusted:      true,
		Provider:       browserProviderName(hidden),
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
	session, err := a.ensureSessionLocked(ctx, hidden, profileKey)
	if err != nil {
		return Result{}, err
	}
	rawTool, _, output, err := a.executeLocked(ctx, session, tool, args)
	if err != nil {
		return Result{}, err
	}
	if shouldAttachHiddenPageState(tool, hidden) {
		output = a.withHiddenPageStateLocked(ctx, session, output)
	}
	result := Result{
		Tool:           tool,
		RawTool:        rawTool,
		Arguments:      browserResultArguments(args),
		Output:         output,
		Text:           contentText(output),
		Pages:          pagesFromOutput(tool, output),
		BrowserMode:    metadata.BrowserMode,
		Presentation:   metadata.Presentation,
		SurfaceVisible: metadata.SurfaceVisible,
		Untrusted:      true,
		Provider:       browserProviderName(hidden),
		DurationMS:     time.Since(started).Milliseconds(),
	}
	if tool == "browser.screenshot" {
		result.ScreenshotPath = firstStringValue(mapValue(output), "path")
		if result.ScreenshotPath != "" {
			result.ScreenshotMarkdown = fmt.Sprintf("![browser screenshot](%s)", result.ScreenshotPath)
		}
	}
	return result, nil
}

func (a *AgentBrowserAdapter) executeLocked(ctx context.Context, session *agentBrowserSession, tool string, args map[string]any) (string, map[string]any, any, error) {
	if tool != "browser.list_tabs" && tool != "browser.open" {
		a.clearObservedTabsLocked()
	}
	switch tool {
	case "browser.list_tabs":
		if a.noOpenTabs {
			a.rememberObservedTabsLocked(nil)
			return "agent_browser_tab_list", map[string]any{}, normalizedPagesOutput(nil), nil
		}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
		if err != nil {
			a.clearObservedTabsLocked()
			return "agent_browser_tab_list", map[string]any{}, nil, err
		}
		pages := normalizeAgentBrowserTabs(result.Data)
		a.rememberObservedTabsLocked(pages)
		return "agent_browser_tab_list", map[string]any{}, normalizedPagesOutput(pages), nil
	case "browser.open":
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, nil, errors.New("browser.open requires url")
		}
		rawTool, rawArgs, knownPages, err := a.openURLLocked(ctx, session, url, true)
		if err != nil {
			return rawTool, rawArgs, nil, err
		}
		a.invalidateSnapshotsLocked()
		a.noOpenTabs = false
		if knownPages != nil {
			a.rememberObservedTabsLocked(knownPages)
			return rawTool, rawArgs, normalizedPagesOutput(knownPages), nil
		}
		tabs, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
		if err != nil {
			a.clearObservedTabsLocked()
			return rawTool, rawArgs, nil, err
		}
		pages := normalizeAgentBrowserTabs(tabs.Data)
		a.rememberObservedTabsLocked(pages)
		return rawTool, rawArgs, normalizedPagesOutput(pages), nil
	case "browser.focus":
		tab, err := agentBrowserTabID(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"tab": tab}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_switch", rawArgs)
		if err == nil {
			a.invalidateSnapshotsLocked()
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
		tabsBefore, listErr := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
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
		_, err = a.callAgentToolLocked(ctx, session, closeTool, closeArgs)
		if err != nil {
			return closeTool, closeArgs, nil, err
		}
		a.invalidateSnapshotsLocked()
		if lastTab {
			a.noOpenTabs = true
			return closeTool, closeArgs, normalizedPagesOutput(nil), nil
		}
		tabs, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
		if err != nil {
			return closeTool, closeArgs, nil, err
		}
		pages := normalizeAgentBrowserTabs(tabs.Data)
		a.rememberObservedTabsLocked(pages)
		return closeTool, closeArgs, normalizedPagesOutput(pages), nil
	case "browser.navigate":
		if err := a.selectRequestedTabLocked(ctx, session, args, true); err != nil {
			return "", nil, nil, err
		}
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, nil, errors.New("browser.navigate requires url")
		}
		rawArgs := map[string]any{"url": url}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_open", rawArgs)
		if err == nil {
			a.invalidateSnapshotsLocked()
			a.noOpenTabs = false
		}
		return "agent_browser_open", rawArgs, agentBrowserOutput(result), err
	case "browser.snapshot":
		if err := a.ensureSnapshotTargetLocked(ctx, session, args); err != nil {
			return "", nil, nil, err
		}
		rawArgs := agentBrowserSnapshotRawArgs()
		output, err := a.takeSnapshotLocked(ctx, session, args, rawArgs)
		return "agent_browser_snapshot", rawArgs, output, err
	case "browser.screenshot":
		if err := a.selectRequestedTabLocked(ctx, session, args, false); err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{}
		if boolArg(args, "full_page") {
			rawArgs["fullPage"] = true
		}
		if path := strings.TrimSpace(stringArg(args, "path")); path != "" {
			rawArgs["path"] = path
		}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_screenshot", rawArgs)
		output := agentBrowserOutput(result)
		if err == nil {
			output["content"] = agentBrowserImageContent(result.Content)
		}
		return "agent_browser_screenshot", rawArgs, output, err
	case "browser.wait":
		if err := a.selectRequestedTabLocked(ctx, session, args, false); err != nil {
			return "", nil, nil, err
		}
		if value := strings.TrimSpace(stringArg(args, "text")); value != "" {
			rawArgs := map[string]any{"text": value}
			result, err := a.callAgentToolLocked(ctx, session, "agent_browser_wait_for_text", rawArgs)
			return "agent_browser_wait_for_text", rawArgs, agentBrowserOutput(result), err
		}
		if duration := intArg(args, "ms", intArg(args, "duration_ms", 0)); duration > 0 {
			rawArgs := map[string]any{"ms": duration}
			result, err := a.callAgentToolLocked(ctx, session, "agent_browser_wait_ms", rawArgs)
			return "agent_browser_wait_ms", rawArgs, agentBrowserOutput(result), err
		}
		rawArgs := map[string]any{"state": "domcontentloaded"}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_wait_for_load", rawArgs)
		return "agent_browser_wait_for_load", rawArgs, agentBrowserOutput(result), err
	case "browser.click":
		pageID, _, descriptor, state, err := a.resolveSnapshotRefLocked(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawRef, err := a.refreshSnapshotRefLocked(ctx, session, pageID, state, descriptor)
		if err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"selector": rawRef}
		beforeURL, _ := a.currentURLLocked(ctx, session)
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_click", rawArgs)
		if err != nil {
			return "agent_browser_click", rawArgs, nil, err
		}
		state.ActionTaken = true
		a.activeSnapshotPage = ""
		afterURL, _ := a.currentURLLocked(ctx, session)
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
			pageID, _, descriptor, state, err := a.resolveSnapshotRefLocked(args)
			if err != nil {
				return "", nil, nil, err
			}
			rawRef, err := a.refreshSnapshotRefLocked(ctx, session, pageID, state, descriptor)
			if err != nil {
				return "", nil, nil, err
			}
			rawArgs := map[string]any{"selector": rawRef, "text": text}
			result, err := a.callAgentToolLocked(ctx, session, "agent_browser_fill", rawArgs)
			output := agentBrowserOutput(result)
			output["filled"] = descriptor.ExternalRef
			if err == nil {
				state.ActionTaken = true
				a.activeSnapshotPage = ""
				output["snapshot_id"] = state.SnapshotID
			}
			return "agent_browser_fill", rawArgs, output, err
		}
		if !shouldUseTypeText(args) {
			return "", nil, nil, errors.New("browser.type requires a snapshot ref or a focused-input mode")
		}
		rawArgs := map[string]any{"selector": ":focus", "text": text}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_type", rawArgs)
		return "agent_browser_type", rawArgs, agentBrowserOutput(result), err
	case "browser.select":
		pageID, _, descriptor, state, err := a.resolveSnapshotRefLocked(args)
		if err != nil {
			return "", nil, nil, err
		}
		rawRef, err := a.refreshSnapshotRefLocked(ctx, session, pageID, state, descriptor)
		if err != nil {
			return "", nil, nil, err
		}
		values := browserSelectValues(args)
		if len(values) == 0 {
			return "", nil, nil, errors.New("browser.select requires value or values")
		}
		rawArgs := map[string]any{"selector": rawRef, "values": values}
		result, err := a.callAgentToolLocked(ctx, session, "agent_browser_select", rawArgs)
		output := agentBrowserOutput(result)
		output["ref"] = descriptor.ExternalRef
		if err == nil {
			state.ActionTaken = true
			a.activeSnapshotPage = ""
			output["snapshot_id"] = state.SnapshotID
		}
		return "agent_browser_select", rawArgs, output, err
	default:
		return "", nil, nil, fmt.Errorf("unsupported browser automation tool %q", tool)
	}
}

func (a *AgentBrowserAdapter) ensureSessionLocked(ctx context.Context, hidden bool, profileKey string) (*agentBrowserSession, error) {
	presentation := "visible"
	if hidden {
		presentation = "hidden"
	}
	if a.session != nil && a.session.alive() && a.activeProfile == profileKey && a.activePresentation == presentation {
		return a.session, nil
	}
	preserveEmptyTabs := a.noOpenTabs && a.activeProfile == profileKey && a.activePresentation == presentation
	a.resetSessionLocked()
	a.noOpenTabs = preserveEmptyTabs
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
	a.session = session
	a.activeProfile = profileKey
	a.activePresentation = presentation
	return session, nil
}

func (a *AgentBrowserAdapter) callAgentToolLocked(ctx context.Context, session *agentBrowserSession, name string, args map[string]any) (agentBrowserToolResult, error) {
	result, err := session.callTool(ctx, name, args)
	if err != nil && !isAgentBrowserActionError(err) {
		session.abort()
		if a.session == session {
			a.session = nil
			a.activeProfile = ""
			a.activePresentation = ""
			a.invalidateSnapshotsLocked()
			a.clearObservedTabsLocked()
		}
	}
	return result, err
}

func (a *AgentBrowserAdapter) resetSessionLocked() {
	if a.session != nil {
		a.session.close()
		a.session = nil
	}
	a.activeProfile = ""
	a.activePresentation = ""
	a.invalidateSnapshotsLocked()
	a.noOpenTabs = false
	a.clearObservedTabsLocked()
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

func (a *AgentBrowserAdapter) openURLLocked(ctx context.Context, session *agentBrowserSession, url string, reuseBlank bool) (string, map[string]any, []any, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil, nil, errors.New("browser URL is required")
	}
	if a.noOpenTabs {
		args := map[string]any{"url": url}
		_, err := a.callAgentToolLocked(ctx, session, "agent_browser_open", args)
		if err == nil {
			a.noOpenTabs = false
		}
		return "agent_browser_open", args, nil, err
	}
	if reuseBlank {
		pages, observed := a.takeObservedTabsLocked()
		if !observed {
			tabs, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
			if err != nil {
				return "agent_browser_tab_list", nil, nil, err
			}
			pages = normalizeAgentBrowserTabs(tabs.Data)
		}
		if canReuseAgentBrowserBlankPage(pages) {
			args := map[string]any{"url": url}
			_, err := a.callAgentToolLocked(ctx, session, "agent_browser_open", args)
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
	_, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_new", args)
	return "agent_browser_tab_new", args, nil, err
}

func canReuseAgentBrowserBlankPage(pages []any) bool {
	return len(pages) == 1 && isAboutBlank(firstStringValue(mapValue(pages[0]), "url"))
}

func (a *AgentBrowserAdapter) rememberObservedTabsLocked(pages []any) {
	a.observedTabs = cloneAgentBrowserPages(pages)
	a.observedTabsValid = true
}

func (a *AgentBrowserAdapter) takeObservedTabsLocked() ([]any, bool) {
	if !a.observedTabsValid {
		return nil, false
	}
	pages := a.observedTabs
	a.clearObservedTabsLocked()
	return pages, true
}

func (a *AgentBrowserAdapter) clearObservedTabsLocked() {
	a.observedTabs = nil
	a.observedTabsValid = false
}

func cloneAgentBrowserPages(pages []any) []any {
	cloned := make([]any, 0, len(pages))
	for _, raw := range pages {
		cloned = append(cloned, cloneArgs(mapValue(raw)))
	}
	return cloned
}

func (a *AgentBrowserAdapter) selectRequestedTabLocked(ctx context.Context, session *agentBrowserSession, args map[string]any, invalidate bool) error {
	if strings.TrimSpace(stringArg(args, "page_id")) == "" {
		return nil
	}
	tab, err := agentBrowserTabID(args)
	if err != nil {
		return err
	}
	if _, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_switch", map[string]any{"tab": tab}); err != nil {
		return err
	}
	if invalidate {
		a.invalidateSnapshotsLocked()
	}
	return nil
}

func (a *AgentBrowserAdapter) ensureSnapshotTargetLocked(ctx context.Context, session *agentBrowserSession, args map[string]any) error {
	if pageID := strings.TrimSpace(stringArg(args, "page_id")); pageID != "" {
		return a.selectRequestedTabLocked(ctx, session, args, false)
	}
	if url := strings.TrimSpace(stringArg(args, "url")); url != "" {
		_, _, _, err := a.openURLLocked(ctx, session, url, true)
		if err == nil {
			a.invalidateSnapshotsLocked()
		}
		return err
	}
	return nil
}

func (a *AgentBrowserAdapter) ensureSnapshotPageActiveLocked(_ context.Context, _ *agentBrowserSession, pageID string) error {
	if a.activeSnapshotPage != pageID {
		return fmt.Errorf("stale or inactive snapshot for %s; take a new browser.snapshot", pageID)
	}
	return nil
}

func (a *AgentBrowserAdapter) currentURLLocked(ctx context.Context, session *agentBrowserSession) (string, error) {
	result, err := a.callAgentToolLocked(ctx, session, "agent_browser_get_url", nil)
	if err != nil {
		return "", err
	}
	data := mapValue(result.Data)
	return firstStringValue(data, "url", "value", "result"), nil
}

func (a *AgentBrowserAdapter) currentTitleLocked(ctx context.Context, session *agentBrowserSession) (string, error) {
	result, err := a.callAgentToolLocked(ctx, session, "agent_browser_get_title", nil)
	if err != nil {
		return "", err
	}
	data := mapValue(result.Data)
	return firstStringValue(data, "title", "value", "result"), nil
}

func (a *AgentBrowserAdapter) currentPageIDLocked(ctx context.Context, session *agentBrowserSession) string {
	result, err := a.callAgentToolLocked(ctx, session, "agent_browser_tab_list", nil)
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

func (a *AgentBrowserAdapter) withHiddenPageStateLocked(ctx context.Context, session *agentBrowserSession, output any) any {
	normalized := map[string]any{}
	if current, ok := output.(map[string]any); ok {
		normalized = cloneArgs(current)
	} else {
		normalized["raw_output"] = output
	}
	url, urlErr := a.currentURLLocked(ctx, session)
	title, titleErr := a.currentTitleLocked(ctx, session)
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
