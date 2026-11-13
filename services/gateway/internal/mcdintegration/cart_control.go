package mcdintegration

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

// CartControl is member-scoped across aliases, workbenches and conversations.
// It stores only control metadata. Full drafts/quotes belong to the execution
// workspace and bounded delivery store, never this durable fence.
type CartControl struct {
	vault                   Vault
	deployment, member, key string
	now                     func() time.Time
}
type CartIntent struct {
	Identity     Identity `json:"identity"`
	ExecutionID  string   `json:"execution_id"`
	OperationKey string   `json:"operation_key"`
	InputDigest  string   `json:"input_digest"`
	Kind         string   `json:"kind"`
	DeadlineMS   int64    `json:"deadline_ms"`
	PickupAtMS   int64    `json:"pickup_at_ms,omitempty"`
}
type CartReceipt struct {
	OperationKey   string `json:"operation_key"`
	InputDigest    string `json:"input_digest"`
	ScopeDigest    string `json:"scope_digest"`
	State          string `json:"state"`
	EvidenceDigest string `json:"evidence_digest,omitempty"`
	Kind           string `json:"kind,omitempty"`
	RecoveryScope  string `json:"recovery_scope,omitempty"`
	PickupAtMS     int64  `json:"pickup_at_ms,omitempty"`
	Retired        bool   `json:"retired,omitempty"`
}
type Handoff struct {
	Identity           Identity `json:"identity"`
	ExecutionID        string   `json:"execution_id"`
	CredentialRevision int64    `json:"credential_revision"`
	ScopeDigest        string   `json:"scope_digest"`
	PurchaseID         string   `json:"purchase_id"`
	Epoch              int64    `json:"epoch"`
	QuoteRevision      int64    `json:"quote_revision"`
	PurchaseDigest     string   `json:"purchase_digest"`
	EnvelopeDigest     string   `json:"envelope_digest"`
	DeadlineMS         int64    `json:"deadline_ms"`
	State              string   `json:"state"`
	ReceiptDigest      string   `json:"receipt_digest,omitempty"`
	AbortReason        string   `json:"abort_reason,omitempty"`
}
type cartRecord struct {
	Version    int                    `json:"version"`
	ObservedMS int64                  `json:"observed_ms"`
	Epoch      int64                  `json:"epoch"`
	Active     string                 `json:"active,omitempty"`
	Operations map[string]CartReceipt `json:"operations"`
	Handoff    *Handoff               `json:"handoff,omitempty"`
	Refreshes  map[string]int         `json:"refreshes"`
	Revisions  map[string]int64       `json:"revisions"`
	Closed     map[string]bool        `json:"closed"`
}

func NewCartControl(v Vault, deployment, member string) (*CartControl, error) {
	if v == nil || !idPattern.MatchString(deployment) || !hashPattern.MatchString(member) {
		return nil, ErrInvalid
	}
	return &CartControl{v, deployment, member, "mcd-cart-control:" + digest([]string{deployment, member}), time.Now}, nil
}
func (c *CartControl) load(ctx context.Context) (cartRecord, int64, int64, error) {
	raw, gen, found, err := c.vault.OpenBindingVersion(ctx, c.key, "mcd-cart-control-v1")
	defer clear(raw)
	anchor, anchorGen, anchored, anchorErr := c.vault.OpenBindingVersion(ctx, c.key+":anchor", "mcd-cart-anchor-v1")
	defer clear(anchor)
	if err != nil || anchorErr != nil || found != anchored || len(raw) > 512<<10 || found && string(anchor) != digest(json.RawMessage(raw)) {
		return cartRecord{}, 0, 0, ErrUnavailable
	}
	r := cartRecord{Version: 1, Operations: map[string]CartReceipt{}, Refreshes: map[string]int{}, Revisions: map[string]int64{}, Closed: map[string]bool{}}
	if found && (appstream.DecodeStrict(raw, &r) != nil || r.Version != 1 || r.Operations == nil || r.Refreshes == nil || r.Revisions == nil || r.Closed == nil || len(r.Operations) > 512 || r.Epoch < 0 || r.Epoch > 1<<53-1) {
		return cartRecord{}, 0, 0, ErrUnavailable
	}
	if c.now().UnixMilli() < r.ObservedMS {
		return cartRecord{}, 0, 0, ErrExpired
	}
	if r.Active != "" {
		receipt, ok := r.Operations[r.Active]
		if !ok || receipt.State != "unknown" {
			return cartRecord{}, 0, 0, ErrUnavailable
		}
	}
	return r, gen, anchorGen, nil
}
func (c *CartControl) save(ctx context.Context, r cartRecord, gen, anchorGen int64) error {
	r.ObservedMS = c.now().UnixMilli()
	raw, err := json.Marshal(r)
	defer clear(raw)
	if err != nil || len(raw) > 512<<10 {
		return ErrUnavailable
	}
	if c.vault.CompareAndReplaceBinding(ctx, c.key+":anchor", "mcd-cart-anchor-v1", []byte(digest(json.RawMessage(raw))), anchorGen) != nil {
		return ErrConflict
	}
	if c.vault.CompareAndReplaceBinding(ctx, c.key, "mcd-cart-control-v1", raw, gen) != nil {
		return ErrConflict
	}
	return nil
}
func (c *CartControl) validate(i CartIntent) error {
	if !i.Identity.valid() || i.Identity.DeploymentID != c.deployment || i.Identity.MemberDigest != c.member || !idPattern.MatchString(i.ExecutionID) || !idPattern.MatchString(i.OperationKey) || !hashPattern.MatchString(i.InputDigest) {
		return ErrScope
	}
	switch i.Kind {
	case "cart_add", "cart_add_configured", "cart_clear", "pickup_select", "quote_export":
	default:
		return ErrInvalid
	}
	return nil
}
func cartScope(i CartIntent) string { return digest([]any{i.Identity, i.ExecutionID}) }
func cartRecoveryScope(i Identity) string {
	return digest([]string{i.DeploymentID, i.OwnerID, i.ClientID, i.InstallationID, i.MemberDigest})
}

