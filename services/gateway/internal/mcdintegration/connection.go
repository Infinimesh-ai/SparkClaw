package mcdintegration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

type record struct {
	Version         int                        `json:"version"`
	Identity        Identity                   `json:"identity"`
	ConnectionID    string                     `json:"connection_id"`
	Closed          bool                       `json:"closed"`
	ObservedMS      int64                      `json:"observed_ms"`
	Session         *Envelope                  `json:"session"`
	MaterialPresent bool                       `json:"material_present"`
	Receipts        map[string]Receipt         `json:"receipts"`
	Revocation      *Receipt                   `json:"revocation"`
	Executions      map[string]executionBudget `json:"executions,omitempty"`
}

type executionBudget struct {
	DeadlineMS         int64 `json:"deadline_ms"`
	CredentialRevision int64 `json:"credential_revision"`
	Reserved           int   `json:"reserved"`
}

func (c *Connection) load(ctx context.Context) (record, int64, error) {
	raw, gen, found, err := c.vault.OpenBindingVersion(ctx, c.binding, c.kind)
	defer clear(raw)
	if err != nil || len(raw) > 128<<10 {
		return record{}, 0, ErrUnavailable
	}
	anchor, _, anchored, err := c.vault.OpenBindingVersion(ctx, c.binding+":anchor", c.kind+":anchor")
	defer clear(anchor)
	if err != nil || found != anchored || anchored && string(anchor) != digest(json.RawMessage(raw)) {
		return record{}, 0, ErrUnavailable
	}
	if !found {
		return record{Version: 2, Identity: c.identity, ConnectionID: c.id, Receipts: map[string]Receipt{}}, 0, nil
	}
	var r record
	if len(raw) > 128<<10 || appstream.DecodeStrict(raw, &r) != nil {
		return record{}, 0, ErrUnavailable
	}
	if r.Session != nil && bytes.Equal(r.Session.Material, []byte("null")) {
		r.Session.Material = nil
	}
	if r.Version != 2 || r.Identity != c.identity ||
		r.ConnectionID != c.id || r.Receipts == nil || len(r.Receipts) > 128 || r.ObservedMS < 0 ||
		r.Closed && (r.Revocation == nil || r.MaterialPresent) || r.MaterialPresent && r.Session == nil || len(r.Executions) > 128 {
		return record{}, 0, ErrUnavailable
	}
	if r.Session != nil {
		if len(r.Session.Material) > 0 || r.Session.Identity != c.identity || r.Session.ConnectionID != c.id ||
			r.Session.CredentialRevision < 1 || r.Session.GrantRevision < 1 || !hashPattern.MatchString(r.Session.MaterialDigest) {
			clear(r.Session.Material)
			return record{}, 0, ErrUnavailable
		}
	}
	return r, gen, nil
}

func (c *Connection) save(ctx context.Context, r record, gen int64) error {
	return c.checkpoint(ctx, r, gen, nil)
}

func (c *Connection) checkpoint(ctx context.Context, r record, gen int64, material []byte) error {
	if r.Session != nil && len(r.Session.Material) > 0 {
		return ErrInvalid
	}
	raw, err := json.Marshal(r)
	defer clear(raw)
	if err != nil || len(raw) > 128<<10 {
		return ErrUnavailable
	}
	previous, current, found, err := c.vault.OpenBindingVersion(ctx, c.binding, c.kind)
	defer clear(previous)
	anchor, anchorGen, anchored, anchorErr := c.vault.OpenBindingVersion(ctx, c.binding+":anchor", c.kind+":anchor")
	defer clear(anchor)
	if err != nil || anchorErr != nil || current != gen || found != (gen != 0) || found != anchored ||
		found && (len(previous) > 128<<10 || string(anchor) != digest(json.RawMessage(previous))) {
		return ErrConflict
	}
	// Claim the new record digest before replacing material/control. The
	// independent high-water anchor rejects a missing or restored older record.
	// Torn/uncertain writes are unreadable unless the exact new record committed;
	// they require explicit recovery, never automatic old-credential fallback.
	if c.vault.CompareAndReplaceBinding(ctx, c.binding+":anchor", c.kind+":anchor", []byte(digest(json.RawMessage(raw))), anchorGen) != nil {
		return ErrConflict
	}
	if material != nil {
		// Only the winning anchor claimant can rotate material. Until the
		// matching control checkpoint commits, every reader fails closed.
		if c.vault.ReplaceBinding(ctx, c.binding+":material", c.kind+":material", material) != nil {
			return ErrConflict
		}
	} else if !r.MaterialPresent {
		// Delete while the claimed anchor blocks renewal. Deleting after the
		// control commit could race with renewal and erase its new material.
		if c.clearMaterial(ctx) != nil {
			return ErrConflict
		}
	}
	if c.vault.CompareAndReplaceBinding(ctx, c.binding, c.kind, raw, gen) != nil {
		return ErrConflict
	}
	return nil
}

