package mcdintegration

import "context"

func CommitKey(h Handoff) string { return "commit-" + digest([]any{h.PurchaseID, h.Epoch}) }

// PendingHandoff exposes only the originating workbench's durable metadata.
// It permits status/abort after the selection execution or lease has expired.
func (m *Manager) PendingHandoff(ctx context.Context, p Principal, x Execution) (*Handoff, error) {
	if !p.matches(x.Identity) || p.DeploymentID != m.deployment {
		return nil, ErrScope
	}
	c, err := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
	if err != nil {
		return nil, err
	}
	c.now = m.now
	r, _, _, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	if r.Handoff == nil {
		return nil, nil
	}
	h := *r.Handoff
	if h.Identity != x.Identity || !p.matches(h.Identity) || !idPattern.MatchString(h.ExecutionID) || h.CredentialRevision < 1 {
		return nil, ErrScope
	}
	return &h, nil
}

// StartCommit is persisted before the reverse command. Neither a timeout nor a
// new execution replays it. Every subsequent attempt must query the same key.
func (m *Manager) StartCommit(ctx context.Context, p Principal, x Execution, h Handoff) error {
	if !p.matches(x.Identity) || h.Identity != x.Identity || h.ExecutionID != x.ExecutionID || h.CredentialRevision != x.CredentialRevision || m.now().UnixMilli() >= h.DeadlineMS {
		return ErrScope
	}
	connection, err := m.connection(p, x.Identity, x.ConnectionID)
	if err != nil {
		return err
	}
	current, _, err := connection.load(ctx)
	if err != nil {
		return err
	}
	now := m.now().UnixMilli()
	if current.Closed || current.Session == nil || !current.MaterialPresent || current.Session.CredentialRevision != x.CredentialRevision || now < current.ObservedMS || now >= current.Session.SessionNotAfterMS || now >= x.DeadlineMS {
		return ErrExpired
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
	if r.Handoff == nil || *r.Handoff != h || r.Handoff.State != "frozen" {
		return ErrConflict
	}
	r.Handoff.State = "commit_started"
	return c.save(ctx, r, gen, anchorGen)
}

func (m *Manager) FinishPurchase(ctx context.Context, p Principal, x Execution, h Handoff, receipt map[string]any) error {
	if !p.matches(x.Identity) || h.Identity != x.Identity || receipt["purchase_id"] != h.PurchaseID || receipt["handoff_epoch"] != h.Epoch || receipt["terminal_verified"] != true || (receipt["state"] != "PAID" && receipt["state"] != "CANCELLED") {
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
	key := "terminal-" + CommitKey(h)
	proof := digest(receipt)
	if old, ok := r.Operations[key]; ok {
		if old.EvidenceDigest == proof {
			return nil
		}
		return ErrConflict
	}
	if r.Handoff == nil || r.Handoff.PurchaseID != h.PurchaseID || r.Handoff.Epoch != h.Epoch || r.Handoff.ScopeDigest != h.ScopeDigest || len(r.Operations) >= 512 {
		return ErrScope
	}
	r.Operations[key] = CartReceipt{OperationKey: key, InputDigest: h.EnvelopeDigest, ScopeDigest: h.ScopeDigest, State: receipt["state"].(string), EvidenceDigest: proof}
	r.Closed[h.PurchaseID] = true
	r.Handoff = nil
	return c.save(ctx, r, gen, anchorGen)
}
