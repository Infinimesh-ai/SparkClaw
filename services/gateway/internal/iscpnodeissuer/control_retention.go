package iscpnodeissuer

import (
	"context"
	"sort"

	control "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpcontrol"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

// Renewal and recovery Records only prove that the device's connection was
// authorized at the time; superseded ones carry no recovery meaning. Business
// reconnection is reconciled through the task index, never through this
// history. The aggregate therefore keeps only what the control exchange itself
// still needs and retires everything else in the admitting transaction:
//   - every Record without a Disposition or not yet terminal: the device's
//     in-flight operation, which also keeps fencing new admission;
//   - the highest-generation succeeded Grant, which the peer registry projects;
//   - the ControlHistoryKeep newest Records, a small idempotency margin so a
//     lost ACK/lookup response or a Predecessor reference to the device's last
//     closed operation still resolves exactly. The device is the only subject
//     of its aggregate and admits nothing while its journal holds a pending
//     operation, so that operation is always among the newest.
//
// A replayed intent of a retired key reaches admission with a credential
// generation precondition below the ledger's current generation and receives a
// signed failed generation_conflict result; it can never be issued again.
const ControlHistoryKeep = 4

func terminalControlState(state string) bool {
	return state == "succeeded" || state == "failed" || state == "cancelled"
}

// pruneControlHistory retires superseded Records and their Parents. The
// caller holds the aggregate's row lock and persists the result atomically with
// the transition that triggered it.
func pruneControlHistory(ledger *ControlState) int {
	if ledger == nil || len(ledger.Records) <= ControlHistoryKeep {
		return 0
	}
	type entry struct {
		key        string
		record     *control.Record
		generation uint64
		parsed     bool
	}
	entries := make([]entry, 0, len(ledger.Records))
	latestGrant, latestGeneration := "", uint64(0)
	for key, record := range ledger.Records {
		generation, err := record.Generation.Uint64()
		entries = append(entries, entry{key, record, generation, err == nil})
		if err == nil && record.State == "succeeded" && record.Result != nil && record.Result.Payload.Body.Grant != nil && generation > latestGeneration {
			latestGrant, latestGeneration = key, generation
		}
	}
	sort.Slice(entries, func(a, b int) bool {
		x, y := entries[a], entries[b]
		if !x.record.CreatedAt.Equal(y.record.CreatedAt) {
			return x.record.CreatedAt.After(y.record.CreatedAt)
		}
		if x.generation != y.generation {
			return x.generation > y.generation
		}
		return x.key > y.key
	})
	retired := 0
	for n, item := range entries {
		if n < ControlHistoryKeep || !item.parsed || item.key == latestGrant || item.record.Disposition == nil || !terminalControlState(item.record.State) || item.record.Result == nil {
			continue
		}
		delete(ledger.Records, item.key)
		delete(ledger.Parents, item.key)
		retired++
	}
	ledger.RetiredRecords += uint64(retired)
	return retired
}

// projectedGrant is the Grant the peer registry publishes for one aggregate.
func projectedGrant(s *State) (trust.Grant, bool, error) {
	if s.Device == nil {
		return trust.Grant{}, false, nil
	}
	op := s.Operations[s.BoundOperation]
	if op == nil || op.Result == nil {
		return trust.Grant{}, false, nil
	}
	grant := op.Result.Grant
	for _, o := range s.Operations {
		if o.Grant != nil && o.Grant.NotBefore.After(grant.NotBefore) {
			grant = *o.Grant
		}
	}
	if s.Control != nil {
		// ControlV2 orders issuance by its durable generation. Its whole-
		// second NotBefore may precede the enrollment's fractional timestamp,
		// and multiple completed generations may share the same timestamp.
		var latestGeneration uint64
		for _, record := range s.Control.Records {
			if record.State == "succeeded" && record.Result != nil && record.Result.Payload.Body.Grant != nil {
				generation, err := record.Generation.Uint64()
				if err != nil || generation == 0 || record.Result.Payload.CredentialGeneration != record.Generation {
					return trust.Grant{}, false, ErrInvalid
				}
				if generation > latestGeneration {
					latestGeneration = generation
					grant = *record.Result.Payload.Body.Grant
				}
			}
		}
	}
	return grant, true, nil
}

type ControlCompaction struct {
	Aggregates     int `json:"aggregates"`
	Compacted      int `json:"compacted"`
	RecordsBefore  int `json:"records_before"`
	RecordsAfter   int `json:"records_after"`
	RetiredRecords int `json:"retired_records"`
}

// CompactControlHistory is the explicit one-time maintenance action for
// aggregates that accumulated history before bounded retention existed. Each
// aggregate is compacted in its own locked transaction with the same rule that
// admission applies; a change of the published Grant aborts that transaction.
func (i *Issuer) CompactControlHistory(ctx context.Context) (ControlCompaction, error) {
	var report ControlCompaction
	if !i.cfg.EnableControlV2 {
		return report, ErrInvalid
	}
	if err := i.store.RequireControlWriter(ctx); err != nil {
		return report, err
	}
	rows, err := i.store.List(ctx)
	if err != nil {
		return report, err
	}
	for _, row := range rows {
		if row.Control == nil {
			continue
		}
		report.Aggregates++
		if len(row.Control.Records) <= ControlHistoryKeep {
			report.RecordsBefore += len(row.Control.Records)
			report.RecordsAfter += len(row.Control.Records)
			continue
		}
		var before, after, retired int
		err = i.store.Update(ctx, row.Credential.InvitationID, func(s *State) error {
			if s.Control == nil {
				return nil
			}
			grant, published, err := projectedGrant(s)
			if err != nil {
				return err
			}
			before = len(s.Control.Records)
			retired = pruneControlHistory(s.Control)
			after = len(s.Control.Records)
			next, stillPublished, err := projectedGrant(s)
			if err != nil || published != stillPublished || Digest(grant) != Digest(next) {
				return ErrConflict
			}
			return nil
		})
		if err != nil {
			return report, err
		}
		report.RecordsBefore += before
		report.RecordsAfter += after
		report.RetiredRecords += retired
		if retired > 0 {
			report.Compacted++
		}
	}
	return report, i.publishRegistry(ctx)
}
