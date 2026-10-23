package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

type Role string

const ClientEmbedded Role = "client_embedded"
const BackendAcquisition Role = "backend_acquisition"

type Capabilities struct {
	Role             Role     `json:"role"`
	Operations       []string `json:"operations"`
	LeaseSeconds     int      `json:"lease_seconds"`
	HeartbeatSeconds int      `json:"heartbeat_seconds"`
}

// BrowserHostAdapter is the common role-explicit resource/command boundary.
// The local implementation retains Controller ownership and public contracts;
// the remote implementation only addresses granted embedded client resources.
type BrowserHostAdapter interface {
	Capabilities() Capabilities
	Acquire(context.Context, Scope) (Binding, error)
	Renew(context.Context, Binding) (Binding, error)
	Dispatch(context.Context, Binding, string, string, map[string]any) (json.RawMessage, error)
	Release(context.Context, Binding) error
}

func (b *Broker) Renew(ctx context.Context, binding Binding) (Binding, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	host := b.hosts[binding.Scope.Identity.key()]
	if host == nil || host.epoch != binding.ConnectionEpoch || host.runtime != binding.RuntimeGeneration || host.hostID != binding.HostID || host.grantDigest != binding.AuthorizationDigest {
		return Binding{}, ErrFence
	}
	live, ok := host.leases[binding.LeaseID]
	if !ok || live.Scope != binding.Scope || live.PageID != binding.PageID || live.PageGeneration != binding.PageGeneration || !leaseLive(host, live, b.now()) || host.reverse == nil && b.now().Sub(host.lastHeartbeat) >= LeaseDuration {
		return Binding{}, ErrFence
	}
	// Only a real LAN host heartbeat extends remote resource authority. An ISCP
	// lease lasts as long as its task and session.
	return live, nil
}

func (b *Broker) Capabilities() Capabilities {
	return Capabilities{Role: ClientEmbedded, Operations: []string{"acquire", "navigate", "read", "snapshot", "click", "fill", "select", "screenshot", "wait", "release"}, LeaseSeconds: 30, HeartbeatSeconds: 10}
}

// BackendHost is an explicit backend role. It never becomes a fallback for a
// missing remote host. Its validated operations use the incumbent Adapter.
type BackendHost struct {
	Adapter  browserautomation.Adapter
	mu       sync.Mutex
	bindings map[string]Binding
	pageURLs map[string]string
	pageIDs  map[string]string
}