// Begin returns dispatch=true only for the caller that durably won the claim.
// Reusing an original key can retrieve its outcome but can never write again.
func (c *CartControl) Begin(ctx context.Context, i CartIntent) (receipt CartReceipt, dispatch bool, err error) {
	if err = c.validate(i); err != nil {
		return
	}
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return CartReceipt{}, false, err
	}
	if old, ok := r.Operations[i.OperationKey]; ok {
		if old.InputDigest != i.InputDigest || old.ScopeDigest != cartScope(i) {
			return CartReceipt{}, false, ErrConflict
		}
		return old, false, nil
	}
	if i.DeadlineMS <= c.now().UnixMilli() || i.DeadlineMS-c.now().UnixMilli() > 900000 {
		return CartReceipt{}, false, ErrExpired
	}
	if r.Active != "" || r.Handoff != nil {
		return CartReceipt{}, false, ErrConflict
	}
	if len(r.Operations) >= 512 {
		return CartReceipt{}, false, ErrUnavailable
	}
	receipt = CartReceipt{OperationKey: i.OperationKey, InputDigest: i.InputDigest, ScopeDigest: cartScope(i), State: "unknown", Kind: i.Kind, RecoveryScope: cartRecoveryScope(i.Identity), PickupAtMS: i.PickupAtMS}
	r.Operations[i.OperationKey] = receipt
	r.Active = i.OperationKey
	err = c.save(ctx, r, gen, anchorGen)
	return receipt, err == nil, err
}

