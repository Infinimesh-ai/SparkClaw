package browserautomation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
		"ok":                    status.Configured && status.State == browsercontrol.StateReady,
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

func (a *PlaywrightExtensionAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.releaseLocked(context.Background())
}

func (a *PlaywrightExtensionAdapter) ReleaseSession(args map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.session == nil || a.scope != playwrightScope(a.cfg, args) {
		return nil
	}
	return a.releaseLocked(context.Background())
}

func (a *PlaywrightExtensionAdapter) acquireSessionLocked(ctx context.Context, args map[string]any) (browsercontrol.Session, error) {
	scope := playwrightScope(a.cfg, args)
	if a.session != nil {
		if a.scope != scope {
			return nil, errors.New("browser profile is busy with another SparkClaw scope")
		}
		return a.session, nil
	}
	if a.controller == nil {
		return nil, errors.New("playwright extension controller is unavailable")
	}
	a.nextTaskID++
	taskID := playwrightTaskID(scope, a.nextTaskID)
	sessionCtx, sessionCancel := context.WithCancel(context.WithoutCancel(ctx))
	session, err := a.controller.AcquireSession(sessionCtx, taskID, 0, playwrightExtensionSessionTTL)
	if err != nil {
		sessionCancel()
		return nil, err
	}
	a.session = session
	a.sessionCancel = sessionCancel
	a.scope = scope
	a.providerSessionRef = playwrightProviderSessionRef(session.Lease().SessionID)
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
	return session, nil
}

func (a *PlaywrightExtensionAdapter) releaseLocked(ctx context.Context) error {
	if a.session == nil {
		return nil
	}
	session := a.session
	sessionCancel := a.sessionCancel
	a.session = nil
	a.sessionCancel = nil
	a.scope = ""
	a.providerSessionRef = ""
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	err := session.Release(releaseCtx)
	if sessionCancel != nil {
		sessionCancel()
	}
	return err
}

func (a *PlaywrightExtensionAdapter) executeControllerLocked(ctx context.Context, operation string, args map[string]any) (map[string]any, error) {
	if a.session == nil {
		return nil, errors.New("playwright extension session is unavailable")
	}
	output, err := a.session.Execute(ctx, operation, args)
	if err != nil {
		switch browsercontrol.ErrorCode(err) {
		case browsercontrol.CodeControllerStale, browsercontrol.CodeSessionNotFound, browsercontrol.CodeSessionStale:
			_ = a.releaseLocked(context.Background())
		}
		return nil, err
	}
	return output, nil
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

func (a *PlaywrightExtensionAdapter) takeSnapshotLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	input := optionalPageArgs(pageID)
	output, err := a.executeControllerLocked(ctx, "page.snapshot", input)
	if err != nil {
		return nil, err
	}
	page := mapValue(output["page"])
	pageID = firstStringValue(page, "page_id")
	if pageID == "" {
		return nil, errorsForPlaywrightSnapshot("controller omitted the active task page")
	}
	read, _ := a.executeControllerLocked(ctx, "page.read", map[string]any{"page_id": pageID, "max_chars": 120000})
	if a.session == nil {
		return nil, errors.New("playwright extension session was lost while taking the snapshot")
	}
	readPage := mapValue(read["page"])
	pageText := firstStringValue(readPage, "text")
	url := firstNonEmptyBrowserString(firstStringValue(page, "url"), firstStringValue(readPage, "url"))
	title := firstNonEmptyBrowserString(firstStringValue(page, "title"), firstStringValue(readPage, "title"))
	refs := playwrightSnapshotRefs(output["snapshot"])
	goal := strings.TrimSpace(stringArg(args, "interaction_goal"))
	allRefs := buildBrowserSnapshotRefs(refs, goal)
	rawTreeBytes, _ := json.Marshal(output["snapshot"])
	rawTree := string(rawTreeBytes)
	contentDigest := digestBrowserStableContent(title, pageText)
	digest := digestBrowserSnapshot(url, title, rawTree, pageText, allRefs)
	previous := a.snapshots[pageID]
	previousID := ""
	repeated := false
	if previous != nil && previous.ActionTaken {
		previousID = previous.SnapshotID
		repeated = previous.ContentDigest != "" && previous.ContentDigest == contentDigest
	}

	a.nextSnapshotID++
	lease := a.session.Lease()
	snapshotID := browserSnapshotID(uint64(lease.SessionGeneration), pageID, a.nextSnapshotID)
	ranked := append([]*browserSnapshotRef{}, allRefs...)
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score == ranked[j].Score {
			return ranked[i].Index < ranked[j].Index
		}
		return ranked[i].Score > ranked[j].Score
	})
	if len(ranked) > browserSnapshotControlLimit {
		ranked = ranked[:browserSnapshotControlLimit]
	}
	state := &browserSnapshotState{
		SnapshotID: snapshotID, PageID: pageID, URL: url, Digest: digest,
		ContentDigest: contentDigest, Refs: map[string]*browserSnapshotRef{},
	}
	controls := make([]any, 0, len(ranked))
	actionRefs := make([]string, 0, len(ranked))
	for _, descriptor := range ranked {
		descriptor.ExternalRef = snapshotID + ":" + descriptor.RawRef + ":" + descriptor.Fingerprint[:16]
		state.Refs[descriptor.ExternalRef] = descriptor
		controls = append(controls, browserSnapshotControl(descriptor))
		if descriptor.Clickable {
			actionRefs = append(actionRefs, descriptor.ExternalRef)
		}
	}
	safeTree := projectBrowserTreeRefs(ranked)
	auth := inferBrowserSnapshotAuth(map[string]any{"text": pageText, "tree": safeTree}, title, url, allRefs)
	a.snapshots[pageID] = state
	a.activeSnapshotPage = pageID
	text := strings.Join(nonEmptyStrings("Page: "+url, safeTree), "\n")
	snapshot := map[string]any{
		"schema_version": "browser_interaction_snapshot_v1", "snapshot_id": snapshotID,
		"previous_snapshot_id": previousID, "page_id": pageID, "url": url, "title": title,
		"interaction_goal": goal, "digest": digest, "content_digest": contentDigest, "repeated": repeated,
		"controls_total": len(allRefs), "controls_returned": len(controls), "truncated": len(allRefs) > len(controls),
		"browser_page_auth_state":      firstStringValue(auth, "authState"),
		"browser_page_auth_confidence": firstStringValue(auth, "authConfidence"),
		"browser_page_auth_signals":    firstStringSliceValue(auth["authSignals"]),
		"aria":                         safeTree, "controls": controls, "refs": controls, "action_refs": actionRefs,
	}
	return map[string]any{
		"text": text, "snapshot_id": snapshotID, "page_id": pageID, "digest": digest,
		"content_digest": contentDigest, "repeated": repeated, "snapshot": snapshot,
		"browser_page_auth_state":      firstStringValue(auth, "authState"),
		"browser_page_auth_confidence": firstStringValue(auth, "authConfidence"),
		"browser_page_auth_signals":    firstStringSliceValue(auth["authSignals"]),
		"auth_challenge_detected":      boolValue(auth["authChallengeDetected"]),
		"content":                      []any{map[string]any{"type": "text", "text": text}},
	}, nil
}

