package r3browser

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

// AcquisitionAdapter makes the product's incumbent dedicated Controller role
// explicit while preserving its public arguments, results and session release.
// This is not a candidate for client_embedded dispatch or a fallback.
type AcquisitionAdapter struct {
	legacy   browserautomation.Adapter
	host     *BackendHost
	mu       sync.Mutex
	bindings map[string]Binding
	cancel   context.CancelFunc
	done     chan struct{}
	closed   bool
}

func NewAcquisitionAdapter(legacy browserautomation.Adapter) browserautomation.Adapter {
	ctx, cancel := context.WithCancel(context.Background())
	adapter := &AcquisitionAdapter{legacy: legacy, host: NewBackendHost(legacy), bindings: map[string]Binding{}, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(adapter.done)
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				adapter.mu.Lock()
				for key, binding := range adapter.bindings {
					if renewed, err := adapter.host.Renew(ctx, binding); err == nil {
						adapter.bindings[key] = renewed
					}
				}
				adapter.mu.Unlock()
			}
		}
	}()
	return adapter
}
func (a *AcquisitionAdapter) Capabilities() Capabilities { return a.host.Capabilities() }
func acquisitionScope(args map[string]any) Scope {
	owner, _ := args["owner_id"].(string)
	if owner == "" {
		owner = "owner"
	}
	profile, _ := args["browser_profile_id"].(string)
	if profile == "" {
		profile = "default"
	}
	token := "profile_" + digest(profile)[:32]
	return Scope{Identity: Identity{OwnerID: owner, ClientID: "backend_service", InstallationID: "backend_acquisition"}, ConversationID: token, TaskID: token}
}
func (a *AcquisitionAdapter) bound(ctx context.Context, args map[string]any) (Binding, error) {
	scope := acquisitionScope(args)
	if !scope.valid() {
		return Binding{}, ErrFence
	}
	key := scope.Identity.key() + "\x00" + scope.ConversationID
	if binding, ok := a.bindings[key]; ok {
		renewed, err := a.host.Renew(ctx, binding)
		if err != nil {
			return Binding{}, err
		}
		a.bindings[key] = renewed
		return renewed, nil
	}
	binding, err := a.host.Acquire(ctx, scope)
	if err != nil {
		return Binding{}, err
	}
	a.bindings[key] = binding
	return binding, nil
}
func (a *AcquisitionAdapter) Health(ctx context.Context, args map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return browserautomation.Result{}, ErrFence
	}
	return a.legacy.Health(ctx, args)
}
func (a *AcquisitionAdapter) Call(ctx context.Context, tool string, args map[string]any) (browserautomation.Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return browserautomation.Result{}, ErrFence
	}
	binding, err := a.bound(ctx, args)
	if err != nil {
		return browserautomation.Result{}, err
	}
	operation := map[string]string{"browser.open": "navigate", "browser.navigate": "navigate", "browser.snapshot": "snapshot", "browser.screenshot": "screenshot", "browser.click": "click", "browser.type": "fill", "browser.select": "select"}[tool]
	if operation == "" {
		// Existing focus/list/wait/close retain the Controller's validated protocol
		// and scope after the common acquisition lease gate.
		switch tool {
		case "browser.focus", "browser.list_tabs", "browser.wait", "browser.close":
			return a.legacy.Call(ctx, tool, args)
		default:
			return browserautomation.Result{}, ErrFence
		}
	}
	input := map[string]any{}
	switch operation {
	case "navigate":
		input["url"] = args["url"]
	case "click", "fill", "select":
		input["ref"] = args["uid"]
		if input["ref"] == nil {
			input["ref"] = args["ref"]
		}
		input["snapshot_id"] = args["snapshot_id"]
		if operation == "fill" {
			input["value"] = args["text"]
		}
		if operation == "select" {
			input["value"] = args["value"]
		}
	}
	// The backend Controller keeps its existing loopback/public HTTP admissions.
	// Remote Host operation validation stays HTTPS-only. No remote substitution.
	if operation == "navigate" {
		value, _ := input["url"].(string)
		if len(value) >= 7 && (value[:7] == "http://" || value == "about:blank") {
			return a.legacy.Call(ctx, tool, args)
		}
	}
	var result browserautomation.Result
	call := &incumbentCall{tool: tool, args: args, result: &result}
	_, err = a.host.Dispatch(context.WithValue(ctx, incumbentCallKey{}, call), binding, opaque("backend_cmd_"), operation, input)
	if err != nil {
		return result, err
	}
	return result, nil
}
func (a *AcquisitionAdapter) ReadPage(ctx context.Context, url string, args map[string]any) (browserautomation.PageReadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return browserautomation.PageReadResult{}, ErrFence
	}
	if _, err := a.bound(ctx, args); err != nil {
		return browserautomation.PageReadResult{}, err
	}
	return a.legacy.ReadPage(ctx, url, args)
}
func (a *AcquisitionAdapter) ReleaseSession(args map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	scope := acquisitionScope(args)
	key := scope.Identity.key() + "\x00" + scope.ConversationID
	if binding, ok := a.bindings[key]; ok {
		a.host.mu.Lock()
		delete(a.host.bindings, binding.LeaseID)
		a.host.mu.Unlock()
		delete(a.bindings, key)
	}
	releaser, ok := a.legacy.(browserautomation.SessionReleaser)
	if !ok {
		return fmt.Errorf("backend acquisition adapter cannot release sessions")
	}
	return releaser.ReleaseSession(args)
}
func (a *AcquisitionAdapter) Close() error {
	a.cancel()
	<-a.done
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.bindings = map[string]Binding{}
	return a.legacy.Close()
}

var _ browserautomation.Adapter = (*AcquisitionAdapter)(nil)
