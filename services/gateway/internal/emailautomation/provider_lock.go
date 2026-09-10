package emailautomation

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// Providers use independent sites in the shared browser profile. Serialize all
// operations on one provider across owners, while allowing different sites to
// progress independently. Waiting never consumes a browser task after cancel.
func (c *Controller) lockProvider(ctx context.Context, providerID string) (func(), error) {
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
	gate := c.providerLocks[provider.ID]
	if gate == nil {
		gate = make(chan struct{}, 1)
		c.providerLocks[provider.ID] = gate
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