func (a *PlaywrightExtensionAdapter) takeScreenshotLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	if snapshotID := strings.TrimSpace(stringArg(args, "snapshot_id")); snapshotID != "" {
		state := a.snapshots[pageID]
		lease := a.session.Lease()
		if state == nil || state.ActionTaken || state.SnapshotID != snapshotID ||
			(uint64Value(args["session_generation"]) != 0 && uint64Value(args["session_generation"]) != uint64(lease.SessionGeneration)) ||
			(uint64Value(args["page_generation"]) != 0 && uint64Value(args["page_generation"]) != uint64(lease.PageGeneration)) ||
			(strings.TrimSpace(stringArg(args, "snapshot_digest")) != "" && strings.TrimSpace(stringArg(args, "snapshot_digest")) != state.Digest) {
			return nil, errorsForPlaywrightSnapshot("visual inspection snapshot is stale")
		}
	}
	input := optionalPageArgs(pageID)
	if boolArg(args, "full_page") {
		input["full_page"] = true
	}
	output, err := a.executeControllerLocked(ctx, "page.screenshot", input)
	if err != nil {
		return nil, err
	}
	screenshot := mapValue(output["screenshot"])
	data := firstStringValue(screenshot, "data_base64")
	mimeType := firstNonEmptyBrowserString(firstStringValue(screenshot, "mime_type"), "image/png")
	if data == "" {
		return nil, errors.New("playwright extension returned no screenshot data")
	}
	return map[string]any{
		"page":    output["page"],
		"content": []any{map[string]any{"type": "image", "mimeType": mimeType, "data": data}},
	}, nil
}

