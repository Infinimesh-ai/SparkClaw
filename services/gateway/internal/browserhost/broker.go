package browserhost

import (
	"sync"
	"time"
)

type Broker struct {
	mu         sync.Mutex
	root       string
	fences     map[string]Fence
	now        func() time.Time
	reverse    ReverseRouter
	controller *ClientControllerHost
}

func NewBroker(root string) (*Broker, error) {
	fences, err := loadFences(root)
	if err != nil {
		return nil, err
	}
	broker := &Broker{root: root, fences: fences, now: time.Now}
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
func (b *Broker) RevokeClientHosts(clientID string) {
	b.mu.Lock()
	controller := b.controller
	b.mu.Unlock()
	if controller != nil {
		controller.closeClient(clientID)
	}
}

func (b *Broker) Close() error {
	b.RevokeClientHosts("")
	return nil
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
