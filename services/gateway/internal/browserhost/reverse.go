package browserhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"
)

// MaxResultBytes bounds one host result body so a whole reverse response,
// including its binding and envelope, stays inside one 64 KiB ISCP message.
const MaxResultBytes = 56 << 10

// MaxCaptureBytes is the largest PNG whose base64 form fits that budget.
const MaxCaptureBytes = 39 << 10

// ReadyWait bounds how long Health waits for a session that is recovering.
const ReadyWait = 10 * time.Second

var readyWait = ReadyWait

// ErrNotDispatched proves a reverse command never reached the page: no
// session was available, or the desktop refused it before executing.
var ErrNotDispatched = errors.New("workbench browser command was not dispatched")

// ReverseSession is one authenticated ISCP session to an installation's
// desktop. Authorization is the session's own: there is no separate host grant,
// heartbeat or lease renewal. Done closes when the session ends for any reason.
type ReverseSession interface {
	SessionID() string
	AuthorizationRevision() uint64
	Done() <-chan struct{}
	Command(context.Context, json.RawMessage) (json.RawMessage, error)
}

// ReverseRouter finds the live session for an installation using local state
// only; it never sends anything to the desktop.
type ReverseRouter interface {
	ReverseSession(Identity) (ReverseSession, error)
}

// SetReverseRouter lets the broker address embedded browsers through the ISCP
// sessions the router owns. Without one, only LAN WebSocket hosts exist.
func (b *Broker) SetReverseRouter(router ReverseRouter) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reverse = router
}

// The desktop derives the same values from its own session, so it can verify
// every binding without trusting anything the Gateway invented.
func rawDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func reverseHostID(identity Identity, sessionID string) string {
	return "iscp_" + rawDigest("sparkclaw.browser.iscp.host.v1\x00" + identity.OwnerID + "\x00" + identity.ClientID + "\x00" + identity.InstallationID + "\x00" + sessionID)[:32]
}
func reverseAuthorizationDigest(sessionID string, revision uint64) string {
	return rawDigest("sparkclaw.browser.iscp.authorization.v1\x00" + sessionID + "\x00" + strconv.FormatUint(revision, 10))
}

// reverseWire satisfies the shared host connection; reverse commands are
// synchronous ISCP calls and never queue on a wire.
type reverseWire struct{}

func (reverseWire) SetWriteDeadline(time.Time) error { return nil }
func (reverseWire) WriteJSON(any) error              { return ErrFence }
func (reverseWire) Close() error                     { return nil }

func sessionDone(session ReverseSession) bool {
	select {
	case <-session.Done():
		return true
	default:
		return false
	}
}

func reverseUsable(host *hostConnection) bool {
	select {
	case <-host.closed:
		return false
	default:
		return !sessionDone(host.reverse)
	}
}

// liveHost returns the installation's host. An ISCP host is created on the
// first command of a task, from the session that is current at that moment,
// and disappears with that session.
func (b *Broker) liveHost(identity Identity) (*hostConnection, error) {
	b.mu.Lock()
	host := b.hosts[identity.key()]
	if host != nil && host.reverse == nil {
		defer b.mu.Unlock()
		if !b.now().Before(host.grantExpires) || b.now().Sub(host.lastHeartbeat) >= LeaseDuration {
			return nil, ErrUnavailable
		}
		return host, nil
	}
	if host != nil && reverseUsable(host) {
		b.mu.Unlock()
		return host, nil
	}
	router := b.reverse
	b.mu.Unlock()
	if router == nil {
		return nil, ErrUnavailable
	}
	session, err := router.ReverseSession(identity)
	if err != nil || session == nil || sessionDone(session) || !idPattern.MatchString(session.SessionID()) || session.AuthorizationRevision() == 0 {
		return nil, ErrUnavailable
	}
	b.mu.Lock()
	if existing := b.hosts[identity.key()]; existing != nil {
		if existing.reverse == nil || reverseUsable(existing) && existing.epoch == session.SessionID() {
			b.mu.Unlock()
			return b.liveHost(identity)
		}
	}
	id := session.SessionID()
	host = &hostConnection{identity: identity, hostID: reverseHostID(identity, id), runtime: "iscp_" + id, epoch: id, grantDigest: reverseAuthorizationDigest(id, session.AuthorizationRevision()), conn: reverseWire{}, reverse: session, closed: make(chan struct{}), leases: map[string]Binding{}, pending: map[string]chan Message{}, generations: map[string]uint64{}}
	old := b.hosts[identity.key()]
	b.hosts[identity.key()] = host
	b.mu.Unlock()
	if old != nil {
		b.disconnect(old)
	}
	go func() {
		select {
		case <-session.Done():
		case <-host.closed:
		}
		b.disconnect(host)
	}()
	return host, nil
}

// Ready reports whether a command could be dispatched now, from local state
// only. A session that is recovering is waited for, at most ReadyWait.
func (b *Broker) Ready(ctx context.Context, identity Identity) error {
	if !identity.valid() {
		return ErrFence
	}
	ctx, cancel := context.WithTimeout(ctx, readyWait)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if b.readyNow(identity) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ErrUnavailable
		case <-ticker.C:
		}
	}
}
func (b *Broker) readyNow(identity Identity) bool {
	b.mu.Lock()
	host, router := b.hosts[identity.key()], b.reverse
	if host != nil && host.reverse == nil {
		ready := b.now().Before(host.grantExpires) && b.now().Sub(host.lastHeartbeat) < LeaseDuration
		b.mu.Unlock()
		return ready
	}
	b.mu.Unlock()
	if host != nil && reverseUsable(host) {
		return true
	}
	if router == nil {
		return false
	}
	session, err := router.ReverseSession(identity)
	return err == nil && session != nil && !sessionDone(session)
}

// LeaseActive reports whether the broker still holds this binding's lease.
func (b *Broker) LeaseActive(binding Binding) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	host := b.hosts[binding.Scope.Identity.key()]
	if host == nil || host.hostID != binding.HostID || host.epoch != binding.ConnectionEpoch {
		return false
	}
	_, ok := host.leases[binding.LeaseID]
	return ok
}

// reverseCall performs one command round trip on the host's own session.
func (b *Broker) reverseCall(ctx context.Context, host *hostConnection, command Command) (Message, error) {
	raw, err := json.Marshal(command)
	if err != nil {
		return Message{}, ErrNotDispatched
	}
	body, err := host.reverse.Command(ctx, raw)
	if err != nil {
		return Message{}, err
	}
	var message Message
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&message) != nil || message.SchemaVersion != 1 || message.Type != "result" || message.CommandID != command.CommandID || !validResult(message, command.Binding) {
		// A malformed answer proves nothing about the page; stop using the host.
		go b.disconnect(host)
		return Message{}, ErrUnavailable
	}
	return message, nil
}

func validResult(message Message, binding Binding) bool {
	if message.Binding == nil || len(message.Output) > MaxResultBytes || (message.Status != "completed" && message.Status != "failed" && message.Status != "unknown") {
		return false
	}
	got := *message.Binding
	return got.Scope == binding.Scope && got.HostID == binding.HostID && got.RuntimeGeneration == binding.RuntimeGeneration && got.ConnectionEpoch == binding.ConnectionEpoch && got.LeaseID == binding.LeaseID && got.PageID == binding.PageID && got.PageGeneration == binding.PageGeneration && got.AuthorizationDigest == binding.AuthorizationDigest
}
