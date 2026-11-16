package mcdintegration

import "context"

// Recovery uses only scoped control metadata and requires a currently admitted
// originating workbench. Retire is called only after explicit Policy approval.
func (m *Manager) CartRecovery(ctx context.Context, p Principal, x Execution, key, inputDigest string) ([]CartReceipt, error) {
	if !p.matches(x.Identity) || p.DeploymentID != m.deployment {
		return nil, ErrScope
	}
	c, err := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
	if err != nil {
		return nil, err
	}
	c.now = m.now
	if key == "" {
		return c.Pending(ctx, x.Identity)
	}
	receipt, err := c.Retire(ctx, x.Identity, key, inputDigest)
	return []CartReceipt{receipt}, err
}