func (c *Connection) Provision(ctx context.Context, raw []byte) (Receipt, error) {
	e, err := DecodeEnvelope(raw)
	if err != nil {
		return Receipt{}, err
	}
	defer clear(e.Material)
	if e.Identity != c.identity || e.ConnectionID != c.id {
		return Receipt{}, ErrScope
	}
	r, gen, err := c.load(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if r.Session != nil {
		defer clear(r.Session.Material)
	}
	if original, ok := r.Receipts[e.ProvisionRequestID]; ok {
		if original.Digest != e.EnvelopeDigest {
			return Receipt{}, ErrConflict
		}
		return original, nil
	}
	if r.Closed {
		return Receipt{}, ErrClosed
	}
	now := c.now().UnixMilli()
	if now < r.ObservedMS || now < e.IssuedAtMS || now >= e.SessionNotAfterMS {
		return Receipt{}, ErrExpired
	}
	if len(r.Receipts) >= 128 {
		return Receipt{}, ErrUnavailable
	}
	invalidate := false
	if old := r.Session; old != nil {
		// The credential digest changes iff its revision changes. The grant
		// revision advances for every new lease/scope, including rotation.
		if e.CredentialRevision < old.CredentialRevision || e.GrantRevision <= old.GrantRevision ||
			(e.MaterialDigest == old.MaterialDigest) != (e.CredentialRevision == old.CredentialRevision) ||
			e.IssuedAtMS < old.IssuedAtMS {
			return Receipt{}, ErrConflict
		}
		invalidate = e.MaterialDigest != old.MaterialDigest || e.MaxRequests < old.MaxRequests
		for _, op := range old.Operations {
			invalidate = invalidate || !slices.Contains(e.Operations, op)
		}
	}
	// Provider health runs without a Vault lock. A racing revoke wins by CAS;
	// the health result can never re-open a closed connection.
	checkCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(min(e.SessionNotAfterMS, now+90000)))
	defer cancel()
	if err = c.runner.Validate(checkCtx, e); err != nil {
		return Receipt{}, ErrUnavailable
	}
	checked := c.now().UnixMilli()
	if checked < now || checked >= e.SessionNotAfterMS || checkCtx.Err() != nil {
		return Receipt{}, ErrExpired
	}
	receipt := Receipt{RequestID: e.ProvisionRequestID, Digest: e.EnvelopeDigest, ConnectionID: c.id,
		CredentialRevision: e.CredentialRevision, GrantRevision: e.GrantRevision, NotAfterMS: e.SessionNotAfterMS, Disposition: "accepted"}
	metadata := e
	metadata.Material = nil
	r.Session = &metadata
	r.MaterialPresent = true
	r.ObservedMS = checked
	r.Receipts[e.ProvisionRequestID] = receipt
	if err = c.checkpoint(ctx, r, gen, e.Material); err != nil {
		return Receipt{}, err
	}
	// Unchanged-credential lease extension need not invalidate work. Rotation,
	// removal of operations and budget narrowing cancel local in-flight reads.
	if invalidate {
		c.cancelReads()
	}
	return receipt, nil
}