func NewBackendHost(adapter browserautomation.Adapter) *BackendHost {
	return &BackendHost{Adapter: adapter, bindings: map[string]Binding{}, pageURLs: map[string]string{}, pageIDs: map[string]string{}}
}
func (*BackendHost) Capabilities() Capabilities {
	return Capabilities{Role: BackendAcquisition, Operations: []string{"acquire", "navigate", "read", "snapshot", "click", "fill", "select", "screenshot", "wait", "release"}, LeaseSeconds: 30, HeartbeatSeconds: 10}
}
func (h *BackendHost) Acquire(ctx context.Context, scope Scope) (Binding, error) {
	if !scope.valid() || h.Adapter == nil {
		return Binding{}, ErrFence
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, binding := range h.bindings {
		if binding.Scope == scope && time.Now().Before(binding.LeaseExpiresAt) {
			return binding, nil
		}
		if binding.Scope != scope {
			return Binding{}, ErrFence
		}
	}
	binding := Binding{Scope: scope, HostID: "backend_acquisition", RuntimeGeneration: opaque("runtime_"), ConnectionEpoch: opaque("epoch_"), LeaseID: opaque("lease_"), PageID: opaque("page_"), PageGeneration: 1, AuthorizationDigest: digest(scope), LeaseExpiresAt: time.Now().Add(LeaseDuration)}
	h.bindings[binding.LeaseID] = binding
	return binding, nil
}
func (h *BackendHost) Renew(ctx context.Context, binding Binding) (Binding, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	live, ok := h.bindings[binding.LeaseID]
	if !ok || live.Scope != binding.Scope || live.RuntimeGeneration != binding.RuntimeGeneration || live.PageID != binding.PageID || live.PageGeneration != binding.PageGeneration || !time.Now().Before(live.LeaseExpiresAt) {
		return Binding{}, ErrFence
	}
	live.LeaseExpiresAt = time.Now().Add(LeaseDuration)
	h.bindings[binding.LeaseID] = live
	return live, nil
}

// This private bridge preserves the incumbent Controller Adapter's exact
// arguments/result schema after common host ownership/operation validation.
// It is never supplied by a renderer or by model arguments.
type incumbentCallKey struct{}
type incumbentCall struct {
	tool   string
	args   map[string]any
	result *browserautomation.Result
}

func (h *BackendHost) Dispatch(ctx context.Context, binding Binding, id, operation string, args map[string]any) (json.RawMessage, error) {
	if !idPattern.MatchString(id) || validateOperation(operation, args) != nil {
		return nil, ErrFence
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	active, ok := h.bindings[binding.LeaseID]
	if !ok || active.Scope != binding.Scope || active.RuntimeGeneration != binding.RuntimeGeneration || active.ConnectionEpoch != binding.ConnectionEpoch || active.PageID != binding.PageID || active.PageGeneration != binding.PageGeneration || !time.Now().Before(active.LeaseExpiresAt) {
		return nil, ErrFence
	}
	if operation == "acquire" {
		return json.Marshal(map[string]any{"page_id": binding.PageID})
	}
	if operation == "release" {
		if releaser, ok := h.Adapter.(browserautomation.SessionReleaser); ok {
			if err := releaser.ReleaseSession(map[string]any{"task_id": binding.Scope.TaskID}); err != nil {
				return nil, err
			}
		}
		delete(h.bindings, binding.LeaseID)
		delete(h.pageURLs, binding.LeaseID)
		delete(h.pageIDs, binding.LeaseID)
		return json.Marshal(map[string]any{"released": true})
	}
	tool := map[string]string{"navigate": "browser.open", "read": "browser.read", "snapshot": "browser.snapshot", "click": "browser.click", "fill": "browser.type", "select": "browser.select", "screenshot": "browser.screenshot", "wait": "browser.wait"}[operation]
	callArgs := map[string]any{"task_id": binding.Scope.TaskID, "owner_id": binding.Scope.OwnerID, "session_id": binding.Scope.TaskID}
	for k, v := range args {
		if k == "ref" {
			k = "uid"
		}
		if k == "value" && operation == "fill" {
			k = "text"
		}
		callArgs[k] = v
	}
	if pageID := h.pageIDs[binding.LeaseID]; pageID != "" {
		callArgs["page_id"] = pageID
	}
	call, preserve := ctx.Value(incumbentCallKey{}).(*incumbentCall)
	if preserve {
		tool = call.tool
		callArgs = call.args
	}
	if operation == "read" && !preserve {
		targetURL := h.pageURLs[binding.LeaseID]
		if targetURL == "" {
			return nil, ErrFence
		}
		callArgs["reuse_active_page"] = true
		read, err := h.Adapter.ReadPage(ctx, targetURL, callArgs)
		if err != nil {
			return nil, err
		}
		return json.Marshal(read)
	}
	result, err := h.Adapter.Call(ctx, tool, callArgs)
	if preserve && err == nil {
		*call.result = result
	}
	if err != nil {
		return nil, err
	}
	if operation == "navigate" && !preserve {
		h.pageURLs[binding.LeaseID], _ = args["url"].(string)
		h.pageIDs[binding.LeaseID] = backendSelectedPageID(result.Output)
		if h.pageIDs[binding.LeaseID] == "" {
			return nil, errors.New("backend acquisition did not bind an actual managed page")
		}
	}
	if preserve {
		return json.Marshal(result.Output)
	}
	raw, err := json.Marshal(result.Output)
	if err != nil {
		return nil, err
	}
	var projected any
	if err := json.Unmarshal(raw, &projected); err != nil {
		return nil, err
	}
	projectBackendPageID(projected, binding.PageID)
	return json.Marshal(projected)
}
func backendSelectedPageID(output any) string {
	values, ok := output.(map[string]any)
	if !ok {
		return ""
	}
	if id, ok := values["page_id"].(string); ok && id != "" {
		return id
	}
	if pages, ok := values["pages"].([]any); ok {
		for _, item := range pages {
			if page, ok := item.(map[string]any); ok {
				if id, ok := page["page_id"].(string); ok && id != "" {
					return id
				}
			}
		}
	}
	return ""
}
func projectBackendPageID(value any, pageID string) {
	switch values := value.(type) {
	case map[string]any:
		for key, nested := range values {
			if key == "page_id" {
				values[key] = pageID
			} else {
				projectBackendPageID(nested, pageID)
			}
		}
	case []any:
		for _, nested := range values {
			projectBackendPageID(nested, pageID)
		}
	}
}
func (h *BackendHost) Release(ctx context.Context, binding Binding) error {
	_, err := h.Dispatch(ctx, binding, opaque("cmd_"), "release", map[string]any{})
	return err
}

// ScopedAdapter is inserted into the isolated workbench ToolHub. It never trusts model
// arguments for identity, role, task, host or page ownership. The page is
// acquired on the first tool call and released with the task.
type ScopedAdapter struct {
	mu        sync.Mutex
	host      BrowserHostAdapter
	scope     Scope
	binding   *Binding
	activeURL string
	closed    bool
}

// EmbeddedProfileID names the one browser profile of a client-embedded host.
const EmbeddedProfileID = "client_embedded"

const stableRouteRebindLimit = 2

func (b *Broker) ForScope(scope Scope) browserautomation.Adapter { return NewScopedAdapter(b, scope) }
func NewScopedAdapter(host BrowserHostAdapter, scope Scope) *ScopedAdapter {
	return &ScopedAdapter{host: host, scope: scope}
}
func (a *ScopedAdapter) acquire(ctx context.Context) (Binding, error) {
	if a.closed || a.host == nil || !a.scope.valid() {
		return Binding{}, ErrFence
	}
	if a.binding == nil {
		binding, err := a.host.Acquire(ctx, a.scope)
		if err != nil {
			return Binding{}, err
		}
		a.binding = &binding
	}
	return *a.binding, nil
}

// dispatch forgets a binding whose lease the host no longer holds, such as
// after its session ended, so the task's next call acquires a fresh page.
func (a *ScopedAdapter) dispatch(ctx context.Context, binding Binding, operation string, input map[string]any) (json.RawMessage, error) {
	output, err := a.host.Dispatch(ctx, binding, opaque("cmd_"), operation, input)
	if err != nil && !errors.Is(err, ErrUnknown) {
		if leases, ok := a.host.(interface{ LeaseActive(Binding) bool }); ok && !leases.LeaseActive(binding) {
			a.binding = nil
			a.activeURL = ""
		}
	}
	return output, err
}

// Health reads local readiness only. It never acquires a page or sends a
// command; the first tool call does that.
func (a *ScopedAdapter) Health(ctx context.Context, _ map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.host == nil || !a.scope.valid() {
		return browserautomation.Result{}, ErrFence
	}
	if ready, ok := a.host.(interface {
		Ready(context.Context, Identity) error
	}); ok {
		if err := ready.Ready(ctx, a.scope.Identity); err != nil {
			return browserautomation.Result{}, err
		}
	} else if _, err := a.acquire(ctx); err != nil {
		return browserautomation.Result{}, err
	}
	output := map[string]any{"ok": true, "configured": true, "status": "ready", "state": "ready", "role": a.host.Capabilities().Role, "owner_id": a.scope.OwnerID, "profile_id": EmbeddedProfileID}
	var generation uint64
	if a.binding != nil {
		output["host_id"], output["page_id"] = a.binding.HostID, a.binding.PageID
		generation = a.binding.PageGeneration
	}
	return browserautomation.Result{Tool: "browser.status", Output: output, Pages: []any{}, SessionGeneration: generation, Untrusted: true, Provider: "browser_host"}, nil
}
func (a *ScopedAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.callLocked(ctx, tool, args)
}
func (a *ScopedAdapter) callLocked(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	presentation, _ := args["presentation"].(string)
	if presentation != "hidden" && presentation != "visible" {
		presentation = "visible"
	}
	mode, _ := args["browser_mode"].(string)
	if mode != "autonomous" && mode != "collaborative" {
		mode = "autonomous"
	}
	if tool == "browser.list_tabs" {
		// No page exists for this task until it navigates; an empty about:blank
		// tab would only make the workflow reuse a page it never opened.
		pages := []any{}
		if a.binding != nil && a.activeURL != "" {
			pages = append(pages, a.pageEntry(*a.binding, a.activeURL, presentation))
		}
		return browserautomation.Result{Tool: tool, Output: map[string]any{"pages": pages}, Pages: pages, Untrusted: true, Provider: "browser_host", BrowserMode: mode, Presentation: presentation}, nil
	}
	binding, err := a.acquire(ctx)
	if err != nil {
		return browserautomation.Result{}, err
	}
	if requested, ok := args["page_id"].(string); ok && requested != "" && requested != binding.PageID {
		return browserautomation.Result{}, ErrFence
	}
	if tool == "browser.focus" {
		// The task page is the only page this lease addresses. Focusing it never
		// changes which local conversation the owner is looking at.
		if a.activeURL == "" {
			return browserautomation.Result{}, ErrFence
		}
		page := a.pageEntry(binding, a.activeURL, presentation)
		output := map[string]any{"page_id": binding.PageID, "url": a.activeURL, "focused": true, "pages": []any{page}}
		a.identify(output, binding, presentation)
		return browserautomation.Result{Tool: tool, Output: output, Pages: []any{page}, SessionGeneration: binding.PageGeneration, Untrusted: true, Provider: "browser_host", BrowserMode: mode, Presentation: presentation}, nil
	}
	operation := map[string]string{"browser.open": "navigate", "browser.navigate": "navigate", "browser.read": "read", "browser.snapshot": "snapshot", "browser.click": "click", "browser.type": "fill", "browser.select": "select", "browser.screenshot": "screenshot", "browser.wait": "wait", "browser.close": "release"}[tool]
	if operation == "" {
		return browserautomation.Result{}, ErrFence
	}
	if operation == "wait" && args["mode"] != nil {
		decoded, err := a.stableWait(ctx, binding, args, presentation)
		if err != nil {
			return browserautomation.Result{}, err
		}
		text, _ := decoded["text"].(string)
		return browserautomation.Result{Tool: tool, RawTool: "stable_state", Output: decoded, Text: text, Pages: []any{}, SessionGeneration: binding.PageGeneration, Untrusted: true, Provider: "browser_host", BrowserMode: mode, Presentation: presentation, SurfaceVisible: surfaceVisible(decoded)}, nil
	}
	input := map[string]any{}
	switch operation {
	case "navigate":
		input["url"] = args["url"]
	case "snapshot":
		if url, ok := args["url"].(string); ok && url != "" && url != a.activeURL {
			if _, err := a.dispatch(ctx, binding, "navigate", map[string]any{"url": url}); err != nil {
				return browserautomation.Result{}, err
			}
			a.activeURL = url
		}
	case "read":
		if value, ok := args["max_chars"]; ok {
			n, ok := integerArg(value)
			if !ok {
				return browserautomation.Result{}, ErrFence
			}
			input["max_chars"] = min(n, MaxReadChars)
		}
	case "click", "fill", "select":
		ref := args["uid"]
		if ref == nil {
			ref = args["ref"]
		}
		input["ref"] = ref
		input["snapshot_id"] = args["snapshot_id"]
		if operation == "fill" {
			input["value"] = args["text"]
		}
		if operation == "select" {
			input["value"] = args["value"]
		}
	case "wait":
		input["milliseconds"] = 0
		if v, ok := args["milliseconds"]; ok {
			input["milliseconds"] = v
		}
	}
	output, err := a.dispatch(ctx, binding, operation, input)
	if err != nil {
		return browserautomation.Result{}, err
	}
	if operation == "navigate" {
		a.activeURL, _ = input["url"].(string)
	}
	if operation == "release" {
		a.binding = nil
		a.activeURL = ""
	}
	var decoded map[string]any
	if err := json.Unmarshal(output, &decoded); err != nil {
		return browserautomation.Result{}, fmt.Errorf("invalid host result: %w", err)
	}
	if operation == "navigate" {
		if url, ok := decoded["url"].(string); ok && url != "" {
			a.activeURL = url
		}
	}
	if operation != "release" {
		a.identify(decoded, binding, presentation)
		if snapshot, ok := decoded["snapshot"].(map[string]any); ok {
			a.identify(snapshot, binding, presentation)
		}
	}
	text, _ := decoded["text"].(string)
	if operation == "wait" && text == "" {
		text = string(output)
	}
	result := browserautomation.Result{Tool: tool, RawTool: operation, Output: decoded, Text: text, Pages: []any{}, SessionGeneration: binding.PageGeneration, Untrusted: true, Provider: "browser_host", BrowserMode: mode, Presentation: presentation, SurfaceVisible: surfaceVisible(decoded)}
	if operation == "navigate" {
		// Workflow consumes only the selected page in this task's scoped browser
		// session. This does not select or present another local conversation.
		result.Pages = []any{a.pageEntry(binding, a.activeURL, presentation)}
	}
	return result, nil
}

// stableWait lets the host decide stability inside the page, then applies the
// frozen-target route rule here, rebinding a same-origin route at most twice.
func (a *ScopedAdapter) stableWait(ctx context.Context, binding Binding, args map[string]any, presentation string) (map[string]any, error) {
	if requested, ok := integerArg(args["session_generation"]); ok && requested != 0 && uint64(requested) != binding.PageGeneration {
		return nil, errors.New("browser_session_stale: requested session generation is no longer active")
	}
	if mode, _ := args["mode"].(string); mode != "stable_state" {
		return nil, ErrFence
	}
	input := map[string]any{"mode": "stable_state"}
	if digest, ok := args["before_digest"].(string); ok && digest != "" {
		input["before_digest"] = digest
	}
	for _, key := range []string{"allow_no_change", "require_ready_state"} {
		if value, ok := args[key].(bool); ok {
			input[key] = value
		}
	}
	if timeout, ok := integerArg(args["timeout_ms"]); ok {
		input["timeout_ms"] = max(500, min(timeout, int(StableWaitLimit/time.Millisecond)))
	}
	expected, _ := args["expected_url"].(string)
	kind, _ := args["target_kind"].(string)
	for rebinds := 0; ; rebinds++ {
		output, err := a.dispatch(ctx, binding, "wait", input)
		if err != nil {
			return nil, err
		}
		var decoded map[string]any
		if err := json.Unmarshal(output, &decoded); err != nil {
			return nil, fmt.Errorf("invalid host result: %w", err)
		}
		switch decoded["status"] {
		case "stable":
		case "timeout":
			return nil, errors.New("browser_settle_timeout: required page signals did not remain stable")
		default:
			return nil, errors.New("browser_settle_timeout: host did not report a stable page")
		}
		current, _ := decoded["url"].(string)
		if current != "" {
			a.activeURL = current
		}
		if expected != "" {
			rebound, err := browserautomation.SettledBrowserRoute(expected, current, app.BrowserTargetKind(kind))
			if err != nil {
				return nil, err
			}
			if rebound != "" && rebound != current {
				if rebinds >= stableRouteRebindLimit {
					return nil, errors.New("browser_route_diverged: same-origin route rebind limit exceeded")
				}
				if _, err := a.dispatch(ctx, binding, "navigate", map[string]any{"url": rebound}); err != nil {
					return nil, fmt.Errorf("browser_renderer_unavailable: rebind route: %w", err)
				}
				a.activeURL = rebound
				continue
			}
		}
		decoded["reason_code"] = "browser_target_settled"
		decoded["text"] = "browser page reached a stable observable state"
		decoded["route_rebinds"] = rebinds
		a.identify(decoded, binding, presentation)
		return decoded, nil
	}
}

// identify stamps the session identity the workflow compares across hidden
// and visible stages. Presentation is the requested lane, exactly as the
// Controller reports it; actual visibility is surface_visible from the host.
func (a *ScopedAdapter) identify(output map[string]any, binding Binding, presentation string) {
	output["session_generation"] = binding.PageGeneration
	output["page_generation"] = binding.PageGeneration
	output["provider_session_ref"] = "browser_host:" + binding.LeaseID
	output["presentation"] = presentation
	output["owner_id"] = a.scope.OwnerID
	output["profile_id"] = EmbeddedProfileID
}
func (a *ScopedAdapter) pageEntry(binding Binding, url, presentation string) map[string]any {
	page := map[string]any{"page_id": binding.PageID, "url": url, "selected": true}
	a.identify(page, binding, presentation)
	return page
}
func surfaceVisible(output map[string]any) bool {
	visible, _ := output["surface_visible"].(bool)
	return visible
}
func (a *ScopedAdapter) ReadPage(ctx context.Context, targetURL string, args map[string]any) (browserautomation.PageReadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	reuse, _ := args["reuse_active_page"].(bool)
	if reuse {
		// Reuse is a bound read, not permission to navigate or reacquire after
		// losing a lease. Dispatch still verifies that this binding is live.
		if a.closed || a.binding == nil || !sameReadURL(a.activeURL, targetURL) {
			return browserautomation.PageReadResult{}, ErrFence
		}
	} else {
		openArgs := make(map[string]any, len(args)+1)
		for key, value := range args {
			openArgs[key] = value
		}
		openArgs["url"] = targetURL
		if _, err := a.callLocked(ctx, "browser.open", openArgs); err != nil {
			return browserautomation.PageReadResult{}, err
		}
	}
	result, err := a.callLocked(ctx, "browser.read", args)
	if err != nil {
		return browserautomation.PageReadResult{}, err
	}
	raw, err := json.Marshal(result.Output)
	if err != nil {
		return browserautomation.PageReadResult{}, err
	}
	var read browserautomation.PageReadResult
	if err = json.Unmarshal(raw, &read); err != nil {
		return read, err
	}
	if read.FinalURL != "" {
		a.activeURL = read.FinalURL
	}
	if reuse && (!sameReadURL(read.URL, targetURL) || !sameReadURL(read.FinalURL, targetURL)) {
		// The page may have navigated itself since open; do not return content
		// from a different document or silently reload the requested one.
		return browserautomation.PageReadResult{}, ErrFence
	}
	read.Provider = "browser_host"
	read.Rendered = true
	read.Untrusted = true
	read.ReadSource = "client_embedded_webcontents"
	read.SessionGeneration = result.SessionGeneration
	if read.Actions == nil {
		read.Actions = []string{}
	}
	return read, nil
}

func sameReadURL(left, right string) bool {
	normalize := func(raw string) string {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
			return ""
		}
		parsed.Host = strings.ToLower(parsed.Host)
		if parsed.Path == "" {
			parsed.Path = "/"
		}
		return parsed.String()
	}
	return normalize(left) != "" && normalize(left) == normalize(right)
}
func (a *ScopedAdapter) ReleaseSession(_ map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.binding == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := a.host.Release(ctx, *a.binding)
	a.binding = nil
	a.activeURL = ""
	return err
}
func (a *ScopedAdapter) Close() error {
	err := a.ReleaseSession(nil)
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return err
}

var _ BrowserHostAdapter = (*Broker)(nil)
var _ BrowserHostAdapter = (*BackendHost)(nil)
var _ browserautomation.Adapter = (*ScopedAdapter)(nil)