func (a *PlaywrightExtensionAdapter) clickLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageID, descriptor, state, rawRef, err := a.resolveSnapshotRefLocked(ctx, args)
	if err != nil {
		return nil, err
	}
	before, _ := a.pageInfoLocked(ctx, pageID)
	output, err := a.executeControllerLocked(ctx, "page.click", map[string]any{"page_id": pageID, "ref": rawRef})
	if err != nil {
		return nil, err
	}
	state.ActionTaken = true
	a.activeSnapshotPage = ""
	after := mapValue(output["page"])
	return map[string]any{
		"clicked": descriptor.ExternalRef, "snapshot_id": state.SnapshotID, "page_id": pageID,
		"fingerprint": descriptor.Fingerprint, "role": descriptor.Role, "accessible_name": descriptor.Name,
		"before_url": firstStringValue(before, "url"), "url": firstStringValue(after, "url"),
		"url_changed": firstStringValue(before, "url") != firstStringValue(after, "url"),
	}, nil
}

func (a *PlaywrightExtensionAdapter) typeLocked(ctx context.Context, args map[string]any) (map[string]any, string, error) {
	text := stringArg(args, "text")
	if text == "" {
		text = stringArg(args, "value")
	}
	if hasElementRef(args) {
		pageID, descriptor, state, rawRef, err := a.resolveSnapshotRefLocked(ctx, args)
		if err != nil {
			return nil, "playwright_mcp.page.fill", err
		}
		output, err := a.executeControllerLocked(ctx, "page.fill", map[string]any{"page_id": pageID, "ref": rawRef, "text": text})
		if err != nil {
			return nil, "playwright_mcp.page.fill", err
		}
		state.ActionTaken = true
		a.activeSnapshotPage = ""
		return map[string]any{
			"page": output["page"], "filled": descriptor.ExternalRef, "snapshot_id": state.SnapshotID,
			"page_id": pageID, "role": descriptor.Role, "accessible_name": descriptor.Name,
		}, "playwright_mcp.page.fill", nil
	}
	if !shouldUseTypeText(args) {
		return nil, "playwright_mcp.page.type", errors.New("browser.type requires a snapshot ref or a focused-input mode")
	}
	input := map[string]any{"text": text, "focused": true}
	if pageID := normalizePlaywrightPageID(stringArg(args, "page_id")); pageID != "" {
		input["page_id"] = pageID
	}
	output, err := a.executeControllerLocked(ctx, "page.type", input)
	return output, "playwright_mcp.page.type", err
}

func (a *PlaywrightExtensionAdapter) selectLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	pageID, descriptor, state, rawRef, err := a.resolveSnapshotRefLocked(ctx, args)
	if err != nil {
		return nil, err
	}
	values := browserSelectValues(args)
	if len(values) == 0 {
		return nil, errors.New("browser.select requires value or values")
	}
	output, err := a.executeControllerLocked(ctx, "page.select", map[string]any{
		"page_id": pageID, "ref": rawRef, "values": values,
	})
	if err != nil {
		return nil, err
	}
	state.ActionTaken = true
	a.activeSnapshotPage = ""
	return map[string]any{
		"page": output["page"], "ref": descriptor.ExternalRef, "snapshot_id": state.SnapshotID,
		"page_id": pageID, "role": descriptor.Role, "accessible_name": descriptor.Name,
	}, nil
}

func (a *PlaywrightExtensionAdapter) resolveSnapshotRefLocked(
	ctx context.Context,
	args map[string]any,
) (string, *browserSnapshotRef, *browserSnapshotState, string, error) {
	external := strings.TrimSpace(stringArg(args, "uid"))
	if external == "" {
		external = strings.TrimSpace(stringArg(args, "ref"))
	}
	if external == "" {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("browser interaction requires a snapshot ref")
	}
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	if pageID == "" {
		for candidate, state := range a.snapshots {
			if state.Refs[external] != nil {
				pageID = candidate
				break
			}
		}
	}
	state := a.snapshots[pageID]
	if state == nil || state.ActionTaken || a.activeSnapshotPage != pageID {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("stale or unknown snapshot; take a new browser.snapshot")
	}
	if requested := strings.TrimSpace(stringArg(args, "snapshot_id")); requested != "" && requested != state.SnapshotID {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("stale or mismatched snapshot_id; take a new browser.snapshot")
	}
	descriptor := state.Refs[external]
	if descriptor == nil {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("stale or unknown snapshot ref; take a new browser.snapshot")
	}
	info, err := a.pageInfoLocked(ctx, pageID)
	if err != nil {
		return "", nil, nil, "", err
	}
	if firstStringValue(info, "url") != state.URL {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("active page URL changed; take a new browser.snapshot")
	}
	fresh, err := a.executeControllerLocked(ctx, "page.snapshot", map[string]any{"page_id": pageID})
	if err != nil {
		return "", nil, nil, "", err
	}
	matches := []string{}
	for _, current := range buildBrowserSnapshotRefs(playwrightSnapshotRefs(fresh["snapshot"]), "") {
		if current.Fingerprint == descriptor.Fingerprint {
			matches = append(matches, current.RawRef)
		}
	}
	if len(matches) == 1 {
		return pageID, descriptor, state, matches[0], nil
	}
	if len(matches) > 1 {
		return "", nil, nil, "", errorsForPlaywrightSnapshot("snapshot ref became ambiguous; take a new browser.snapshot")
	}
	return "", nil, nil, "", errorsForPlaywrightSnapshot("snapshot ref changed or is unavailable; take a new browser.snapshot")
}