func (c *Connection) Revoke(ctx context.Context, requestID string) (Receipt, error) {
	if !idPattern.MatchString(requestID) {
		return Receipt{}, ErrInvalid
	}
	r, gen, err := c.load(ctx)
	if err != nil {
		return Receipt{}, err
	}
	if r.Session != nil {
		defer clear(r.Session.Material)
	}
	if r.Closed {
		c.cancelReads()
		if c.clearMaterial(ctx) != nil {
			return Receipt{}, ErrUnavailable
		}
		return *r.Revocation, nil
	}
	if _, ok := r.Receipts[requestID]; ok {
		return Receipt{}, ErrConflict
	}
	receipt := Receipt{RequestID: requestID, Digest: digest([]any{c.identity, c.id, requestID, "revoke"}), ConnectionID: c.id, Disposition: "revoked"}
	r.Closed = true
	r.MaterialPresent = false
	r.Revocation = &receipt
	if r.Session != nil {
		r.Session.Material = nil
	}
	r.ObservedMS = max(r.ObservedMS, c.now().UnixMilli())
	c.cancelReads()
	if err = c.save(ctx, r, gen); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (c *Connection) clearMaterial(ctx context.Context) error {
	if c.vault.DeleteBinding(ctx, c.binding+":material", c.kind+":material") != nil {
		return ErrUnavailable
	}
	return nil
}

func (c *Connection) cancelReads() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.active {
		cancel()
	}
}

// Expire removes material, retaining the high-water marks and original
// receipts. An expired connection may only be renewed by a newer Mac grant.
func (c *Connection) Expire(ctx context.Context) error {
	r, gen, err := c.load(ctx)
	if err != nil {
		return err
	}
	if r.Session == nil {
		return nil
	}
	defer clear(r.Session.Material)
	if c.now().UnixMilli() < r.ObservedMS {
		return ErrExpired
	}
	if c.now().UnixMilli() < r.Session.SessionNotAfterMS {
		return nil
	}
	if r.MaterialPresent {
		r.MaterialPresent = false
		if err = c.save(ctx, r, gen); err != nil {
			return err
		}
	}
	c.cancelReads()
	return nil
}

func (c *Connection) Read(ctx context.Context, x Execution, op string, args json.RawMessage) (json.RawMessage, error) {
	if !readOperations[op] {
		return nil, ErrScope
	}
	return c.invoke(ctx, x, op, args, func(ctx context.Context, e Envelope) (json.RawMessage, error) {
		return c.runner.Read(ctx, e, x, op, args)
	})
}

