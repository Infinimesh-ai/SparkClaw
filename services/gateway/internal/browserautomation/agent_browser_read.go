package browserautomation

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type agentBrowserPageState struct {
	URL             string
	Title           string
	ReadableText    string
	VisibleText     string
	OriginalLength  int
	Source          string
	SourceTruncated bool
}

func (a *AgentBrowserAdapter) ReadPage(ctx context.Context, targetURL string, args map[string]any) (PageReadResult, error) {
	started := time.Now()
	metadata := browserModeMetadata(args, "autonomous")
	hidden := shouldUseHiddenBrowserSession(metadata, args)
	profileKey := a.browserProfileKey(args)
	timeoutMS := intArg(args, "timeout_ms", a.cfg.Adapters.BrowserAutomation.TimeoutMS)
	readCtx := ctx
	cancel := func() {}
	if _, ok := ctx.Deadline(); !ok {
		readCtx, cancel = context.WithTimeout(ctx, adapterTimeout(timeoutMS))
	}
	defer cancel()

	a.mu.Lock()
	defer a.mu.Unlock()
	entry, err := a.ensureSessionLocked(readCtx, hidden, profileKey)
	if err != nil {
		return PageReadResult{}, err
	}
	openTool, _, _, err := entry.openURLLocked(readCtx, targetURL, true)
	if err != nil {
		return PageReadResult{}, err
	}
	entry.invalidateSnapshotsLocked()

	readMode := "browser_session"
	if hidden {
		readMode = "hidden_browser_session"
	}
	result := PageReadResult{
		URL: targetURL, Provider: browserProviderName(hidden), Untrusted: true,
		Actions: []string{openTool}, ReadMode: readMode, Errors: map[string]any{},
		BrowserMode: metadata.BrowserMode, Presentation: metadata.Presentation, SurfaceVisible: metadata.SurfaceVisible,
		SessionGeneration: entry.generation, ProviderSessionRef: entry.session.sessionName, Rendered: true,
	}

	result.Actions = append(result.Actions, "agent_browser_wait_for_load")
	if _, waitErr := entry.callAgentToolLocked(readCtx, "agent_browser_wait_for_load", map[string]any{"state": "domcontentloaded"}); waitErr != nil {
		result.Errors["agent_browser_wait_for_load"] = waitErr.Error()
	} else {
		result.ReadyState = "domcontentloaded"
	}

	maxChars := intArg(args, "max_chars", 120000)
	state, stateErrors := entry.readCurrentPageLocked(readCtx, maxChars)
	for key, value := range stateErrors {
		result.Errors[key] = value
	}
	result.Actions = append(result.Actions,
		"agent_browser_read", "agent_browser_get_text", "agent_browser_get_url", "agent_browser_get_title")
	result.FinalURL = firstNonEmptyAgentBrowserString(state.URL, targetURL)
	result.Title = state.Title
	result.Text = state.ReadableText
	if result.Text == "" {
		result.Text = state.VisibleText
	}
	result.TextLength = state.OriginalLength
	result.TextTruncated = state.SourceTruncated
	result.ContentType = "text/plain; source=agent-browser-read"
	result.ReadSource = firstNonEmptyAgentBrowserString(state.Source, "active-tab-rendered-dom")

	auth, authErr := entry.snapshotAuthMetadataLocked(readCtx, state)
	result.Actions = append(result.Actions, "agent_browser_snapshot")
	if authErr != nil {
		result.Errors["agent_browser_snapshot"] = authErr.Error()
		auth = inferAgentBrowserSnapshotAuth(map[string]any{"text": state.VisibleText}, state.Title, state.URL, nil)
	}
	applyPageReadAuth(&result, auth)

	result.DurationMS = time.Since(started).Milliseconds()
	if strings.TrimSpace(result.Text) == "" {
		return PageReadResult{}, fmt.Errorf("agent-browser returned no readable page content: %v", result.Errors)
	}
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	return result, nil
}