func (a *PlaywrightExtensionAdapter) pageInfoLocked(ctx context.Context, pageID string) (map[string]any, error) {
	output, err := a.executeControllerLocked(ctx, "page.info", optionalPageArgs(pageID))
	if err != nil {
		return nil, err
	}
	page := mapValue(output["page"])
	if page == nil {
		return nil, errors.New("playwright extension omitted page metadata")
	}
	return page, nil
}

func (a *PlaywrightExtensionAdapter) waitForReadyLocked(ctx context.Context, pageID string, timeoutMS int) error {
	timeoutMS = boundedBrowserSettleValue(timeoutMS, 500, 120000)
	readyCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	for {
		info, err := a.pageInfoLocked(readyCtx, pageID)
		if err == nil {
			state := strings.ToLower(strings.TrimSpace(firstStringValue(info, "ready_state")))
			if state == "interactive" || state == "complete" {
				return nil
			}
		}
		select {
		case <-readyCtx.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.New("browser_settle_timeout: page did not reach a ready state")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func (a *PlaywrightExtensionAdapter) waitForStableStateLocked(ctx context.Context, args map[string]any) (map[string]any, error) {
	lease := a.session.Lease()
	if requested := uint64Value(args["session_generation"]); requested != 0 && requested != uint64(lease.SessionGeneration) {
		return nil, errors.New("browser_session_stale: requested session generation is no longer active")
	}
	adapterCfg := a.cfg.Adapters.BrowserAutomation
	timeoutMS := boundedBrowserSettleValue(intArg(args, "timeout_ms", adapterCfg.SettleTimeoutMS), 500, 120000)
	quietMS := boundedBrowserSettleValue(intArg(args, "quiet_period_ms", adapterCfg.SettleQuietPeriodMS), 100, 10000)
	pollMS := boundedBrowserSettleValue(intArg(args, "poll_interval_ms", adapterCfg.SettlePollIntervalMS), 25, quietMS)
	settleCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	pageID := normalizePlaywrightPageID(stringArg(args, "page_id"))
	expectedURL := firstNonEmptyBrowserString(stringArg(args, "expected_url"), stringArg(args, "canonical_url"))
	targetKind := app.BrowserTargetKind(strings.TrimSpace(stringArg(args, "target_kind")))
	beforeDigest := strings.TrimSpace(stringArg(args, "before_digest"))
	allowNoChange := boolArg(args, "allow_no_change")
	requiredStable := maxInt(2, quietMS/pollMS+1)
	stableCount := 0
	observations := 0
	routeRebinds := 0
	var last browserStableObservation
	var stableSince time.Time
	for {
		read, err := a.executeControllerLocked(settleCtx, "page.read", mergePageArgs(pageID, map[string]any{"max_chars": 120000}))
		if err == nil {
			page := mapValue(read["page"])
			observation := browserStableObservation{
				URL: firstStringValue(page, "url"), Title: firstStringValue(page, "title"),
				Digest: digestBrowserStableContent(firstStringValue(page, "title"), firstStringValue(page, "text")),
			}
			observations++
			rebound := ""
			if expectedURL != "" {
				rebound, err = settleBrowserRoute(expectedURL, observation.URL, targetKind)
				if err != nil {
					return nil, err
				}
			}
			if observation == last {
				stableCount++
			} else {
				last = observation
				stableCount = 1
				stableSince = time.Now().UTC()
			}
			if rebound != "" && rebound != observation.URL && stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond {
				if routeRebinds >= adapterCfg.RouteRebindLimit {
					return nil, errors.New("browser_route_diverged: same-origin route rebind limit exceeded")
				}
				if _, err := a.executeControllerLocked(settleCtx, "page.navigate", mergePageArgs(pageID, map[string]any{"url": rebound})); err != nil {
					return nil, fmt.Errorf("browser_renderer_unavailable: rebind route: %w", err)
				}
				routeRebinds++
				stableCount = 0
				last = browserStableObservation{}
			} else if rebound == "" || rebound == observation.URL {
				changed := beforeDigest == "" || observation.Digest != beforeDigest
				if stableCount >= requiredStable && time.Since(stableSince) >= time.Duration(quietMS)*time.Millisecond && (changed || allowNoChange) {
					if pageID == "" {
						pageID = firstStringValue(page, "page_id")
					}
					return map[string]any{
						"status": "stable", "reason_code": "browser_target_settled",
						"text": "browser page reached a stable observable state", "page_id": pageID,
						"url": observation.URL, "title": observation.Title, "state_digest": observation.Digest,
						"state_changed": changed, "observations": observations, "quiet_period_ms": quietMS,
						"route_rebinds": routeRebinds, "session_generation": lease.SessionGeneration,
						"provider_session_ref": a.providerSessionRef,
					}, nil
				}
			}
		}
		select {
		case <-settleCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("browser_settle_timeout: required page signals did not remain stable")
		case <-time.After(time.Duration(pollMS) * time.Millisecond):
		}
	}
}

func (a *PlaywrightExtensionAdapter) invalidateSnapshotsLocked() {
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
}

func (a *PlaywrightExtensionAdapter) withSessionMetadataLocked(output map[string]any, metadata browserModeFields, args map[string]any) map[string]any {
	if output == nil {
		output = map[string]any{}
	} else {
		output = cloneArgs(output)
	}
	lease := a.session.Lease()
	ownerID, profileID := splitBrowserProfileKey(playwrightScope(a.cfg, args))
	output["session_generation"] = lease.SessionGeneration
	output["page_generation"] = lease.PageGeneration
	output["provider_session_ref"] = a.providerSessionRef
	output["presentation"] = metadata.Presentation
	output["owner_id"] = ownerID
	output["profile_id"] = profileID
	if pages := extractPages(output); len(pages) > 0 {
		annotated := make([]any, 0, len(pages))
		for _, raw := range pages {
			page := cloneArgs(mapValue(raw))
			page["session_generation"] = lease.SessionGeneration
			page["page_generation"] = lease.PageGeneration
			page["provider_session_ref"] = a.providerSessionRef
			page["presentation"] = metadata.Presentation
			page["owner_id"] = ownerID
			page["profile_id"] = profileID
			annotated = append(annotated, page)
		}
		output["pages"] = annotated
	}
	if snapshot := mapValue(output["snapshot"]); snapshot != nil {
		snapshot = cloneArgs(snapshot)
		snapshot["session_generation"] = lease.SessionGeneration
		snapshot["page_generation"] = lease.PageGeneration
		snapshot["provider_session_ref"] = a.providerSessionRef
		snapshot["presentation"] = metadata.Presentation
		snapshot["owner_id"] = ownerID
		snapshot["profile_id"] = profileID
		output["snapshot"] = snapshot
	}
	return output
}

func playwrightScope(cfg config.Config, args map[string]any) string {
	ownerID := strings.TrimSpace(stringArg(args, "owner_id"))
	if ownerID == "" {
		ownerID = "owner"
	}
	profileID := strings.TrimSpace(stringArg(args, "browser_profile_id"))
	if profileID == "" {
		profileID = strings.TrimSpace(cfg.Tools.BrowserAutomation.Profile)
	}
	if profileID == "" {
		profileID = "default"
	}
	return ownerID + "\x00" + profileID
}

func playwrightTaskID(scope string, sequence uint64) string {
	digest := sha256.Sum256([]byte(scope + "\x00" + strconv.FormatUint(sequence, 10)))
	return "pw-" + hex.EncodeToString(digest[:12])
}

func playwrightProviderSessionRef(sessionID string) string {
	digest := sha256.Sum256([]byte("playwright-extension\x00" + sessionID))
	return "pw-" + hex.EncodeToString(digest[:10])
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

func playwrightSnapshotRefs(snapshot any) map[string]any {
	refs := map[string]any{}
	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			ref := firstStringValue(typed, "ref")
			if browserRefNumber(ref) > 0 {
				role := firstNonEmptyBrowserString(firstStringValue(typed, "role"), "control")
				refs[ref] = map[string]any{
					"role": role, "name": firstStringValue(typed, "name", "accessible_name"),
					"clickable": boolValue(typed["clickable"]) || browserInteractiveRole(strings.ToLower(role)),
				}
			}
			for key, item := range typed {
				if key != "ref" {
					visit(item)
				}
			}
		}
	}
	visit(snapshot)
	return refs
}

func errorsForPlaywrightSnapshot(message string) error {
	return &app.CodedToolError{
		Code: app.ToolErrorSnapshotStale,
		Err:  fmt.Errorf("playwright extension snapshot: %s", message),
	}
}
