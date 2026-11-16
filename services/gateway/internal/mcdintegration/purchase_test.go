package mcdintegration

import (
	"testing"
	"time"
)

func TestPurchaseCommitRestartScopeAndTerminalReceipt(t *testing.T) {
	_, v, runner, e, now := harness(t)
	m, err := NewManager(v, runner, e.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.UnixMilli(now.Load()) }
	i := e.Identity
	p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
	if _, err = m.Provision(t.Context(), p, encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	x, err := m.Selection(t.Context(), p, "original-execution")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewCartControl(v, i.DeploymentID, i.MemberDigest)
	c.now = m.now
	h, err := c.Freeze(t.Context(), i, x.ExecutionID, Handoff{Identity: i, ExecutionID: x.ExecutionID, CredentialRevision: x.CredentialRevision, PurchaseID: "purchase", Epoch: 1, QuoteRevision: 1, PurchaseDigest: digest("meal"), EnvelopeDigest: digest("quote"), DeadlineMS: now.Load() + 120000})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.StartCommit(t.Context(), p, x, h); err != nil {
		t.Fatal(err)
	}
	if err = m.StartCommit(t.Context(), p, x, h); err == nil {
		t.Fatal("replayed original commit")
	}
	cold, _ := NewManager(v, runner, i.DeploymentID)
	cold.now = m.now
	next := x
	next.ExecutionID = "new-execution"
	next.CredentialRevision++
	pending, err := cold.PendingHandoff(t.Context(), p, next)
	if err != nil || pending == nil || pending.State != "commit_started" || pending.ExecutionID != x.ExecutionID {
		t.Fatal(pending, err)
	}
	foreign := p
	foreign.ClientID = "foreign"
	if _, err = cold.PendingHandoff(t.Context(), foreign, next); err == nil {
		t.Fatal("foreign recovery accepted")
	}
	for _, bad := range []map[string]any{{"state": "not_found"}, {"state": "PAID", "terminal_verified": false, "purchase_id": h.PurchaseID, "handoff_epoch": h.Epoch}, {"state": "PAID", "terminal_verified": true, "purchase_id": "other", "handoff_epoch": h.Epoch}} {
		if cold.FinishPurchase(t.Context(), p, next, h, bad) == nil {
			t.Fatal("unverified terminal released freeze")
		}
	}
	receipt := map[string]any{"state": "PAID", "terminal_verified": true, "purchase_id": h.PurchaseID, "handoff_epoch": h.Epoch, "automatic_write_retry": false}
	if err = cold.FinishPurchase(t.Context(), p, next, h, receipt); err != nil {
		t.Fatal(err)
	}
	if pending, err = cold.PendingHandoff(t.Context(), p, next); err != nil || pending != nil {
		t.Fatal(pending, err)
	}
	if err = cold.StartCommit(t.Context(), p, x, h); err == nil {
		t.Fatal("terminal purchase reused")
	}
	if err = cold.FinishPurchase(t.Context(), p, next, h, receipt); err != nil {
		t.Fatal("original terminal receipt not idempotent", err)
	}
}

func TestPurchaseRevocationBlocksCommitButKeepsRecoveryIdentity(t *testing.T) {
	_, v, r, e, _ := harness(t)
	m, _ := NewManager(v, r, e.Identity.DeploymentID)
	i := e.Identity
	p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
	if _, err := m.Provision(t.Context(), p, encoded(t, e)); err != nil {
		t.Fatal(err)
	}
	x, err := m.Selection(t.Context(), p, "execution")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := NewCartControl(v, i.DeploymentID, i.MemberDigest)
	h, err := c.Freeze(t.Context(), i, x.ExecutionID, Handoff{Identity: i, ExecutionID: x.ExecutionID, CredentialRevision: x.CredentialRevision, PurchaseID: "purchase", Epoch: 1, QuoteRevision: 1, PurchaseDigest: digest("meal"), EnvelopeDigest: digest("quote"), DeadlineMS: time.Now().UnixMilli() + 120000})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Revoke(t.Context(), p, i, e.ConnectionID, "revoke"); err != nil {
		t.Fatal(err)
	}
	if m.StartCommit(t.Context(), p, x, h) == nil {
		t.Fatal("revoked connection purchased")
	}
	recovery, err := m.Recovery(t.Context(), p, "recovery")
	if err != nil || recovery.Identity != i {
		t.Fatal(recovery, err)
	}
	if _, err = m.Read(t.Context(), p, recovery, "store_get", []byte(`{"store_code":"3330613"}`)); err == nil {
		t.Fatal("recovery restored provider access")
	}
}
