package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

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
	mu      sync.Mutex
	host    *ClientControllerHost
	scope   Scope
	binding *Binding
	closed  bool
}

// EmbeddedProfileID names the one browser profile of a client-embedded host.
const EmbeddedProfileID = "client_embedded"

func (b *Broker) ForScope(scope Scope) browserautomation.Adapter {
	b.mu.Lock()
	host := b.controller
	b.mu.Unlock()
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

// Health probes readiness without acquiring a page or starting the Controller.
func (a *ScopedAdapter) Health(ctx context.Context, _ map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed || a.host == nil || !a.scope.valid() {
		return browserautomation.Result{}, ErrFence
	}
	if err := a.host.Ready(ctx, a.scope.Identity); err != nil {
		return browserautomation.Result{}, err
	}
	output := map[string]any{"ok": true, "configured": true, "status": "ready", "state": "ready", "role": ClientEmbedded, "owner_id": a.scope.OwnerID, "profile_id": EmbeddedProfileID}
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
	if a.closed || a.host == nil || !a.scope.valid() {
		return browserautomation.Result{}, ErrFence
	}
	if tool == "browser.list_tabs" && a.binding == nil {
		return browserautomation.Result{Tool: tool, Output: map[string]any{"pages": []any{}}, Pages: []any{}, Untrusted: true, Provider: "playwright-extension"}, nil
	}
	binding, err := a.acquire(ctx)
	if err != nil {
		return browserautomation.Result{}, err
	}
	result, err := a.host.CallTool(ctx, binding, tool, args)
	if tool == "browser.close" && err == nil {
		// Explicit close destroys the requested page before relinquishing the
		// task's remaining authority. Normal task release may retain its view.
		releaseErr := a.host.Release(ctx, binding)
		a.binding = nil
		return result, releaseErr
	}
	if err != nil {
		a.forgetInactiveControllerBinding(binding, err)
	}
	return result, err
}

func (a *ScopedAdapter) forgetInactiveControllerBinding(binding Binding, err error) {
	if errors.Is(err, ErrUnknown) {
		return
	}
	if !a.host.LeaseActive(binding) {
		a.binding = nil
	}
}

func (a *ScopedAdapter) ReadPage(ctx context.Context, targetURL string, args map[string]any) (browserautomation.PageReadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	reuse, _ := args["reuse_active_page"].(bool)
	if reuse && (a.closed || a.binding == nil) {
		return browserautomation.PageReadResult{}, ErrFence
	}
	binding, err := a.acquire(ctx)
	if err != nil {
		return browserautomation.PageReadResult{}, err
	}
	result, err := a.host.ReadTool(ctx, binding, targetURL, args)
	if err != nil {
		a.forgetInactiveControllerBinding(binding, err)
	}
	return result, err
}

func (a *ScopedAdapter) ReleaseSession(_ map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.binding == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.host.Release(ctx, *a.binding)
	a.binding = nil
	return err
}
func (a *ScopedAdapter) Close() error {
	err := a.ReleaseSession(nil)
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	return err
}

var _ browserautomation.Adapter = (*ScopedAdapter)(nil)