func (e *agentBrowserSessionEntry) readCurrentPageLocked(ctx context.Context, maxChars int) (agentBrowserPageState, map[string]any) {
	state := agentBrowserPageState{}
	errorsByTool := map[string]any{}

	readResult, err := e.callAgentToolLocked(ctx, "agent_browser_read", nil)
	if err != nil {
		errorsByTool["agent_browser_read"] = err.Error()
	} else {
		data := mapValue(readResult.Data)
		state.ReadableText = firstStringValue(data, "content", "text", "value", "result")
		if state.ReadableText == "" {
			state.ReadableText = contentText(agentBrowserOutput(readResult))
		}
		state.URL = firstStringValue(data, "finalUrl", "final_url", "url")
		state.Source = firstStringValue(data, "source")
		state.SourceTruncated = boolValue(data["truncated"])
	}

	textResult, err := e.callAgentToolLocked(ctx, "agent_browser_get_text", map[string]any{"selector": "body"})
	if err != nil {
		errorsByTool["agent_browser_get_text"] = err.Error()
	} else {
		state.VisibleText = firstStringValue(mapValue(textResult.Data), "text", "value", "result")
	}
	if url, err := e.currentURLLocked(ctx); err != nil {
		errorsByTool["agent_browser_get_url"] = err.Error()
	} else if url != "" {
		state.URL = url
	}
	if title, err := e.currentTitleLocked(ctx); err != nil {
		errorsByTool["agent_browser_get_title"] = err.Error()
	} else {
		state.Title = title
	}

	state.OriginalLength = len([]rune(state.ReadableText))
	if state.OriginalLength == 0 {
		state.OriginalLength = len([]rune(state.VisibleText))
	}
	var truncated, visibleTruncated bool
	state.ReadableText, truncated = limitAgentBrowserPageText(state.ReadableText, maxChars)
	state.VisibleText, visibleTruncated = limitAgentBrowserPageText(state.VisibleText, maxChars)
	state.SourceTruncated = state.SourceTruncated || truncated || visibleTruncated
	return state, errorsByTool
}

func (e *agentBrowserSessionEntry) snapshotAuthMetadataLocked(ctx context.Context, state agentBrowserPageState) (map[string]any, error) {
	result, err := e.callAgentToolLocked(ctx, "agent_browser_snapshot", agentBrowserSnapshotRawArgs())
	if err != nil {
		return nil, err
	}
	data := mapValue(result.Data)
	refs := mapValue(data["refs"])
	if refs == nil {
		return nil, errorsForSnapshot("agent-browser omitted the structured refs map")
	}
	tree := firstStringValue(data, "snapshot", "tree", "text")
	enrichAgentBrowserRefsFromTree(refs, tree)
	metadata := map[string]any{"text": state.VisibleText, "tree": tree}
	return inferAgentBrowserSnapshotAuth(metadata, state.Title, state.URL, buildAgentBrowserSnapshotRefs(refs, "")), nil
}

func applyPageReadAuth(result *PageReadResult, metadata map[string]any) {
	if result == nil || metadata == nil {
		return
	}
	result.AuthState = firstStringValue(metadata, "browser_page_auth_state", "authState", "auth_state")
	result.AuthConfidence = firstStringValue(metadata, "browser_page_auth_confidence", "authConfidence", "auth_confidence")
	result.AuthSignals = firstStringSliceValue(metadata["browser_page_auth_signals"], metadata["authSignals"], metadata["auth_signals"])
	result.AuthChallengeDetected = boolValue(metadata["auth_challenge_detected"]) || boolValue(metadata["authChallengeDetected"])
}

func limitAgentBrowserPageText(value string, maxChars int) (string, bool) {
	if maxChars <= 0 {
		maxChars = 120000
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value, false
	}
	return string(runes[:maxChars]), true
}

func firstNonEmptyAgentBrowserString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
