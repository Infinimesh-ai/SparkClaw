package browserautomation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

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

func (a *PlaywrightExtensionAdapter) invalidateSnapshotsLocked() {
	a.snapshots = map[string]*browserSnapshotState{}
	a.activeSnapshotPage = ""
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
