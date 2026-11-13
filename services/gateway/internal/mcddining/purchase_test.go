package mcddining

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"os"
	"strings"
	"testing"
	"time"
)

type diningRunner struct {
	selectionRunner
	quotes  int
	changed bool
}

func hashDining(v any) string {
	raw, _ := json.Marshal(v)
	raw, _ = mcdintegration.Canonical(raw)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (r *diningRunner) Quote(_ context.Context, _ mcdintegration.Envelope, x mcdintegration.Execution, q mcdintegration.QuoteRequest, _ string) (json.RawMessage, error) {
	r.quotes++
	amount := 1490
	if r.changed && r.quotes > 1 {
		amount = 1400
	}
	fields := map[string]any{"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": "fixture", "scope": mcdintegration.QuoteScope{Identity: x.Identity, CredentialRevision: x.CredentialRevision, ExecutionID: x.ExecutionID}, "source_quote_ref": "source", "purchase_id": q.PurchaseID, "quote_revision": q.QuoteRevision, "handoff_epoch": q.HandoffEpoch, "generated_at_ms": time.Now().UnixMilli(), "expires_at_ms": time.Now().UnixMilli() + 119000, "currency": "CNY", "payment_method": "ARCHCARD", "cart_digest": strings.Repeat("a", 64), "quote": map[string]any{"store_code": "3330613", "store_name": "Fixture store", "pickup_at": "2026-11-14T20:00:00+08:00", "eat_type": "locker-in", "items": []any{map[string]any{"name": "meal"}}, "discount_names": []any{}, "amount_cents": amount}, "pickup_receipt": nil, "purchase_digest": hashDining(amount)}
	fields["envelope_digest"] = hashDining(fields)
	return json.Marshal(fields)
}

type diningHost struct {
	commits, aborts int
	unknown         bool
}

func (h *diningHost) Prepare(_ context.Context, raw json.RawMessage) (map[string]any, error) {
	var e mcdintegration.QuoteEnvelope
	json.Unmarshal(raw, &e)
	return map[string]any{"state": "prepared", "purchase_digest": e.PurchaseDigest}, nil
}
func (h *diningHost) Abort(_ context.Context, _ mcdintegration.QuoteScope, p string, e int64, r string) (map[string]any, error) {
	h.aborts++
	return map[string]any{"state": "aborted_before_write", "purchase_id": p, "handoff_epoch": e, "reason": r}, nil
}
func (h *diningHost) Commit(_ context.Context, _ mcdintegration.QuoteScope, q mcdintegration.Handoff, approval string, review map[string]any) (map[string]any, error) {
	h.commits++
	if h.unknown {
		return nil, errors.New("lost response")
	}
	return map[string]any{"state": "PAID", "purchase_id": q.PurchaseID, "handoff_epoch": q.Epoch, "terminal_verified": true, "result_json": "{}"}, nil
}
func (h *diningHost) Status(context.Context, mcdintegration.QuoteScope, string) (map[string]any, error) {
	return map[string]any{}, errors.New("offline")
}
func (h *diningHost) Orders(context.Context, mcdintegration.QuoteScope) (map[string]any, error) {
	return map[string]any{"orders": []any{}}, nil
}
func diningFixture(t *testing.T) (*Selection, *diningRunner, *diningHost) {
	t.Helper()
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	json.Unmarshal(raw, &fields)
	fields["issued_at_ms"] = time.Now().UnixMilli() - 1000
	fields["session_not_after_ms"] = time.Now().UnixMilli() + 900000
	fields["operations"] = []string{"checkout_quote"}
	fields["max_requests"] = 128
	delete(fields, "envelope_digest")
	fields["envelope_digest"] = hashDining(fields)
	raw, _ = json.Marshal(fields)
	e, err := mcdintegration.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	r := &diningRunner{}
	m, err := mcdintegration.NewManager(credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("42", 32)}), r, e.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	i := e.Identity
	p := mcdintegration.Principal{DeploymentID: i.DeploymentID, OwnerID: i.OwnerID, ClientID: i.ClientID, InstallationID: i.InstallationID}
	if _, err = m.Provision(t.Context(), p, raw); err != nil {
		t.Fatal(err)
	}
	s, err := NewSelection(t.Context(), m, p, "execution")
	if err != nil {
		t.Fatal(err)
	}
	h := &diningHost{}
	s.WithCart(t.TempDir(), h).WithPurchase(true)
	if _, err = s.Quote(t.Context(), []byte(`{"operation":"checkout_quote","store_code":"3330613","pickup_at":"2026-11-14T20:00:00+08:00","eat_type":"locker-in","max_amount_cny":"20.00"}`)); err != nil {
		t.Fatal(err)
	}
	return s, r, h
}
func TestPurchaseArgumentsAreFrozenAndUnknownCommitNeverReplays(t *testing.T) {
	s, _, h := diningFixture(t)
	args, err := s.PurchaseArguments()
	if err != nil {
		t.Fatal(err)
	}
	args["amount_cents"] = float64(1)
	if _, err = s.Commit(t.Context(), "approval", args); err == nil || h.commits != 0 {
		t.Fatal("drift purchased")
	}
	args, _ = s.PurchaseArguments()
	h.unknown = true
	if _, err = s.Commit(t.Context(), "approval", args); err == nil {
		t.Fatal("lost reply treated as success")
	}
	s.Close(t.Context())
	if h.aborts != 0 {
		t.Fatal("unknown commit aborted")
	}
	if _, err = s.Commit(t.Context(), "approval", args); err == nil || h.commits != 1 {
		t.Fatal("unknown commit replayed")
	}
}
func TestQuoteRefreshReusesOnlyIdenticalContentAndOriginalPurchase(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "identical", true: "changed_lower_price"}[changed], func(t *testing.T) {
			s, r, h := diningFixture(t)
			r.changed = changed
			args, _ := s.PurchaseArguments()
			purchase := s.cart.purchase
			s.cart.handoff.DeadlineMS = 0
			result, err := s.Commit(t.Context(), "original-approval", args)
			if changed {
				if err == nil || h.commits != 0 {
					t.Fatal("changed meal reused approval", result, err)
				}
			} else if err != nil || result["state"] != "PAID" || h.commits != 1 {
				t.Fatal(result, err)
			}
			if r.quotes != 2 || h.aborts != 1 || s.cart.purchase != purchase {
				t.Fatal("refresh did not retain original purchase", r.quotes, h.aborts)
			}
		})
	}
}
