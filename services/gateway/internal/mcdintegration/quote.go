package mcdintegration

import (
	"context"
	"encoding/json"
	"path/filepath"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

type QuoteRequest struct {
	Request        json.RawMessage `json:"request"`
	PurchaseID     string          `json:"purchase_id"`
	QuoteRevision  int64           `json:"quote_revision"`
	HandoffEpoch   int64           `json:"handoff_epoch"`
	SourceQuoteRef string          `json:"source_quote_ref"`
}
type QuoteRunner interface {
	Quote(context.Context, Envelope, Execution, QuoteRequest, string) (json.RawMessage, error)
}
type QuoteScope struct {
	Identity
	CredentialRevision int64  `json:"credential_revision"`
	ExecutionID        string `json:"execution_id"`
}
type QuoteEnvelope struct {
	SchemaVersion    int             `json:"schema_version"`
	Canonicalization string          `json:"canonicalization"`
	AdapterRevision  string          `json:"adapter_revision"`
	Scope            QuoteScope      `json:"scope"`
	SourceQuoteRef   string          `json:"source_quote_ref"`
	PurchaseID       string          `json:"purchase_id"`
	QuoteRevision    int64           `json:"quote_revision"`
	HandoffEpoch     int64           `json:"handoff_epoch"`
	GeneratedAtMS    int64           `json:"generated_at_ms"`
	ExpiresAtMS      int64           `json:"expires_at_ms"`
	Currency         string          `json:"currency"`
	PaymentMethod    string          `json:"payment_method"`
	CartDigest       string          `json:"cart_digest"`
	Quote            json.RawMessage `json:"quote"`
	PickupReceipt    json.RawMessage `json:"pickup_receipt"`
	PurchaseDigest   string          `json:"purchase_digest"`
	EnvelopeDigest   string          `json:"envelope_digest"`
}

func decodeQuote(raw []byte, x Execution, q QuoteRequest, now int64) (QuoteEnvelope, error) {
	var e QuoteEnvelope
	if len(raw) > 256<<10 || appstream.DecodeStrict(raw, &e) != nil {
		return e, ErrInvalid
	}
	if _, err := Canonical(raw); err != nil {
		return e, ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return e, ErrInvalid
	}
	delete(fields, "envelope_digest")
	if e.SchemaVersion != 1 || e.Canonicalization != "mcd-json-v1" || e.Scope != (QuoteScope{x.Identity, x.CredentialRevision, x.ExecutionID}) || e.PurchaseID != q.PurchaseID || e.QuoteRevision != q.QuoteRevision || e.HandoffEpoch != q.HandoffEpoch || e.Currency != "CNY" || e.PaymentMethod != "ARCHCARD" || !hashPattern.MatchString(e.PurchaseDigest) || !hashPattern.MatchString(e.CartDigest) || digest(fields) != e.EnvelopeDigest || e.GeneratedAtMS > now || e.ExpiresAtMS <= now || e.ExpiresAtMS-e.GeneratedAtMS > 120000 {
		return e, ErrInvalid
	}
	return e, nil
}

// Quote claims the member before any read, so another task cannot change the
// cart between quote generation and the atomic handoff freeze. Full bodies are
// returned only to the owned workflow's transient memory and private Mac route.
func (m *Manager) Quote(ctx context.Context, p Principal, x Execution, q QuoteRequest, root string) (json.RawMessage, Handoff, error) {
	if !filepath.IsAbs(root) || !idPattern.MatchString(q.PurchaseID) || q.QuoteRevision < 1 || q.QuoteRevision > 3 || q.HandoffEpoch != 0 || len(q.Request) > 8192 {
		return nil, Handoff{}, ErrInvalid
	}
	runner, ok := m.runner.(QuoteRunner)
	if !ok {
		return nil, Handoff{}, ErrUnavailable
	}
	d, _, err := m.directory(ctx)
	if err != nil {
		return nil, Handoff{}, err
	}
	registered := false
	for _, r := range d.Connections {
		registered = registered || r.Identity == x.Identity && r.ID == x.ConnectionID
	}
	if !registered {
		return nil, Handoff{}, ErrUnavailable
	}
	c, err := m.connection(p, x.Identity, x.ConnectionID)
	if err != nil {
		return nil, Handoff{}, err
	}
	control, _ := NewCartControl(m.vault, m.deployment, x.Identity.MemberDigest)
	control.now = m.now
	input, _ := json.Marshal(q)
	intent := CartIntent{Identity: x.Identity, ExecutionID: x.ExecutionID, OperationKey: "quote-" + digest([]any{x.Identity, x.ExecutionID, q}), InputDigest: digest(q), Kind: "quote_export", DeadlineMS: x.DeadlineMS}
	var handoff Handoff
	output, err := c.invoke(ctx, x, "checkout_quote", input, func(ctx context.Context, e Envelope) (json.RawMessage, error) {
		_, dispatch, err := control.Begin(ctx, intent)
		if err != nil {
			return nil, err
		}
		if !dispatch {
			return nil, ErrConflict
		}
		epoch, err := control.NextQuoteEpoch(ctx, intent)
		if err != nil {
			return nil, err
		}
		q.HandoffEpoch = epoch
		raw, err := runner.Quote(ctx, e, x, q, root)
		if err != nil {
			// This role has no official write endpoints and no Mac dispatch occurs
			// until FreezeQuote commits. Its failed read is proven non-dispatch.
			_, _ = control.NotDispatched(ctx, intent, digest("quote_read_failed_before_handoff"))
			return nil, err
		}
		quote, err := decodeQuote(raw, x, q, m.now().UnixMilli())
		if err != nil {
			clear(raw)
			return nil, err
		}
		handoff, err = control.FreezeQuote(ctx, intent, Handoff{Identity: x.Identity, ExecutionID: x.ExecutionID, CredentialRevision: x.CredentialRevision, PurchaseID: q.PurchaseID, Epoch: epoch, QuoteRevision: q.QuoteRevision, PurchaseDigest: quote.PurchaseDigest, EnvelopeDigest: quote.EnvelopeDigest, DeadlineMS: quote.ExpiresAtMS})
		if err != nil {
			clear(raw)
			return nil, err
		}
		return raw, nil
	})
	return output, handoff, err
}
