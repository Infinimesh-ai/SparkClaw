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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

type AgentBrowserAdapter struct {
	cfg config.Config
	// mu guards the single Host-CDP entry and command cache. Chromium and its
	// profile are owned by browserd, so every logical browser scope attaches
	// through one MCP session.
	mu                 sync.Mutex
	entry              *agentBrowserSessionEntry
	commandPath        string
	commandValidated   bool
	namespace          string
	nextGeneration     uint64
	observeStableState func(context.Context, *agentBrowserSession) (browserStableObservation, error)
	callAgentTool      func(context.Context, *agentBrowserSession, string, map[string]any) (agentBrowserToolResult, error)
}

// agentBrowserSessionEntry owns the one live Host-CDP agent-browser session.
type agentBrowserSessionEntry struct {
	mu         sync.Mutex
	adapter    *AgentBrowserAdapter
	session    *agentBrowserSession
	generation uint64
	// The remaining state is guarded by mu. ownedTabs maps the provider's
	// stable per-session tab id to one logical SparkClaw scope. Existing tabs
	// are absent and therefore owner-controlled.
	snapshots          map[string]*agentBrowserSnapshotState
	activeSnapshotPage string
	nextSnapshotID     uint64
	pageGeneration     uint64
	ownedTabs          map[string]string
}

func NewAdapter(cfg config.Config) Adapter {
	adapter := &AgentBrowserAdapter{
		cfg:            cfg,
		namespace:      newAgentBrowserNamespace(),
		nextGeneration: uint64(time.Now().UTC().UnixMicro()),
	}
	adapter.entry = &agentBrowserSessionEntry{
		adapter: adapter, snapshots: map[string]*agentBrowserSnapshotState{},
		pageGeneration: 1, ownedTabs: map[string]string{},
	}
	return adapter
}

func (a *AgentBrowserAdapter) Close() error {
	closeSessionEntry(a.entry)
	return nil
}

func (a *AgentBrowserAdapter) ReleaseSession(args map[string]any) error {
	scope := a.browserProfileKey(args)
	a.entry.mu.Lock()
	defer a.entry.mu.Unlock()
	for tabID, owner := range a.entry.ownedTabs {
		if owner == scope {
			delete(a.entry.ownedTabs, tabID)
		}
	}
	a.entry.invalidateSnapshotsLocked()
	return nil
}

