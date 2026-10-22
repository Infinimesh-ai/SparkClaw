package emailautomation

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type providerLane string

const (
	// The inbound lane admits the shared receive page: probe, read, discovery,
	// capture, thread enumeration and timeline pages.
	providerLaneInbound providerLane = "inbound"
	// The mutation lane admits send and mark-read, which own a task page each.
	providerLaneMutation providerLane = "mutation"
)

// Providers use independent sites in the shared browser profile. Serialize each
// lane of one provider across owners, while letting the other lane and other
// sites progress independently: a send must not hold inbound capture for its
// whole duration. Whole-profile exclusivity (login, generic browser access)
// stays with the Browser Controller. Waiting never consumes a browser task
// after cancel.
func (c *Controller) lockProvider(ctx context.Context, providerID string) (func(), error) {
	return c.lockProviderLane(ctx, providerID, providerLaneInbound)
}

func (c *Controller) lockProviderLane(ctx context.Context, providerID string, lane providerLane) (func(), error) {
	provider, ok := c.registry.Get(providerID)
	if !ok {
		return nil, codedError(app.ToolErrorEmailInvalidInput, "Email provider is not registered")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.providerLocksMu.Lock()
	if c.providerLocks == nil {
		c.providerLocks = make(map[string]chan struct{})
	}
	key := provider.ID + "/" + string(lane)
	gate := c.providerLocks[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		c.providerLocks[key] = gate
	}
	c.providerLocksMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-gate
			return nil, err
		}
		return func() { <-gate }, nil
	}
}