// Retire abandons only an expired non-purchase selection intent. Preserve the
// original unknown outcome and key; this is never proof that a write failed.
func (c *CartControl) Retire(ctx context.Context, i Identity, key, inputDigest string) (CartReceipt, error) {
	if !i.valid() || i.DeploymentID != c.deployment || i.MemberDigest != c.member {
		return CartReceipt{}, ErrScope
	}
	r, gen, anchor, err := c.load(ctx)
	if err != nil {
		return CartReceipt{}, err
	}
	receipt, ok := r.Operations[key]
	if !ok || receipt.RecoveryScope != cartRecoveryScope(i) || receipt.InputDigest != inputDigest || receipt.Kind == "quote_export" {
		return CartReceipt{}, ErrScope
	}
	if receipt.Retired {
		return receipt, nil
	}
	if receipt.State != "unknown" || r.Active != key || r.Handoff != nil || receipt.PickupAtMS <= 0 || receipt.PickupAtMS > c.now().UnixMilli() {
		return CartReceipt{}, ErrConflict
	}
	receipt.Retired = true
	r.Operations[key] = receipt
	r.Active = ""
	return receipt, c.save(ctx, r, gen, anchor)
}
func (c *CartControl) Pending(ctx context.Context, i Identity) ([]CartReceipt, error) {
	if !i.valid() || i.DeploymentID != c.deployment || i.MemberDigest != c.member {
		return nil, ErrScope
	}
	r, _, _, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	rows := []CartReceipt{}
	if receipt, ok := r.Operations[r.Active]; ok && receipt.RecoveryScope == cartRecoveryScope(i) {
		rows = append(rows, receipt)
	}
	return rows, nil
}
func (c *CartControl) ConfirmStatus(ctx context.Context, i Identity, executionID, providerID, evidence string) error {
	r, _, _, err := c.load(ctx)
	if err != nil {
		return err
	}
	receipt, ok := r.Operations[r.Active]
	if !ok {
		return nil
	}
	intent := CartIntent{Identity: i, ExecutionID: executionID, OperationKey: receipt.OperationKey, InputDigest: receipt.InputDigest, Kind: receipt.Kind}
	if receipt.ScopeDigest != cartScope(intent) {
		return ErrScope
	}
	prefix := "cart-"
	if receipt.Kind == "pickup_select" {
		prefix = "pickup-"
	}
	if providerID != prefix+digest(map[string]any{"identity": i.MemberDigest, "key": receipt.OperationKey}) {
		return ErrScope
	}
	_, err = c.Confirm(ctx, intent, evidence)
	return err
}

// Confirm is called only after the trusted provider verifies the current cart
// against the frozen draft. Current equality never permits replaying the write.
func (c *CartControl) Confirm(ctx context.Context, i CartIntent, evidenceDigest string) (CartReceipt, error) {
	return c.resolve(ctx, i, evidenceDigest, "confirmed")
}

