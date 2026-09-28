package browsercontrol

import (
	"context"
	"errors"
	"time"
)

type MailObserverBinding struct {
	Provider          string `json:"provider"`
	OwnerScope        string `json:"owner_scope"`
	MailboxID         string `json:"mailbox_id"`
	AccountAddress    string `json:"account_address"`
	BindingGeneration int64  `json:"binding_generation"`
}
type MailObserverLease struct {
	Watches              []MailObserverWatch `json:"watches"`
	SchemaVersion        int                 `json:"schema_version"`
	Epoch                string              `json:"epoch"`
	CredentialGeneration int64               `json:"credential_generation"`
	LeaseMS              int64               `json:"lease_ms"`
}
type MailObserverWatch struct {
	Provider          string `json:"provider"`
	MailboxID         string `json:"mailbox_id"`
	BindingGeneration int64  `json:"binding_generation"`
	WatchEpoch        string `json:"watch_epoch"`
	State             string `json:"state"`
	Sequence          int64  `json:"sequence"`
}
type MailObserverEvent struct {
	Epoch                string    `json:"epoch"`
	Sequence             int64     `json:"sequence"`
	Provider             string    `json:"provider"`
	OwnerScope           string    `json:"owner_scope"`
	MailboxID            string    `json:"mailbox_id"`
	BindingGeneration    int64     `json:"binding_generation"`
	CredentialGeneration int64     `json:"credential_generation"`
	WatchEpoch           string    `json:"watch_epoch"`
	Kind                 string    `json:"kind"`
	Reason               string    `json:"reason"`
	State                string    `json:"state"`
	ObservedAt           time.Time `json:"observed_at"`
}
type MailObserverBatch struct {
	SchemaVersion int                 `json:"schema_version"`
	Epoch         string              `json:"epoch"`
	Events        []MailObserverEvent `json:"events"`
}

type MailObserverTransport interface {
	ReconcileMailObservers(context.Context, []MailObserverBinding) (MailObserverLease, error)
	PollMailObservers(context.Context, MailObserverLease) (MailObserverBatch, error)
	AckMailObservers(context.Context, MailObserverLease, int64) error
}

// Do not hold operations for the lifetime of a poll: token replacement, MCP
// work and Reader must remain available while this local socket is waiting.
func (s *Service) observerRequest(ctx context.Context, action string, lease MailObserverLease, bindings []MailObserverBinding, seq int64, out any) error {
	release, err := s.acquireOperations(ctx, false)
	if err != nil {
		return err
	}
	token, generation, err := func() ([]byte, int64, error) {
		defer release()
		if s.vault == nil || s.vault.Ready() != nil {
			return nil, 0, newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
		}
		token, generation, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
		if err != nil {
			zero(token)
			return nil, 0, mapVaultError(err)
		}
		status := s.Status(ctx)
		if !found || !status.Configured || generation != status.CredentialGeneration || lease.CredentialGeneration != 0 && lease.CredentialGeneration != generation {
			zero(token)
			return nil, 0, newError(CodeCredentialStale, false, errors.New("observer credential changed"))
		}
		return token, generation, nil
	}()
	if err != nil {
		return err
	}
	defer zero(token)
	client, ok := s.client.(*HTTPControllerClient)
	if !ok {
		return newError(CodeControllerUnavailable, true, errors.New("observer transport unavailable"))
	}
	payload := map[string]any{"profile_id": s.profileID, "token": string(token), "credential_generation": generation}
	if action == "reconcile" {
		if bindings == nil {
			bindings = []MailObserverBinding{}
		}
		payload["bindings"] = bindings
	} else {
		payload["epoch"] = lease.Epoch
	}
	if action == "ack" {
		payload["sequence"] = seq
	}
	defer delete(payload, "token")
	return client.postJSON(ctx, "/v1/mail-observers/"+action, payload, maxControllerResponseBytes, out)
}
func (s *Service) ReconcileMailObservers(ctx context.Context, bindings []MailObserverBinding) (MailObserverLease, error) {
	var out MailObserverLease
	err := s.observerRequest(ctx, "reconcile", MailObserverLease{}, bindings, 0, &out)
	if err == nil && (out.SchemaVersion != 1 || len(out.Epoch) < 1 || len(out.Epoch) > 128 || out.CredentialGeneration < 1 || out.LeaseMS < 30000 || out.LeaseMS > 300000) {
		err = invalidControllerResponse()
	}
	return out, err
}
func (s *Service) PollMailObservers(ctx context.Context, lease MailObserverLease) (MailObserverBatch, error) {
	var out MailObserverBatch
	err := s.observerRequest(ctx, "events", lease, nil, 0, &out)
	if err == nil && (out.SchemaVersion != 1 || out.Epoch != lease.Epoch || len(out.Events) > 64) {
		err = invalidControllerResponse()
	}
	if err == nil {
		var last int64
		for _, e := range out.Events {
			if e.Epoch != lease.Epoch || e.CredentialGeneration != lease.CredentialGeneration || e.Sequence <= last || len(e.MailboxID) > 128 || len(e.OwnerScope) != 64 || len(e.WatchEpoch) > 128 || len(e.Reason) > 64 || e.ObservedAt.IsZero() {
				return out, invalidControllerResponse()
			}
			last = e.Sequence
		}
	}
	return out, err
}
func (s *Service) AckMailObservers(ctx context.Context, lease MailObserverLease, seq int64) error {
	var out struct {
		SchemaVersion int `json:"schema_version"`
	}
	err := s.observerRequest(ctx, "ack", lease, nil, seq, &out)
	if err == nil && out.SchemaVersion != 1 {
		return invalidControllerResponse()
	}
	return err
}
