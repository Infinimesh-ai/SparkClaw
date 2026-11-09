package browserhost

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

// The old reverse Command is retained only until P3. Production Controller
// calls require this typed operation boundary; there is no LAN fallback.
type ControllerReverseSession interface {
	ReverseSession
	browsercontrol.ISCPControllerTransport
}

type controllerHostLease struct {
	binding  Binding
	peer     ControllerReverseSession
	adapter  *browserautomation.PlaywrightExtensionAdapter
	done     chan struct{}
	retiring bool
}
type ClientControllerHost struct {
	broker *Broker
	cfg    config.Config
	mu     sync.Mutex
	leases map[string]*controllerHostLease
}

func (b *Broker) EnableClientController(cfg config.Config) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.controller == nil {
		b.controller = &ClientControllerHost{broker: b, cfg: cfg, leases: map[string]*controllerHostLease{}}
	}
}
func (*ClientControllerHost) Capabilities() Capabilities {
	return Capabilities{Role: ClientEmbedded, Operations: []string{"acquire", "execute", "release", "status", "chunk"}}
}
func (h *ClientControllerHost) peer(identity Identity) (ControllerReverseSession, error) {
	if !identity.valid() {
		return nil, ErrFence
	}
	h.broker.mu.Lock()
	router := h.broker.reverse
	h.broker.mu.Unlock()
	if router == nil {
		return nil, ErrUnavailable
	}
	peer, err := router.ReverseSession(identity)
	if err != nil || peer == nil || sessionDone(peer) || !idPattern.MatchString(peer.SessionID()) || peer.AuthorizationRevision() == 0 || peer.AuthorizationRevision() > 1<<53-1 {
		return nil, ErrUnavailable
	}
	typed, ok := peer.(ControllerReverseSession)
	if !ok {
		return nil, ErrUnavailable
	}
	return typed, nil
}
func controllerBinding(scope Scope, peer ControllerReverseSession) Binding {
	return Binding{Scope: scope, HostID: reverseHostID(scope.Identity, peer.SessionID()), RuntimeGeneration: "iscp_" + peer.SessionID(),
		ConnectionEpoch: peer.SessionID(), LeaseID: opaque("lease_"), PageID: opaque("page_"), PageGeneration: 1,
		AuthorizationDigest: reverseAuthorizationDigest(peer.SessionID(), peer.AuthorizationRevision())}
}
func (h *ClientControllerHost) client(binding Binding, peer ControllerReverseSession) (*browsercontrol.ISCPControllerClient, error) {
	raw, err := json.Marshal(binding)
	if err != nil {
		return nil, err
	}
	return browsercontrol.NewISCPControllerClient(peer, raw, peer.AuthorizationRevision(), browsercontrol.ControllerFenceHooks{
		Before: func(ctx context.Context, intent browsercontrol.ControllerIntent) error {
			return h.before(ctx, binding, intent)
		},
		After: func(ctx context.Context, intent browsercontrol.ControllerIntent, outcome string) error {
			return h.after(ctx, binding, intent, outcome)
		},
	})
}
func (h *ClientControllerHost) Ready(ctx context.Context, identity Identity) error {
	peer, err := h.peer(identity)
	if err != nil {
		return err
	}
	client, err := h.client(controllerBinding(Scope{Identity: identity, ConversationID: "status", TaskID: "status"}, peer), peer)
	if err != nil {
		return err
	}
	status, err := client.ControllerStatus(ctx)
	if err != nil || !status.Available {
		return ErrUnavailable
	}
	return nil
}
func (h *ClientControllerHost) Acquire(ctx context.Context, scope Scope) (Binding, error) {
	if !scope.valid() {
		return Binding{}, ErrFence
	}
	if err := ctx.Err(); err != nil {
		return Binding{}, err
	}
	peer, err := h.peer(scope.Identity)
	if err != nil {
		return Binding{}, err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, entry := range h.leases {
		if entry.binding.Scope == scope && !entry.retiring && !sessionDone(entry.peer) {
			return entry.binding, nil
		}
	}
	if len(h.leases) >= 1024 {
		return Binding{}, ErrUnavailable
	}
	binding := controllerBinding(scope, peer)
	client, err := h.client(binding, peer)
	if err != nil {
		return Binding{}, err
	}
	entry := &controllerHostLease{binding: binding, peer: peer, adapter: browserautomation.NewClientPlaywrightExtensionAdapter(h.cfg, client), done: make(chan struct{})}
	h.leases[binding.LeaseID] = entry
	go func() {
		select {
		case <-peer.Done():
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.Release(ctx, binding)
		case <-entry.done:
		}
	}()
	return binding, nil
}
func (h *ClientControllerHost) require(binding Binding, release bool) (*controllerHostLease, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entry := h.leases[binding.LeaseID]
	if entry == nil || entry.binding != binding || entry.retiring && !release || sessionDone(entry.peer) {
		return nil, ErrFence
	}
	return entry, nil
}
func (h *ClientControllerHost) LeaseActive(binding Binding) bool {
	_, err := h.require(binding, false)
	return err == nil
}
func (h *ClientControllerHost) Renew(_ context.Context, binding Binding) (Binding, error) {
	_, err := h.require(binding, false)
	return binding, err
}
func (*ClientControllerHost) Dispatch(context.Context, Binding, string, string, map[string]any) (json.RawMessage, error) {
	return nil, ErrFence
}

func trustedControllerArgs(binding Binding, args map[string]any) map[string]any {
	out := make(map[string]any, len(args)+5)
	for key, value := range args {
		out[key] = value
	}
	out["owner_id"] = binding.Scope.OwnerID
	out["task_id"] = binding.Scope.TaskID
	out["session_id"] = binding.Scope.TaskID
	out["browser_profile_id"] = EmbeddedProfileID
	for _, key := range []string{"client_id", "installation_id", "local_conversation_id", "local_task_id", "host_id", "lease_id", "runtime_generation", "connection_epoch", "authorization_digest", "browser_role"} {
		delete(out, key)
	}
	return out
}
func (h *ClientControllerHost) CallTool(ctx context.Context, binding Binding, tool string, args map[string]any) (browserautomation.Result, error) {
	entry, err := h.require(binding, false)
	if err != nil {
		return browserautomation.Result{}, err
	}
	result, err := entry.adapter.Call(ctx, tool, trustedControllerArgs(binding, args))
	if browsercontrol.ErrorCode(err) == browsercontrol.CodeWriteUnknown {
		return result, errors.Join(ErrUnknown, err)
	}
	return result, err
}
func (h *ClientControllerHost) ReadTool(ctx context.Context, binding Binding, url string, args map[string]any) (browserautomation.PageReadResult, error) {
	entry, err := h.require(binding, false)
	if err != nil {
		return browserautomation.PageReadResult{}, err
	}
	result, err := entry.adapter.ReadPage(ctx, url, trustedControllerArgs(binding, args))
	result.ReadSource = "client_embedded_webcontents"
	result.Untrusted = true
	return result, err
}
func (h *ClientControllerHost) Release(ctx context.Context, binding Binding) error {
	h.mu.Lock()
	entry := h.leases[binding.LeaseID]
	if entry == nil {
		h.mu.Unlock()
		return nil
	}
	if entry.binding != binding || entry.retiring {
		h.mu.Unlock()
		return ErrFence
	}
	entry.retiring = true
	h.mu.Unlock()
	err := entry.adapter.CloseContext(ctx)
	h.mu.Lock()
	delete(h.leases, binding.LeaseID)
	close(entry.done)
	h.mu.Unlock()
	return err
}

func (h *ClientControllerHost) closeClient(clientID string) {
	h.mu.Lock()
	var bindings []Binding
	for _, entry := range h.leases {
		if clientID == "" || entry.binding.Scope.ClientID == clientID {
			bindings = append(bindings, entry.binding)
		}
	}
	h.mu.Unlock()
	var pending sync.WaitGroup
	for _, binding := range bindings {
		pending.Add(1)
		go func() {
			defer pending.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = h.Release(ctx, binding)
		}()
	}
	pending.Wait()
}

func (h *ClientControllerHost) before(ctx context.Context, binding Binding, intent browsercontrol.ControllerIntent) error {
	if ctx.Err() != nil {
		return browsercontrol.ErrControllerNotSent
	}
	if _, err := h.require(binding, intent.Operation == "browser.controller.release"); err != nil {
		return err
	}
	var echoed Binding
	if json.Unmarshal(intent.Binding, &echoed) != nil || echoed != binding || !idPattern.MatchString(intent.CommandID) || !hexDigest.MatchString(intent.Digest) {
		return ErrFence
	}
	b := h.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.fences[intent.CommandID]; exists || len(b.fences) >= MaxFences {
		return ErrFence
	}
	if intent.Operation != "browser.controller.release" {
		for _, prior := range b.fences {
			if prior.Scope.Identity == binding.Scope.Identity && prior.Scope.ConversationID == binding.Scope.ConversationID && prior.Write && (prior.State == "unknown" || prior.State == "dispatched") {
				return ErrUnknown
			}
		}
	}
	fence := Fence{CommandID: intent.CommandID, Scope: binding.Scope, HostID: binding.HostID, RuntimeGeneration: binding.RuntimeGeneration, ConnectionEpoch: binding.ConnectionEpoch, LeaseID: binding.LeaseID, PageID: binding.PageID, PageGeneration: binding.PageGeneration, AuthorizationDigest: binding.AuthorizationDigest, Digest: intent.Digest, Write: true, State: "dispatched", UpdatedAt: b.now()}
	if err := persistFence(b.root, fence); err != nil {
		return err
	}
	b.fences[intent.CommandID] = fence
	return nil
}
func (h *ClientControllerHost) after(_ context.Context, binding Binding, intent browsercontrol.ControllerIntent, outcome string) error {
	b := h.broker
	b.mu.Lock()
	defer b.mu.Unlock()
	fence, ok := b.fences[intent.CommandID]
	if !ok || fence.Digest != intent.Digest || fence.Scope != binding.Scope {
		return ErrFence
	}
	if fence.State != "dispatched" {
		return ErrFence
	}
	switch outcome {
	case "completed":
		fence.State = "completed"
	case "failed", "not_dispatched":
		fence.State = "failed"
	case "unknown":
		fence.State = "unknown"
	default:
		return ErrFence
	}
	fence.UpdatedAt = b.now()
	if err := persistFence(b.root, fence); err != nil {
		// The durable dispatched record remains a restart fence. Reflect its
		// uncertainty now as well so the owner can reconcile once storage recovers.
		fence.State = "unknown"
		b.fences[intent.CommandID] = fence
		return err
	}
	b.fences[intent.CommandID] = fence
	return nil
}

var _ BrowserHostAdapter = (*ClientControllerHost)(nil)