func (c *Connection) invoke(ctx context.Context, x Execution, op string, args json.RawMessage, run func(context.Context, Envelope) (json.RawMessage, error)) (json.RawMessage, error) {
	ctx, cancelRead := context.WithCancel(ctx)
	key := new(int)
	c.mu.Lock()
	c.active[key] = cancelRead
	c.mu.Unlock()
	defer func() { cancelRead(); c.mu.Lock(); delete(c.active, key); c.mu.Unlock() }()
	now := c.now().UnixMilli()
	if x.Identity != c.identity || x.ConnectionID != c.id || !idPattern.MatchString(x.ExecutionID) ||
		x.DeadlineMS <= now || x.DeadlineMS-now > 900000 || x.MaxRequests < 1 || x.MaxRequests > 128 || len(args) > 16<<10 {
		return nil, ErrScope
	}
	if _, err := Canonical(args); err != nil {
		return nil, ErrInvalid
	}
	r, gen, err := c.load(ctx)
	if err != nil {
		return nil, err
	}
	if r.Session != nil {
		defer clear(r.Session.Material)
	}
	if r.Closed {
		return nil, ErrClosed
	}
	e := r.Session
	if e == nil || !r.MaterialPresent {
		return nil, ErrUnavailable
	}
	if now < r.ObservedMS || now < e.IssuedAtMS || now >= e.SessionNotAfterMS {
		if now >= e.SessionNotAfterMS {
			if err = c.Expire(ctx); err != nil {
				return nil, err
			}
		}
		return nil, ErrExpired
	}
	if x.CredentialRevision != e.CredentialRevision || x.GrantRevision < 1 || x.GrantRevision > e.GrantRevision ||
		!slices.Contains(e.Operations, op) || x.MaxRequests > e.MaxRequests {
		return nil, ErrScope
	}
	// Reserve the helper's entire maximum before dispatch, never refund an
	// uncertain call. Restarting or parallel calls cannot reset this execution's
	// aggregate network budget. Only expired execution entries may be pruned.
	if r.Executions == nil {
		r.Executions = map[string]executionBudget{}
	}
	for id, b := range r.Executions {
		if now >= b.DeadlineMS {
			delete(r.Executions, id)
		}
	}
	b, exists := r.Executions[x.ExecutionID]
	if exists && (b.DeadlineMS != x.DeadlineMS || b.CredentialRevision != x.CredentialRevision) {
		return nil, ErrScope
	}
	if !exists && len(r.Executions) >= 128 || b.Reserved+x.MaxRequests > e.MaxRequests {
		return nil, ErrBudget
	}
	r.Executions[x.ExecutionID] = executionBudget{x.DeadlineMS, x.CredentialRevision, b.Reserved + x.MaxRequests}
	// Persist observed wall time before dispatch to detect rollback on restart.
	r.ObservedMS = now
	if err = c.save(ctx, r, gen); err != nil {
		return nil, err
	}
	// Only now, after durable identity/revision/expiry/authority admission,
	// may the trusted helper's material be decrypted into transient memory.
	material, _, found, err := c.vault.OpenBindingVersion(ctx, c.binding+":material", c.kind+":material")
	defer clear(material)
	if err != nil || !found || len(material) > 48<<10 || digest(json.RawMessage(material)) != e.MaterialDigest {
		return nil, ErrUnavailable
	}
	hydrated := *e
	hydrated.Material = material
	encoded, err := json.Marshal(hydrated)
	if err != nil {
		return nil, ErrUnavailable
	}
	_, err = DecodeEnvelope(encoded)
	clear(encoded)
	if err != nil {
		return nil, ErrUnavailable
	}
	readCtx, cancel := context.WithDeadline(ctx, time.UnixMilli(min(x.DeadlineMS, e.SessionNotAfterMS, c.now().UnixMilli()+90000)))
	defer cancel()
	result, err := run(readCtx, hydrated)
	if errors.Is(err, ErrCredential) {
		// A late failure of the old credential cannot erase a newer rotation.
		latest, latestGen, loadErr := c.load(ctx)
		if loadErr == nil && latest.Session != nil && latest.MaterialPresent && latest.Session.MaterialDigest == e.MaterialDigest && latest.Session.CredentialRevision == e.CredentialRevision {
			latest.MaterialPresent = false
			if c.save(ctx, latest, latestGen) != nil {
				clear(result)
				return nil, ErrUnavailable
			}
			c.cancelReads()
		}
		clear(result)
		return nil, ErrCredential
	}
	if err != nil || readCtx.Err() != nil {
		clear(result)
		return nil, ErrUnavailable
	}
	// Never deliver a delayed result under a changed member, narrowed grant,
	// revoked connection, expired lease or clock rollback. Lease-only renewal
	// can retain the same credential and allowed operation.
	latest, _, err := c.load(ctx)
	if latest.Session != nil {
		defer clear(latest.Session.Material)
	}
	current := latest.Session
	after := c.now().UnixMilli()
	if err != nil || latest.Closed || current == nil || !latest.MaterialPresent || after < max(now, latest.ObservedMS) ||
		after >= current.SessionNotAfterMS || after >= e.SessionNotAfterMS || current.MaterialDigest != e.MaterialDigest ||
		!slices.Contains(current.Operations, op) || current.MaxRequests < e.MaxRequests {
		clear(result)
		return nil, ErrClosed
	}
	return result, nil
}