// NotDispatched accepts only the trusted helper's explicit pre-write proof.
// Missing/failed transport responses must remain unknown.
func (c *CartControl) NotDispatched(ctx context.Context, i CartIntent, evidenceDigest string) (CartReceipt, error) {
	return c.resolve(ctx, i, evidenceDigest, "failed_before_write")
}
func (c *CartControl) resolve(ctx context.Context, i CartIntent, evidenceDigest, state string) (CartReceipt, error) {
	if c.validate(i) != nil || !hashPattern.MatchString(evidenceDigest) {
		return CartReceipt{}, ErrInvalid
	}
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return CartReceipt{}, err
	}
	receipt, ok := r.Operations[i.OperationKey]
	if !ok || receipt.InputDigest != i.InputDigest || receipt.ScopeDigest != cartScope(i) {
		return CartReceipt{}, ErrScope
	}
	if receipt.State != "unknown" {
		if receipt.State != state || receipt.EvidenceDigest != evidenceDigest {
			return CartReceipt{}, ErrConflict
		}
		return receipt, nil
	}
	if r.Active != i.OperationKey {
		return CartReceipt{}, ErrConflict
	}
	receipt.State = state
	receipt.EvidenceDigest = evidenceDigest
	r.Operations[i.OperationKey] = receipt
	r.Active = ""
	return receipt, c.save(ctx, r, gen, anchorGen)
}
func (c *CartControl) Freeze(ctx context.Context, i Identity, executionID string, h Handoff) (Handoff, error) {
	return c.freeze(ctx, i, executionID, h, nil)
}
func (c *CartControl) FreezeQuote(ctx context.Context, intent CartIntent, h Handoff) (Handoff, error) {
	if c.validate(intent) != nil || intent.Kind != "quote_export" {
		return Handoff{}, ErrInvalid
	}
	return c.freeze(ctx, intent.Identity, intent.ExecutionID, h, &intent)
}
func (c *CartControl) NextQuoteEpoch(ctx context.Context, intent CartIntent) (int64, error) {
	r, _, _, err := c.load(ctx)
	if err != nil {
		return 0, err
	}
	receipt, ok := r.Operations[intent.OperationKey]
	if !ok || r.Active != intent.OperationKey || receipt.State != "unknown" || receipt.InputDigest != intent.InputDigest || receipt.ScopeDigest != cartScope(intent) {
		return 0, ErrConflict
	}
	return r.Epoch + 1, nil
}
func (c *CartControl) freeze(ctx context.Context, i Identity, executionID string, h Handoff, intent *CartIntent) (Handoff, error) {
	if !i.valid() || i.DeploymentID != c.deployment || i.MemberDigest != c.member || !idPattern.MatchString(executionID) || !idPattern.MatchString(h.PurchaseID) || !hashPattern.MatchString(h.PurchaseDigest) || !hashPattern.MatchString(h.EnvelopeDigest) || h.QuoteRevision < 1 || h.QuoteRevision > 1<<53-1 || h.Epoch < 1 || h.Epoch > 1<<53-1 {
		return Handoff{}, ErrInvalid
	}
	h.ScopeDigest = digest([]any{i, executionID})
	h.State = "frozen"
	h.ReceiptDigest = ""
	h.AbortReason = ""
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return Handoff{}, err
	}
	if r.Handoff != nil {
		if *r.Handoff == h {
			return h, nil
		}
		return Handoff{}, ErrConflict
	}
	if intent != nil {
		receipt, ok := r.Operations[intent.OperationKey]
		if !ok || r.Active != intent.OperationKey || receipt.State != "unknown" || receipt.InputDigest != intent.InputDigest || receipt.ScopeDigest != cartScope(*intent) {
			return Handoff{}, ErrConflict
		}
		receipt.State = "confirmed"
		receipt.EvidenceDigest = h.EnvelopeDigest
		r.Operations[intent.OperationKey] = receipt
		r.Active = ""
	}
	if r.Active != "" || h.Epoch != r.Epoch+1 || h.DeadlineMS <= c.now().UnixMilli() || h.DeadlineMS-c.now().UnixMilli() > 120000 || r.Closed[h.PurchaseID] || h.QuoteRevision <= r.Revisions[h.PurchaseID] {
		return Handoff{}, ErrConflict
	}
	if r.Revisions[h.PurchaseID] > 0 {
		if r.Refreshes[h.PurchaseID] >= 2 {
			return Handoff{}, ErrBudget
		}
		r.Refreshes[h.PurchaseID]++
	} else if h.QuoteRevision != 1 || len(r.Revisions) >= 512 {
		return Handoff{}, ErrConflict
	} else {
		r.Refreshes[h.PurchaseID] = 0
	}
	r.Revisions[h.PurchaseID] = h.QuoteRevision
	r.Epoch = h.Epoch
	r.Handoff = &h
	return h, c.save(ctx, r, gen, anchorGen)
}
func (c *CartControl) RequestAbort(ctx context.Context, scope, purchase string, epoch int64, reason string) (Handoff, error) {
	if reason != "quote_refresh" && reason != "user_abandonment" && reason != "execution_termination" {
		return Handoff{}, ErrInvalid
	}
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return Handoff{}, err
	}
	h := r.Handoff
	if h == nil || h.ScopeDigest != scope || h.PurchaseID != purchase || h.Epoch != epoch {
		return Handoff{}, ErrScope
	}
	if h.State == "abort_requested" {
		if h.AbortReason != reason {
			return Handoff{}, ErrConflict
		}
		return *h, nil
	}
	h.State = "abort_requested"
	h.AbortReason = reason
	return *h, c.save(ctx, r, gen, anchorGen)
}

// FinishAbort requires an authenticated Mac aborted_before_write receipt. The
// caller owns channel verification; timeout and not_found never call this.
func (c *CartControl) FinishAbort(ctx context.Context, scope, purchase string, epoch int64, state, receiptDigest string) error {
	if state != "aborted_before_write" || !hashPattern.MatchString(receiptDigest) {
		return ErrInvalid
	}
	r, gen, anchorGen, err := c.load(ctx)
	if err != nil {
		return err
	}
	key := "abort-" + digest([]any{scope, purchase, epoch})
	if old, exists := r.Operations[key]; exists {
		if old.State == state && old.EvidenceDigest == receiptDigest && old.ScopeDigest == scope {
			return nil
		}
		return ErrConflict
	}
	h := r.Handoff
	if h == nil || h.ScopeDigest != scope || h.PurchaseID != purchase || h.Epoch != epoch || h.State != "abort_requested" {
		return ErrScope
	}
	// Preserve the original epoch's negative receipt in the permanent journal.
	if len(r.Operations) >= 512 {
		return ErrUnavailable
	}
	r.Operations[key] = CartReceipt{OperationKey: key, InputDigest: h.EnvelopeDigest, ScopeDigest: scope, State: state, EvidenceDigest: receiptDigest}
	if h.AbortReason != "quote_refresh" {
		r.Closed[purchase] = true
	}
	r.Handoff = nil
	return c.save(ctx, r, gen, anchorGen)
}
