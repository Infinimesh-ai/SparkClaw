package mcdintegration

import (
	"testing"
	"time"
)

func statusAbortFixture(t *testing.T, state string) (*Manager, *CartControl, Principal, Execution, Handoff) {
	t.Helper()
	_, vault, runner, envelope, now := harness(t)
	m, err := NewManager(vault, runner, envelope.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	m.now = func() time.Time { return time.UnixMilli(now.Load()) }
	i := envelope.Identity
	p := Principal{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID}
	if _, err = m.Provision(t.Context(), p, encoded(t, envelope)); err != nil {
		t.Fatal(err)
	}
	x, err := m.Selection(t.Context(), p, "original-execution")
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCartControl(vault, i.DeploymentID, i.MemberDigest)
	if err != nil {
		t.Fatal(err)
	}
	c.now = m.now
	h, err := c.Freeze(t.Context(), i, x.ExecutionID, Handoff{Identity: i, ExecutionID: x.ExecutionID,
		CredentialRevision: x.CredentialRevision, PurchaseID: "purchase", Epoch: 1, QuoteRevision: 1,
		PurchaseDigest: digest("meal"), EnvelopeDigest: digest("quote"), DeadlineMS: now.Load() + 120000})
	if err != nil {
		t.Fatal(err)
	}
	if state == "commit_started" {
		if err = m.StartCommit(t.Context(), p, x, h); err != nil {
			t.Fatal(err)
		}
	} else if state == "abort_requested" {
		if _, err = c.RequestAbort(t.Context(), h.ScopeDigest, h.PurchaseID, h.Epoch, "quote_refresh"); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := m.PendingHandoff(t.Context(), p, x)
	if err != nil || pending == nil {
		t.Fatal(pending, err)
	}
	return m, c, p, x, *pending
}

func statusAbortReceipt(h Handoff) map[string]any {
	return map[string]any{"state": "aborted_before_write", "operation_key": CommitKey(h),
		"scope_digest": digest(QuoteScope{Identity: h.Identity, CredentialRevision: h.CredentialRevision, ExecutionID: h.ExecutionID}),
		"purchase_id":  h.PurchaseID, "handoff_epoch": h.Epoch, "non_dispatch_proven": true, "purchase_closed": true,
		"reason": "execution_termination", "terminal_verified": false, "automatic_write_retry": false,
		"official_order_canceled": false, "refund_attempted": false}
}

func TestStatusAbortClosesOriginalPurchaseAfterRestartAndRevocation(t *testing.T) {
	for _, state := range []string{"frozen", "commit_started", "abort_requested"} {
		t.Run(state, func(t *testing.T) {
			m, c, p, x, h := statusAbortFixture(t, state)
			if _, err := m.Revoke(t.Context(), p, x.Identity, x.ConnectionID, "disconnect"); err != nil {
				t.Fatal(err)
			}
			cold, err := NewManager(m.vault, m.runner, m.deployment)
			if err != nil {
				t.Fatal(err)
			}
			cold.now = m.now
			recovery, err := cold.Recovery(t.Context(), p, "recovery-execution")
			if err != nil {
				t.Fatal(err)
			}
			proof := statusAbortReceipt(h)
			for range 2 {
				if err = cold.FinishStatusAbort(t.Context(), p, recovery, h, proof); err != nil {
					t.Fatal(err)
				}
			}
			r, _, _, err := c.load(t.Context())
			if err != nil || r.Handoff != nil || !r.Closed[h.PurchaseID] {
				t.Fatal(r, err)
			}
			negative := r.Operations["abort-"+digest([]any{h.ScopeDigest, h.PurchaseID, h.Epoch})]
			if negative.State != "aborted_before_write" || negative.InputDigest != h.EnvelopeDigest || negative.EvidenceDigest != digest(proof) {
				t.Fatal("original negative receipt missing", negative)
			}
			h.Epoch++
			h.QuoteRevision++
			if _, err = c.Freeze(t.Context(), h.Identity, h.ExecutionID, h); err == nil {
				t.Fatal("retired purchase became payable")
			}
		})
	}
}

func TestStatusAbortRequiresCompleteOriginalAuthenticatedProof(t *testing.T) {
	m, c, p, x, h := statusAbortFixture(t, "commit_started")
	for field, bad := range map[string]any{"state": "not_found", "operation_key": "commit-other", "scope_digest": digest("foreign"),
		"purchase_id": "foreign", "handoff_epoch": int64(2), "non_dispatch_proven": false, "purchase_closed": false,
		"reason": "quote_refresh", "terminal_verified": true, "automatic_write_retry": true, "official_order_canceled": true, "refund_attempted": true} {
		t.Run(field, func(t *testing.T) {
			proof := statusAbortReceipt(h)
			proof[field] = bad
			if err := m.FinishStatusAbort(t.Context(), p, x, h, proof); err == nil {
				t.Fatal("bad proof released original")
			}
		})
	}
	foreign := p
	foreign.ClientID = "other"
	if err := m.FinishStatusAbort(t.Context(), foreign, x, h, statusAbortReceipt(h)); err == nil {
		t.Fatal("foreign principal accepted")
	}
	r, _, _, err := c.load(t.Context())
	if err != nil || r.Handoff == nil || *r.Handoff != h || len(r.Operations) != 0 {
		t.Fatal("failed proofs mutated journal", r, err)
	}
}

func TestOldStatusAbortCannotRetireNewerEpoch(t *testing.T) {
	m, c, p, x, old := statusAbortFixture(t, "frozen")
	if _, err := c.RequestAbort(t.Context(), old.ScopeDigest, old.PurchaseID, old.Epoch, "quote_refresh"); err != nil {
		t.Fatal(err)
	}
	if err := c.FinishAbort(t.Context(), old.ScopeDigest, old.PurchaseID, old.Epoch, "aborted_before_write", digest("original-abort")); err != nil {
		t.Fatal(err)
	}
	next := old
	next.Epoch++
	next.QuoteRevision++
	next, err := c.Freeze(t.Context(), x.Identity, x.ExecutionID, next)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.FinishStatusAbort(t.Context(), p, x, old, statusAbortReceipt(old)); err == nil {
		t.Fatal("old proof closed newer preparation")
	}
	r, _, _, err := c.load(t.Context())
	if err != nil || r.Handoff == nil || *r.Handoff != next || r.Closed[old.PurchaseID] {
		t.Fatal(r, err)
	}
}
