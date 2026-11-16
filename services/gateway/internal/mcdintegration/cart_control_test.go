package mcdintegration

import (
	"errors"
	"testing"
	"time"
)

func TestCartControlUnknownSurvivesRestartAndBlocksOtherAlias(t *testing.T) {
	_, vault, _, e, _ := harness(t)
	c, err := NewCartControl(vault, e.Identity.DeploymentID, e.Identity.MemberDigest)
	if err != nil {
		t.Fatal(err)
	}
	intent := CartIntent{Identity: e.Identity, ExecutionID: "execution", OperationKey: "add", InputDigest: digest("draft"), Kind: "cart_add", DeadlineMS: time.Now().Add(time.Minute).UnixMilli()}
	first, dispatch, err := c.Begin(t.Context(), intent)
	if err != nil || !dispatch || first.State != "unknown" {
		t.Fatal(first, dispatch, err)
	}
	restarted, _ := NewCartControl(vault, e.Identity.DeploymentID, e.Identity.MemberDigest)
	replay, dispatch, err := restarted.Begin(t.Context(), intent)
	if err != nil || dispatch || replay != first {
		t.Fatal("unknown replayed", err)
	}
	other := intent
	other.Identity.AccountRef = "alias-two"
	other.OperationKey = "other"
	if _, _, err = restarted.Begin(t.Context(), other); !errors.Is(err, ErrConflict) {
		t.Fatal("alias bypassed unresolved cart", err)
	}
	drift := intent
	drift.InputDigest = digest("other-draft")
	if _, _, err = restarted.Begin(t.Context(), drift); !errors.Is(err, ErrConflict) {
		t.Fatal("key drift accepted", err)
	}
	if _, err = restarted.Confirm(t.Context(), intent, digest("matching-cart")); err != nil {
		t.Fatal(err)
	}
	if _, dispatch, err = restarted.Begin(t.Context(), intent); err != nil || dispatch {
		t.Fatal("confirmed cart replayed", err)
	}
	if _, dispatch, err = restarted.Begin(t.Context(), other); err != nil || !dispatch {
		t.Fatal("confirmed cart did not release member", err)
	}
}

func TestCartFreezeDoesNotReleaseOnExpiryOrUnprovenAbort(t *testing.T) {
	_, v, _, e, now := harness(t)
	c, _ := NewCartControl(v, e.Identity.DeploymentID, e.Identity.MemberDigest)
	c.now = func() time.Time { return time.UnixMilli(now.Load()) }
	h := Handoff{PurchaseID: "purchase", Epoch: 1, QuoteRevision: 1, PurchaseDigest: digest("purchase"), EnvelopeDigest: digest("quote"), DeadlineMS: now.Load() + 1000}
	h, err := c.Freeze(t.Context(), e.Identity, "execution", h)
	if err != nil {
		t.Fatal(err)
	}
	now.Add(2000)
	i := CartIntent{Identity: e.Identity, ExecutionID: "execution", OperationKey: "add", InputDigest: digest("draft"), Kind: "cart_add", DeadlineMS: now.Load() + 1000}
	if _, _, err = c.Begin(t.Context(), i); !errors.Is(err, ErrConflict) {
		t.Fatal("time released freeze", err)
	}
	if _, err = c.RequestAbort(t.Context(), h.ScopeDigest, h.PurchaseID, h.Epoch, "quote_refresh"); err != nil {
		t.Fatal(err)
	}
	if err = c.FinishAbort(t.Context(), h.ScopeDigest, h.PurchaseID, h.Epoch, "not_found", digest("receipt")); !errors.Is(err, ErrInvalid) {
		t.Fatal("not_found released freeze", err)
	}
	if _, _, err = c.Begin(t.Context(), i); !errors.Is(err, ErrConflict) {
		t.Fatal("pending abort released freeze", err)
	}
	for range 2 {
		if err = c.FinishAbort(t.Context(), h.ScopeDigest, h.PurchaseID, h.Epoch, "aborted_before_write", digest("receipt")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = c.Freeze(t.Context(), e.Identity, "execution", h); !errors.Is(err, ErrConflict) {
		t.Fatal("old epoch reused", err)
	}
	h.Epoch++
	h.QuoteRevision++
	h.DeadlineMS = now.Load() + 1000
	if _, err = c.Freeze(t.Context(), e.Identity, "execution", h); err != nil {
		t.Fatal("fresh same-purchase quote blocked", err)
	}
}

func TestCartRetirementPreservesUnknownAndCannotRestoreOriginalKey(t *testing.T) {
	_, v, _, e, now := harness(t)
	c, _ := NewCartControl(v, e.Identity.DeploymentID, e.Identity.MemberDigest)
	c.now = func() time.Time { return time.UnixMilli(now.Load()) }
	i := CartIntent{Identity: e.Identity, ExecutionID: "old", OperationKey: "old-key", InputDigest: digest("draft"), Kind: "cart_add", DeadlineMS: now.Load() + 500, PickupAtMS: now.Load() + 1000}
	if _, dispatch, err := c.Begin(t.Context(), i); err != nil || !dispatch {
		t.Fatal(err)
	}
	if _, err := c.Retire(t.Context(), e.Identity, i.OperationKey, i.InputDigest); !errors.Is(err, ErrConflict) {
		t.Fatal("future meal retired", err)
	}
	now.Add(1001)
	other := e.Identity
	other.OwnerID = "foreign"
	if _, err := c.Retire(t.Context(), other, i.OperationKey, i.InputDigest); !errors.Is(err, ErrScope) {
		t.Fatal("foreign owner retired", err)
	}
	receipt, err := c.Retire(t.Context(), e.Identity, i.OperationKey, i.InputDigest)
	if err != nil || receipt.State != "unknown" || !receipt.Retired {
		t.Fatal(receipt, err)
	}
	if old, dispatch, err := c.Begin(t.Context(), i); err != nil || dispatch || old != receipt {
		t.Fatal("retired write replayed", err)
	}
	i.ExecutionID = "new"
	i.OperationKey = "new-key"
	i.DeadlineMS = now.Load() + 1000
	i.PickupAtMS = now.Load() + 10000
	if _, dispatch, err := c.Begin(t.Context(), i); err != nil || !dispatch {
		t.Fatal("explicit new intent blocked", err)
	}
}
