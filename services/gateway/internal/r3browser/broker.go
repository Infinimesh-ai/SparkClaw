package r3browser

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type hostGrant struct {
	Identity Identity
	Grant    Grant
	Revoked  bool
}
type hostConnection struct {
	identity                            Identity
	hostID, runtime, epoch, grantDigest string
	grantExpires                        time.Time
	conn                                *websocket.Conn
	mu                                  sync.Mutex
	closed                              chan struct{}
	once                                sync.Once
	leases                              map[string]Binding
	pending                             map[string]chan Message
	generations                         map[string]uint64
	lastHeartbeat                       time.Time
}

func (h *hostConnection) send(value any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	select {
	case <-h.closed:
		return ErrUnavailable
	default:
	}
	h.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return h.conn.WriteJSON(value)
}
func (h *hostConnection) close() { h.once.Do(func() { close(h.closed); h.conn.Close() }) }

type Broker struct {
	mu     sync.Mutex
	root   string
	grants map[string]*hostGrant
	hosts  map[string]*hostConnection
	fences map[string]Fence
	now    func() time.Time
}

func NewBroker(root string) (*Broker, error) {
	fences, err := loadFences(root)
	if err != nil {
		return nil, err
	}
	broker := &Broker{root: root, grants: map[string]*hostGrant{}, hosts: map[string]*hostConnection{}, fences: fences, now: time.Now}
	// Crash between dispatch and receipt is never proof that a write failed.
	for id, fence := range fences {
		if fence.State == "dispatched" {
			if fence.Write {
				fence.State = "unknown"
			} else {
				fence.State = "fenced"
			}
			fence.UpdatedAt = broker.now()
			if err := persistFence(root, fence); err != nil {
				return nil, err
			}
			fences[id] = fence
		}
	}
	return broker, nil
}
func (b *Broker) IssueGrant(identity Identity) (Grant, error) {
	if !identity.valid() {
		return Grant{}, ErrFence
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// One active host grant per installation. Explicit replacement invalidates
	// existing resources; a login token by itself never acquires host authority.
	for id, g := range b.grants {
		if g.Identity == identity {
			g.Revoked = true
			delete(b.grants, id)
		}
	}
	if host := b.hosts[identity.key()]; host != nil {
		host.close()
	}
	if len(b.grants) >= 1024 {
		return Grant{}, ErrFence
	}
	token := opaque("grant_") + opaque("")
	grant := Grant{HostID: opaque("host_"), Token: token, Digest: digest(token), ExpiresAt: b.now().Add(GrantDuration)}
	b.grants[grant.HostID] = &hostGrant{Identity: identity, Grant: grant}
	return grant, nil
}
func (b *Broker) RevokeGrant(identity Identity, hostID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	g := b.grants[hostID]
	if g == nil || g.Identity != identity {
		return ErrFence
	}
	g.Revoked = true
	delete(b.grants, hostID)
	if host := b.hosts[identity.key()]; host != nil && host.hostID == hostID {
		host.close()
	}
	return nil
}
func (b *Broker) RevokeClientHosts(clientID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, g := range b.grants {
		if g.Identity.ClientID == clientID {
			delete(b.grants, id)
		}
	}
	for _, h := range b.hosts {
		if h.identity.ClientID == clientID {
			h.close()
		}
	}
}
func (b *Broker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, host := range b.hosts {
		host.close()
	}
	b.grants = map[string]*hostGrant{}
	return nil
}

// ServeHost must be called inside the authenticated gateway's revocable client
// context after verifying this installation's server-side binding. All secrets
// travel in headers on pinned WSS, never query strings or renderer state.
func (b *Broker) ServeHost(ctx context.Context, w http.ResponseWriter, r *http.Request, identity Identity) {
	if !identity.valid() || r.TLS == nil || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" {
		http.Error(w, "host access rejected", http.StatusForbidden)
		return
	}
	hostID := r.Header.Get("X-SparkClaw-Host-ID")
	token := r.Header.Get("X-SparkClaw-Host-Grant")
	runtime := r.Header.Get("X-SparkClaw-Runtime")
	if !idPattern.MatchString(runtime) || len(token) > 160 {
		http.Error(w, "host access rejected", http.StatusForbidden)
		return
	}
	b.mu.Lock()
	g := b.grants[hostID]
	valid := g != nil && !g.Revoked && g.Identity == identity && b.now().Before(g.Grant.ExpiresAt) && subtle.ConstantTimeCompare([]byte(digest(token)), []byte(g.Grant.Digest)) == 1
	if !valid {
		b.mu.Unlock()
		http.Error(w, "host access rejected", http.StatusForbidden)
		return
	}
	expires := g.Grant.ExpiresAt
	authorization := g.Grant.Digest
	b.mu.Unlock()
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	host := &hostConnection{identity: identity, hostID: hostID, runtime: runtime, epoch: opaque("epoch_"), grantDigest: authorization, grantExpires: expires, conn: conn, closed: make(chan struct{}), leases: map[string]Binding{}, pending: map[string]chan Message{}, generations: map[string]uint64{}, lastHeartbeat: b.now()}
	b.mu.Lock()
	// Grant replacement can race the WebSocket handshake.
	if b.grants[hostID] != g || !b.now().Before(expires) {
		b.mu.Unlock()
		host.close()
		return
	}
	old := b.hosts[identity.key()]
	b.hosts[identity.key()] = host
	b.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer b.disconnect(host)
	conn.SetReadLimit(MaxMessageBytes)
	if err := host.send(map[string]any{"schema_version": 1, "type": "welcome", "host_id": hostID, "runtime_generation": runtime, "connection_epoch": host.epoch, "authorization_digest": authorization, "grant_expires_at": expires, "lease_seconds": 30, "heartbeat_seconds": 10}); err != nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
			host.close()
		case <-host.closed:
		}
	}()
	conn.SetReadDeadline(time.Now().Add(LeaseDuration))
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var message Message
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&message); err != nil || message.SchemaVersion != 1 {
			return
		}
		switch message.Type {
		case "heartbeat":
			if message.Binding != nil || message.CommandID != "" || len(message.Output) != 0 {
				return
			}
			b.mu.Lock()
			if b.hosts[identity.key()] != host || !b.now().Before(expires) {
				b.mu.Unlock()
				return
			}
			host.lastHeartbeat = b.now()
			bindings := make([]Binding, 0, len(host.leases))
			for id, binding := range host.leases {
				binding.LeaseExpiresAt = minTime(b.now().Add(LeaseDuration), expires)
				host.leases[id] = binding
				bindings = append(bindings, binding)
			}
			b.mu.Unlock()
			conn.SetReadDeadline(time.Now().Add(LeaseDuration))
			if err := host.send(map[string]any{"schema_version": 1, "type": "renew", "bindings": bindings, "connection_epoch": host.epoch}); err != nil {
				return
			}
		case "result":
			if message.Binding == nil || message.CommandID == "" || len(message.Output) > 96<<10 || (message.Status != "completed" && message.Status != "failed" && message.Status != "unknown") {
				return
			}
			b.mu.Lock()
			fence, ok := b.fences[message.CommandID]
			pending := host.pending[message.CommandID]
			valid := ok && fence.State == "dispatched" && pending != nil && b.hosts[identity.key()] == host && message.Binding.Scope == fence.Scope && message.Binding.HostID == fence.HostID && message.Binding.RuntimeGeneration == fence.RuntimeGeneration && message.Binding.ConnectionEpoch == fence.ConnectionEpoch && message.Binding.LeaseID == fence.LeaseID && message.Binding.PageID == fence.PageID && message.Binding.PageGeneration == fence.PageGeneration && message.Binding.AuthorizationDigest == fence.AuthorizationDigest
			if valid {
				lease, live := host.leases[fence.LeaseID]
				valid = live && b.now().Before(lease.LeaseExpiresAt)
			}
			if valid {
				select {
				case pending <- message:
				default:
				}
			}
			b.mu.Unlock()
		default:
			return
		}
	}
}
func minTime(a, c time.Time) time.Time {
	if a.Before(c) {
		return a
	}
	return c
}
func (b *Broker) disconnect(host *hostConnection) {
	host.close()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.hosts[host.identity.key()] == host {
		delete(b.hosts, host.identity.key())
	}
	for id, pending := range host.pending {
		fence := b.fences[id]
		if fence.State == "dispatched" {
			if fence.Write {
				fence.State = "unknown"
			} else {
				fence.State = "fenced"
			}
			fence.UpdatedAt = b.now()
			if persistFence(b.root, fence) == nil {
				b.fences[id] = fence
			}
		}
		select {
		case pending <- Message{Status: "unknown", ErrorCode: "host_disconnected"}:
		default:
		}
	}
	host.leases = map[string]Binding{}
}
func (b *Broker) Acquire(ctx context.Context, scope Scope) (Binding, error) {
	if !scope.valid() {
		return Binding{}, ErrFence
	}
	b.mu.Lock()
	host := b.hosts[scope.Identity.key()]
	if host == nil || !b.now().Before(host.grantExpires) || b.now().Sub(host.lastHeartbeat) >= LeaseDuration {
		b.mu.Unlock()
		return Binding{}, ErrUnavailable
	}
	for _, fence := range b.fences {
		if fence.Scope.Identity == scope.Identity && fence.Scope.ConversationID == scope.ConversationID && fence.Write && (fence.State == "unknown" || fence.State == "dispatched") {
			b.mu.Unlock()
			return Binding{}, ErrUnknown
		}
	}
	for _, binding := range host.leases {
		if binding.Scope.ConversationID == scope.ConversationID {
			if binding.Scope.TaskID == scope.TaskID && b.now().Before(binding.LeaseExpiresAt) {
				b.mu.Unlock()
				return binding, nil
			}
			b.mu.Unlock()
			return Binding{}, ErrFence
		}
	}
	if len(host.leases) >= 32 {
		b.mu.Unlock()
		return Binding{}, ErrFence
	}
	host.generations[scope.ConversationID]++
	binding := Binding{Scope: scope, HostID: host.hostID, RuntimeGeneration: host.runtime, ConnectionEpoch: host.epoch, LeaseID: opaque("lease_"), PageID: opaque("page_"), PageGeneration: host.generations[scope.ConversationID], AuthorizationDigest: host.grantDigest, LeaseExpiresAt: minTime(b.now().Add(LeaseDuration), host.grantExpires)}
	host.leases[binding.LeaseID] = binding
	b.mu.Unlock()
	if _, err := b.Dispatch(ctx, binding, opaque("cmd_"), "acquire", map[string]any{}); err != nil {
		b.mu.Lock()
		delete(host.leases, binding.LeaseID)
		b.mu.Unlock()
		return Binding{}, err
	}
	return binding, nil
}
func (b *Broker) Dispatch(ctx context.Context, binding Binding, commandID, operation string, args map[string]any) (json.RawMessage, error) {
	if !idPattern.MatchString(commandID) || validateOperation(operation, args) != nil {
		return nil, ErrFence
	}
	b.mu.Lock()
	host := b.hosts[binding.Scope.Identity.key()]
	if host == nil || host.hostID != binding.HostID || host.runtime != binding.RuntimeGeneration || host.epoch != binding.ConnectionEpoch || host.grantDigest != binding.AuthorizationDigest {
		b.mu.Unlock()
		return nil, ErrFence
	}
	live, ok := host.leases[binding.LeaseID]
	if !ok || live.PageID != binding.PageID || live.PageGeneration != binding.PageGeneration || live.Scope != binding.Scope || !b.now().Before(live.LeaseExpiresAt) || !b.now().Before(host.grantExpires) {
		b.mu.Unlock()
		return nil, ErrFence
	}
	binding.LeaseExpiresAt = live.LeaseExpiresAt
	command := Command{SchemaVersion: 1, Type: "command", CommandID: commandID, Binding: binding, Operation: operation, Arguments: args}
	command.Digest = commandDigest(command)
	if prior, exists := b.fences[commandID]; exists {
		b.mu.Unlock()
		if prior.Digest != command.Digest {
			return nil, ErrFence
		}
		if prior.Write {
			return nil, ErrUnknown
		}
		return nil, ErrFence
	}
	if len(b.fences) >= MaxFences {
		b.mu.Unlock()
		return nil, ErrFence
	}
	for _, prior := range b.fences {
		if prior.Scope.Identity == binding.Scope.Identity && prior.Scope.ConversationID == binding.Scope.ConversationID && prior.Write && prior.State == "unknown" && operation != "release" {
			b.mu.Unlock()
			return nil, ErrUnknown
		}
	}
	fence := Fence{CommandID: commandID, Scope: binding.Scope, HostID: binding.HostID, RuntimeGeneration: binding.RuntimeGeneration, ConnectionEpoch: binding.ConnectionEpoch, LeaseID: binding.LeaseID, PageID: binding.PageID, PageGeneration: binding.PageGeneration, AuthorizationDigest: binding.AuthorizationDigest, Digest: command.Digest, Write: writeOperation(operation), State: "dispatched", UpdatedAt: b.now()}
	if err := persistFence(b.root, fence); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	b.fences[commandID] = fence
	pending := make(chan Message, 1)
	host.pending[commandID] = pending
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(host.pending, commandID); b.mu.Unlock() }()
	err := host.send(command)
	var response Message
	if err == nil {
		timer := time.NewTimer(LeaseDuration)
		defer timer.Stop()
		select {
		case response = <-pending:
		case <-ctx.Done():
			err = ctx.Err()
		case <-host.closed:
			err = ErrUnavailable
		case <-timer.C:
			err = ErrUnavailable
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	fence = b.fences[commandID]
	if err != nil || response.Status == "unknown" {
		if fence.Write {
			fence.State = "unknown"
			err = ErrUnknown
		} else {
			fence.State = "fenced"
			err = ErrFence
		}
	} else if response.Status == "failed" {
		fence.State = "failed"
		err = ErrFence
	} else {
		fence.State = "completed"
	}
	fence.UpdatedAt = b.now()
	if saveErr := persistFence(b.root, fence); saveErr != nil {
		return nil, saveErr
	}
	b.fences[commandID] = fence
	if operation == "release" {
		delete(host.leases, binding.LeaseID)
	}
	return response.Output, err
}
func (b *Broker) Release(ctx context.Context, binding Binding) error {
	_, err := b.Dispatch(ctx, binding, opaque("cmd_"), "release", map[string]any{})
	return err
}

// Reconcile is an explicit control action after reviewing independent website
// evidence. It cannot replay a write or manufacture its missing business result.
func (b *Broker) Reconcile(identity Identity, commandID, digestValue, outcome string) error {
	if outcome != "observed_completed" && outcome != "observed_not_applied" {
		return ErrFence
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	fence, ok := b.fences[commandID]
	if ok && fence.Scope.Identity == identity && fence.Digest == digestValue && fence.State == outcome {
		return nil
	}
	if !ok || fence.Scope.Identity != identity || fence.Digest != digestValue || fence.State != "unknown" {
		return ErrFence
	}
	fence.State = outcome
	fence.UpdatedAt = b.now()
	if err := persistFence(b.root, fence); err != nil {
		return err
	}
	b.fences[commandID] = fence
	return nil
}
func (b *Broker) Fences(identity Identity) []Fence {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []Fence{}
	for _, f := range b.fences {
		if f.Scope.Identity == identity && f.State == "unknown" {
			out = append(out, f)
		}
	}
	return out
}
