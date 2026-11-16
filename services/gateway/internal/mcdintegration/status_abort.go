package mcdintegration

import "context"

// FinishStatusAbort accepts the native host's permanent local retirement proof.
// Status recovery removes authority only; it never negotiates checkout.abort or
// substitutes not_found for proof. Its fixed termination reason also closes a
// purchase whose earlier quote_refresh abort was interrupted.
func (m *Manager) FinishStatusAbort(ctx context.Context, p Principal, x Execution, h Handoff, receipt map[string]any) error {
	scope := QuoteScope{Identity: h.Identity, CredentialRevision: h.CredentialRevision, ExecutionID: h.ExecutionID}
	if !p.matches(x.Identity) || p.DeploymentID != m.deployment || h.Identity != x.Identity ||
		h.ScopeDigest != digest([]any{h.Identity, h.ExecutionID}) || h.CredentialRevision < 1 ||
		receipt["state"] != "aborted_before_write" || receipt["operation_key"] != CommitKey(h) ||
		receipt["scope_digest"] != digest(scope) || receipt["purchase_id"] != h.PurchaseID || receipt["handoff_epoch"] != h.Epoch ||
		receipt["non_dispatch_proven"] != true || receipt["purchase_closed"] != true ||
		receipt["reason"] != "execution_termination" || receipt["terminal_verified"] != false ||
		receipt["automatic_write_retry"] != false || receipt["official_order_canceled"] != false || receipt["refund_attempted"] != false {
		return ErrConflict
	}
	c, err := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
	if err != nil {
		return err
	}
	c.now = m.now
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return err
	}
	key := "abort-" + digest([]any{h.ScopeDigest, h.PurchaseID, h.Epoch})
	proof := digest(receipt)
	if old, exists := r.Operations[key]; exists {
		if old.State == "aborted_before_write" && old.ScopeDigest == h.ScopeDigest && old.EvidenceDigest == proof && r.Closed[h.PurchaseID] {
			return nil
		}
		return ErrConflict
	}
	if r.Handoff == nil || *r.Handoff != h || len(r.Operations) >= 512 {
		return ErrConflict
	}
	switch h.State {
	case "frozen", "commit_started", "abort_requested":
	default:
		return ErrConflict
	}
	r.Operations[key] = CartReceipt{OperationKey: key, InputDigest: h.EnvelopeDigest, ScopeDigest: h.ScopeDigest,
		State: "aborted_before_write", EvidenceDigest: proof}
	r.Closed[h.PurchaseID] = true
	r.Handoff = nil
	return c.save(ctx, r, gen, anchorGen)
}