func (a *AgentBrowserAdapter) Health(ctx context.Context, args map[string]any) (Result, error) {
	started := time.Now()
	a.mu.Lock()
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	if a.commandPath == "" {
		command, err := resolveAgentBrowserCommand(adapterCfg.Command)
		if err != nil {
			a.mu.Unlock()
			return Result{}, err
		}
		a.commandPath = command
	}
	commandPath := a.commandPath
	a.mu.Unlock()
	preflight := inspectBrowserEnvironment(ctx, adapterCfg, commandPath)
	if preflight.providerVersionPinned {
		a.mu.Lock()
		a.commandValidated = true
		a.mu.Unlock()
	}
	metadata := browserModeMetadata(args, "autonomous")
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
	scope := a.browserProfileKey(args)

	entry, err := a.acquireSessionEntry(ctx)
	if err != nil {
		return Result{}, err
	}
	defer entry.mu.Unlock()
	rawTool, _, output, err := entry.executeLocked(ctx, tool, args, scope)
	if err != nil {
		return Result{}, err
	}
	if shouldAttachHiddenPageState(tool, hidden) {
		output = entry.withHiddenPageStateLocked(ctx, output)
	}
	output = entry.withSessionMetadataLocked(output, metadata, scope)
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

func (e *agentBrowserSessionEntry) executeLocked(ctx context.Context, tool string, args map[string]any, scope string) (string, map[string]any, any, error) {
	switch tool {
	case "browser.list_tabs":
		result, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if err != nil {
			return "agent_browser_tab_list", map[string]any{}, nil, err
		}
		pages := e.ownedPagesLocked(normalizeAgentBrowserTabs(result.Data), scope)
		return "agent_browser_tab_list", map[string]any{}, normalizedPagesOutput(pages), nil
	case "browser.open":
		url := strings.TrimSpace(stringArg(args, "url"))
		if url == "" {
			return "", nil, nil, errors.New("browser.open requires url")
		}
		rawTool, rawArgs, pages, err := e.openURLLocked(ctx, url, scope)
		if err != nil {
			return rawTool, rawArgs, nil, err
		}
		e.invalidateSnapshotsLocked()
		return rawTool, rawArgs, normalizedPagesOutput(pages), nil
	case "browser.focus":
		tab, err := agentBrowserTabID(args)
		if err != nil {
			return "", nil, nil, err
		}
		if err := e.requireOwnedTabLocked(tab, scope); err != nil {
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
		if err := e.requireOwnedTabLocked(tab, scope); err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"tab": tab}
		tabsBefore, listErr := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if listErr != nil {
			return "agent_browser_tab_list", nil, nil, listErr
		}
		allTabs := normalizeAgentBrowserTabs(tabsBefore.Data)
		if len(allTabs) == 1 {
			if _, err := e.callAgentToolLocked(ctx, "agent_browser_tab_new", map[string]any{"url": "about:blank"}); err != nil {
				return "agent_browser_tab_new", map[string]any{"url": "about:blank"}, nil, err
			}
		}
		_, err = e.callAgentToolLocked(ctx, "agent_browser_tab_close", rawArgs)
		if err != nil {
			return "agent_browser_tab_close", rawArgs, nil, err
		}
		delete(e.ownedTabs, tab)
		e.invalidateSnapshotsLocked()
		tabs, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
		if err != nil {
			return "agent_browser_tab_close", rawArgs, nil, err
		}
		pages := e.ownedPagesLocked(normalizeAgentBrowserTabs(tabs.Data), scope)
		return "agent_browser_tab_close", rawArgs, normalizedPagesOutput(pages), nil
	case "browser.navigate":
		if err := e.selectRequestedTabLocked(ctx, args, true, scope); err != nil {
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
		}
		return "agent_browser_open", rawArgs, agentBrowserOutput(result), err
	case "browser.snapshot":
		if err := e.ensureSnapshotTargetLocked(ctx, args, scope); err != nil {
			return "", nil, nil, err
		}
		rawArgs := agentBrowserSnapshotRawArgs()
		output, err := e.takeSnapshotLocked(ctx, args, rawArgs)
		return "agent_browser_snapshot", rawArgs, output, err
	case "browser.screenshot":
		if err := e.selectRequestedTabLocked(ctx, args, false, scope); err != nil {
			return "", nil, nil, err
		}
		if snapshotID := strings.TrimSpace(stringArg(args, "snapshot_id")); snapshotID != "" {
			pageID := strings.TrimSpace(stringArg(args, "page_id"))
			state := e.snapshots[pageID]
			if state == nil || state.ActionTaken || state.SnapshotID != snapshotID {
				return "", nil, nil, errorsForSnapshot("visual inspection snapshot is stale")
			}
			if requested := uint64Value(args["session_generation"]); requested != 0 && requested != e.generation {
				return "", nil, nil, errorsForSnapshot("visual inspection session generation is stale")
			}
			if requested := uint64Value(args["page_generation"]); requested != 0 && requested != e.pageGeneration {
				return "", nil, nil, errorsForSnapshot("visual inspection page generation is stale")
			}
			if requested := strings.TrimSpace(stringArg(args, "snapshot_digest")); requested != "" && requested != state.Digest {
				return "", nil, nil, errorsForSnapshot("visual inspection snapshot digest is stale")
			}
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
		if err := e.selectRequestedTabLocked(ctx, args, false, scope); err != nil {
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
		if err := e.selectOwnedPageLocked(ctx, pageID, scope); err != nil {
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
		e.pageGeneration++
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
			if err := e.selectOwnedPageLocked(ctx, pageID, scope); err != nil {
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
				e.pageGeneration++
				e.activeSnapshotPage = ""
				output["snapshot_id"] = state.SnapshotID
				output["page_id"] = pageID
				output["role"] = descriptor.Role
				output["accessible_name"] = descriptor.Name
			}
			return "agent_browser_fill", rawArgs, output, err
		}
		if !shouldUseTypeText(args) {
			return "", nil, nil, errors.New("browser.type requires a snapshot ref or a focused-input mode")
		}
		if err := e.selectRequestedTabLocked(ctx, args, false, scope); err != nil {
			return "", nil, nil, err
		}
		rawArgs := map[string]any{"selector": ":focus", "text": text}
		result, err := e.callAgentToolLocked(ctx, "agent_browser_type", rawArgs)
		return "agent_browser_type", rawArgs, agentBrowserOutput(result), err
	case "browser.select":
		pageID, _, descriptor, state, err := e.resolveSnapshotRefLocked(args)
		if err != nil {
			return "", nil, nil, err
		}
		if err := e.selectOwnedPageLocked(ctx, pageID, scope); err != nil {
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
			e.pageGeneration++
			e.activeSnapshotPage = ""
			output["snapshot_id"] = state.SnapshotID
			output["page_id"] = pageID
			output["role"] = descriptor.Role
			output["accessible_name"] = descriptor.Name
		}
		return "agent_browser_select", rawArgs, output, err
	default:
		return "", nil, nil, fmt.Errorf("unsupported browser automation tool %q", tool)
	}
}

// acquireSessionEntry returns the one live Host-CDP session with its entry
// lock held. Chromium and the profile outlive this MCP subprocess and remain
// owned by browserd.
func (a *AgentBrowserAdapter) acquireSessionEntry(ctx context.Context) (*agentBrowserSessionEntry, error) {
	entry := a.entry
	entry.mu.Lock()
	if entry.session == nil || !entry.session.alive() {
		if err := entry.initializeSessionLocked(ctx); err != nil {
			entry.mu.Unlock()
			return nil, err
		}
	}
	return entry, nil
}

// initializeSessionLocked starts or replaces only the private agent-browser
// MCP process. A replacement loses every in-memory tab grant because existing
// Chromium tabs must fail closed after the provider session changes.
func (e *agentBrowserSessionEntry) initializeSessionLocked(ctx context.Context) error {
	a := e.adapter
	a.mu.Lock()
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	commandPath := a.commandPath
	commandValidated := a.commandValidated
	a.mu.Unlock()
	if commandPath == "" {
		command, err := resolveAgentBrowserCommand(adapterCfg.Command)
		if err != nil {
			return err
		}
		commandPath = command
	}
	if !commandValidated {
		if err := validateAgentBrowserVersion(ctx, commandPath, adapterCfg.StartupTimeoutMS); err != nil {
			return err
		}
	}
	if e.session != nil {
		e.session.close()
		e.setSessionLocked(nil, e.generation)
	}
	connectTimeoutMS := adapterCfg.HostCDP.ConnectTimeoutMS
	if connectTimeoutMS <= 0 {
		connectTimeoutMS = adapterCfg.StartupTimeoutMS
	}
	startupCtx, cancel := context.WithTimeout(ctx, time.Duration(adapterStartupTimeoutMS(connectTimeoutMS))*time.Millisecond)
	defer cancel()
	session, err := newAgentBrowserSession(startupCtx, adapterCfg, commandPath, a.namespace)
	if err != nil {
		return err
	}
	if err := session.initialize(startupCtx); err != nil {
		session.abort()
		return fmt.Errorf("start agent-browser MCP session: %w", err)
	}
	e.snapshots = map[string]*agentBrowserSnapshotState{}
	e.activeSnapshotPage = ""
	e.ownedTabs = map[string]string{}
	a.mu.Lock()
	a.commandPath = commandPath
	a.commandValidated = true
	a.nextGeneration++
	generation := a.nextGeneration
	a.mu.Unlock()
	e.setSessionLocked(session, generation)
	return nil
}

// setSessionLocked publishes the session pointer and generation under both
// entry.mu (already held) and the adapter lock, keeping them readable from
// Health without waiting on a busy entry.
func (e *agentBrowserSessionEntry) setSessionLocked(session *agentBrowserSession, generation uint64) {
	e.adapter.mu.Lock()
	e.session = session
	e.generation = generation
	e.adapter.mu.Unlock()
}

// closeSessionEntry waits for any in-flight call against the entry to finish,
// then detaches the MCP subprocess without stopping browserd or Chromium.
func closeSessionEntry(e *agentBrowserSessionEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session != nil {
		e.session.close()
		e.setSessionLocked(nil, e.generation)
	}
	e.ownedTabs = map[string]string{}
	e.invalidateSnapshotsLocked()
}

func (e *agentBrowserSessionEntry) callAgentToolLocked(ctx context.Context, name string, args map[string]any) (agentBrowserToolResult, error) {
	var result agentBrowserToolResult
	var err error
	if e.adapter.callAgentTool != nil {
		result, err = e.adapter.callAgentTool(ctx, e.session, name, args)
	} else {
		result, err = e.session.callTool(ctx, name, args)
	}
	if err != nil && !isAgentBrowserActionError(err) {
		e.session.abort()
		e.invalidateSnapshotsLocked()
		e.ownedTabs = map[string]string{}
	}
	return result, err
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

func (e *agentBrowserSessionEntry) openURLLocked(ctx context.Context, url, scope string) (string, map[string]any, []any, error) {
	if strings.TrimSpace(url) == "" {
		return "", nil, nil, errors.New("browser URL is required")
	}
	beforeResult, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
	if err != nil {
		return "agent_browser_tab_list", nil, nil, err
	}
	beforeIDs := agentBrowserPageTabIDs(normalizeAgentBrowserTabs(beforeResult.Data))
	args := map[string]any{"url": url}
	if _, err := e.callAgentToolLocked(ctx, "agent_browser_tab_new", args); err != nil {
		return "agent_browser_tab_new", args, nil, err
	}
	afterResult, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
	if err != nil {
		return "agent_browser_tab_list", nil, nil, err
	}
	after := normalizeAgentBrowserTabs(afterResult.Data)
	created := make([]string, 0, 1)
	for tabID := range agentBrowserPageTabIDs(after) {
		if _, existed := beforeIDs[tabID]; !existed {
			created = append(created, tabID)
		}
	}
	if len(created) != 1 {
		return "agent_browser_tab_new", args, nil, fmt.Errorf(
			"browser tab ownership is ambiguous: expected one new tab, observed %d", len(created),
		)
	}
	tabID := created[0]
	e.ownedTabs[tabID] = scope
	if _, err := e.callAgentToolLocked(ctx, "agent_browser_tab_switch", map[string]any{"tab": tabID}); err != nil {
		return "agent_browser_tab_switch", map[string]any{"tab": tabID}, nil, err
	}
	listed, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
	if err != nil {
		return "agent_browser_tab_list", nil, nil, err
	}
	pages := e.ownedPagesLocked(normalizeAgentBrowserTabs(listed.Data), scope)
	return "agent_browser_tab_new", args, pages, nil
}

func browserURLsShareOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || left.Hostname() == "" || right.Hostname() == "" {
		return false
	}
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		left.Port() == right.Port()
}

func agentBrowserPageTabIDs(pages []any) map[string]struct{} {
	ids := make(map[string]struct{}, len(pages))
	for _, raw := range pages {
		if tabID := firstStringValue(mapValue(raw), "tab_id"); tabID != "" {
			ids[tabID] = struct{}{}
		}
	}
	return ids
}

func (e *agentBrowserSessionEntry) ownedPagesLocked(pages []any, scope string) []any {
	present := agentBrowserPageTabIDs(pages)
	for tabID := range e.ownedTabs {
		if _, ok := present[tabID]; !ok {
			delete(e.ownedTabs, tabID)
		}
	}
	owned := make([]any, 0, len(pages))
	for _, raw := range pages {
		page := mapValue(raw)
		if e.ownedTabs[firstStringValue(page, "tab_id")] == scope {
			owned = append(owned, cloneArgs(page))
		}
	}
	return owned
}

func (e *agentBrowserSessionEntry) requireOwnedTabLocked(tab, scope string) error {
	if owner, ok := e.ownedTabs[tab]; !ok || owner != scope {
		return fmt.Errorf("browser tab %s is not owned by the active SparkClaw scope", tab)
	}
	return nil
}

func (e *agentBrowserSessionEntry) currentOwnedTabLocked(ctx context.Context, scope string) (string, error) {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_tab_list", nil)
	if err != nil {
		return "", err
	}
	pages := normalizeAgentBrowserTabs(result.Data)
	e.ownedPagesLocked(pages, scope)
	for _, raw := range pages {
		page := mapValue(raw)
		if boolValue(page["selected"]) {
			tabID := firstStringValue(page, "tab_id")
			if err := e.requireOwnedTabLocked(tabID, scope); err != nil {
				return "", err
			}
			return tabID, nil
		}
	}
	return "", errors.New("agent-browser did not report an active tab")
}

func (e *agentBrowserSessionEntry) selectRequestedTabLocked(ctx context.Context, args map[string]any, invalidate bool, scope string) error {
	tab := ""
	var err error
	if strings.TrimSpace(stringArg(args, "page_id")) == "" {
		tab, err = e.currentOwnedTabLocked(ctx, scope)
	} else {
		tab, err = agentBrowserTabID(args)
		if err == nil {
			err = e.requireOwnedTabLocked(tab, scope)
		}
	}
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

func (e *agentBrowserSessionEntry) selectOwnedPageLocked(ctx context.Context, pageID, scope string) error {
	return e.selectRequestedTabLocked(ctx, map[string]any{"page_id": pageID}, false, scope)
}

func (e *agentBrowserSessionEntry) ensureSnapshotTargetLocked(ctx context.Context, args map[string]any, scope string) error {
	if pageID := strings.TrimSpace(stringArg(args, "page_id")); pageID != "" {
		return e.selectRequestedTabLocked(ctx, args, false, scope)
	}
	if url := strings.TrimSpace(stringArg(args, "url")); url != "" {
		_, _, _, err := e.openURLLocked(ctx, url, scope)
		if err == nil {
			e.invalidateSnapshotsLocked()
		}
		return err
	}
	return e.selectRequestedTabLocked(ctx, args, false, scope)
}

func (e *agentBrowserSessionEntry) ensureSnapshotPageActiveLocked(pageID string) error {
	if e.activeSnapshotPage != pageID {
		return &app.CodedToolError{
			Code: app.ToolErrorSnapshotStale,
			Err:  fmt.Errorf("stale or inactive snapshot for %s; take a new browser.snapshot", pageID),
		}
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
		tabID = "t" + index
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

func (e *agentBrowserSessionEntry) withSessionMetadataLocked(output any, metadata browserModeFields, scope string) map[string]any {
	normalized := map[string]any{}
	if current, ok := output.(map[string]any); ok {
		normalized = cloneArgs(current)
	} else if output != nil {
		normalized["raw_output"] = output
	}
	normalized["session_generation"] = e.generation
	normalized["page_generation"] = e.pageGeneration
	normalized["provider_session_ref"] = e.session.sessionName
	normalized["presentation"] = metadata.Presentation
	ownerID, profileID := splitBrowserProfileKey(scope)
	normalized["owner_id"] = ownerID
	normalized["profile_id"] = profileID
	if pages, ok := normalized["pages"].([]any); ok {
		annotated := make([]any, 0, len(pages))
		for _, raw := range pages {
			page := cloneArgs(mapValue(raw))
			page["session_generation"] = e.generation
			page["page_generation"] = e.pageGeneration
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
		snapshot["page_generation"] = e.pageGeneration
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
