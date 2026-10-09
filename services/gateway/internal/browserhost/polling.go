package browserhost

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"sync"
	"time"
)

// PollMessage has a transport-local sequence. Repeating an acknowledged cursor
// cannot consume another command, and a gap closes the host instead of skipping.
type PollMessage struct {
	Sequence uint64          `json:"sequence"`
	Body     json.RawMessage `json:"body"`
}
type pollingWire struct {
	mu     sync.Mutex
	next   uint64
	ack    uint64
	queue  []PollMessage
	closed bool
}

func (*pollingWire) SetWriteDeadline(time.Time) error { return nil }
func (w *pollingWire) WriteJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > MaxMessageBytes {
		return ErrFence
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || len(w.queue) >= 64 {
		return ErrUnavailable
	}
	w.next++
	w.queue = append(w.queue, PollMessage{w.next, raw})
	return nil
}
func (w *pollingWire) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.queue = nil
	return nil
}
func (w *pollingWire) poll(after uint64) ([]PollMessage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || after < w.ack || after > w.next {
		return nil, ErrFence
	}
	w.ack = after
	for len(w.queue) > 0 && w.queue[0].Sequence <= after {
		w.queue = w.queue[1:]
	}
	out := []PollMessage{}
	if len(w.queue) > 0 {
		row := w.queue[0]
		row.Body = append(json.RawMessage(nil), row.Body...)
		out = append(out, row)
	}
	return out, nil
}
func (b *Broker) OpenPolling(ctx context.Context, identity Identity, hostID, token, runtime string, onClose func()) (string, error) {
	if !identity.valid() || !idPattern.MatchString(runtime) || len(token) > 160 {
		return "", ErrFence
	}
	b.mu.Lock()
	g := b.grants[hostID]
	if g == nil || g.Revoked || g.Identity != identity || !b.now().Before(g.Grant.ExpiresAt) || subtle.ConstantTimeCompare([]byte(digest(token)), []byte(g.Grant.Digest)) != 1 {
		b.mu.Unlock()
		return "", ErrFence
	}
	wire := &pollingWire{}
	host := &hostConnection{identity: identity, hostID: hostID, runtime: runtime, epoch: opaque("epoch_"), grantDigest: g.Grant.Digest, grantExpires: g.Grant.ExpiresAt, conn: wire, closed: make(chan struct{}), leases: map[string]Binding{}, pending: map[string]chan Message{}, generations: map[string]uint64{}, lastHeartbeat: b.now()}
	old := b.hosts[identity.key()]
	b.hosts[identity.key()] = host
	b.mu.Unlock()
	if old != nil {
		b.disconnect(old)
	}
	if err := host.send(map[string]any{"schema_version": 1, "type": "welcome", "host_id": hostID, "runtime_generation": runtime, "connection_epoch": host.epoch, "authorization_digest": host.grantDigest, "grant_expires_at": host.grantExpires, "lease_seconds": 30, "heartbeat_seconds": 10}); err != nil {
		b.disconnect(host)
		return "", err
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer b.disconnect(host)
		if onClose != nil {
			defer onClose()
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-host.closed:
				return
			case <-ticker.C:
				b.mu.Lock()
				valid := b.hosts[identity.key()] == host && b.now().Before(host.grantExpires) && b.now().Sub(host.lastHeartbeat) < LeaseDuration
				b.mu.Unlock()
				if !valid {
					return
				}
			}
		}
	}()
	return host.epoch, nil
}
func (b *Broker) pollingHost(identity Identity, hostID, epoch string) (*hostConnection, *pollingWire, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	host := b.hosts[identity.key()]
	if host == nil || host.hostID != hostID || host.epoch != epoch || !b.now().Before(host.grantExpires) || b.now().Sub(host.lastHeartbeat) >= LeaseDuration {
		return nil, nil, ErrFence
	}
	wire, ok := host.conn.(*pollingWire)
	if !ok {
		return nil, nil, ErrFence
	}
	return host, wire, nil
}
func (b *Broker) Poll(identity Identity, hostID, epoch string, after uint64) ([]PollMessage, error) {
	_, wire, err := b.pollingHost(identity, hostID, epoch)
	if err != nil {
		return nil, err
	}
	return wire.poll(after)
}
func (b *Broker) ReceivePolling(identity Identity, hostID, epoch string, message Message) error {
	host, _, err := b.pollingHost(identity, hostID, epoch)
	if err != nil {
		return err
	}
	return b.receiveHostMessage(host, message)
}
func (b *Broker) ClosePolling(identity Identity, hostID, epoch string) error {
	host, _, err := b.pollingHost(identity, hostID, epoch)
	if err != nil {
		return err
	}
	b.disconnect(host)
	return nil
}
