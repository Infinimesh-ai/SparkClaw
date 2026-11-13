package mcdintegration

import "context"

// AbortHandoff only releases after a matching authenticated Mac receipt. The
// channel callback cannot replace the permanent original epoch tombstone.
func (m *Manager) AbortHandoff(ctx context.Context, p Principal, x Execution, h Handoff, reason string, call func(context.Context) (map[string]any, error)) error {
	if !p.matches(x.Identity) || p.DeploymentID != m.deployment || h.ScopeDigest != digest([]any{x.Identity, x.ExecutionID}) {
		return ErrScope
	}
	c, err := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
	if err != nil {
		return err
	}
	c.now = m.now
	if _, err = c.RequestAbort(ctx, h.ScopeDigest, h.PurchaseID, h.Epoch, reason); err != nil {
		return err
	}
	receipt, err := call(ctx)
	if err != nil {
		return ErrUnavailable
	}
	// Strict typed host decoding normalizes integer receipt fields at the route.
	if receipt["state"] != "aborted_before_write" || receipt["purchase_id"] != h.PurchaseID || receipt["handoff_epoch"] != h.Epoch || receipt["reason"] != reason {
		return ErrConflict
	}
	return c.FinishAbort(ctx, h.ScopeDigest, h.PurchaseID, h.Epoch, "aborted_before_write", digest(receipt))
}
