package iscpnodeissuer

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	control "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpcontrol"
)

// boundEnrollment makes the aggregate publish a registry peer, as a completed
// enrollment does in production.
func boundEnrollment(s *State) {
	s.BoundOperation = "operation-enrollment"
	s.Operations[s.BoundOperation] = &Operation{ID: s.BoundOperation, State: "bundle_ready", Result: &EnrollmentResult{Version: 1, OperationID: s.BoundOperation, InvitationID: s.Credential.InvitationID, Identity: *s.Device}}
}

func authenticatedControl(t *testing.T, f *fixture, r control.Request) control.Request {
	t.Helper()
	ch := control.Challenge{Type: "iscp.control.challenge.v2", Protocol: control.Protocol, Audience: f.cfg.Issuer.Identity.DeviceID, ChallengeID: control.RandomID(), Challenge: control.RandomID(), Purpose: r.Action, ServerTime: time.Now().UTC().Truncate(time.Second), ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)}
	_ = control.Authenticate(&r, ch, f.client, "", time.Now())
	return r
}

// renewalRequest derives a fresh admission from the fixture request, exactly as
// the client coordinator does: a new operation at the observed generation.
func renewalRequest(t *testing.T, f *fixture, base control.Request, id string, generation control.Counter, predecessor *control.Predecessor) control.Request {
	t.Helper()
	intent := *base.Intent
	intent.OperationID = id
	intent.Preconditions.CredentialGeneration = generation
	intent.Predecessor = predecessor
	r := base
	r.Key.OperationID = id
	r.Intent = &intent
	r.SemanticDigest, _ = control.Digest("ISCP-OPERATION-INTENT-V2\x00", intent)
	delivery, _, _ := control.NewDelivery()
	r.Delivery = &delivery
	r.Ack = nil
	return authenticatedControl(t, f, r)
}

func ackRequest(t *testing.T, f *fixture, r control.Request, ack control.Ack) control.Request {
	t.Helper()
	r.Action = "ack"
	r.Intent = nil
	r.Delivery = nil
	r.Ack = &ack
	return authenticatedControl(t, f, r)
}

func installRenewal(t *testing.T, f *fixture, r control.Request) control.Result {
	t.Helper()
	ctx := context.Background()
	out, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || out.Result == nil || out.Result.Payload.State != "succeeded" {
		t.Fatalf("renewal %s failed: %v", r.Key.OperationID, e)
	}
	ack := control.Ack{ResultDigest: out.Result.ResultDigest, Disposition: "installed", CredentialGeneration: out.Result.Payload.CredentialGeneration, RecordedAt: time.Now().UTC().Truncate(time.Second)}
	d, e := f.issuer.Execute(ctx, ackRequest(t, f, r, ack), f.client.Identity, "")
	if e != nil || d.Disposition == nil {
		t.Fatalf("install ack %s failed: %v", r.Key.OperationID, e)
	}
	return *out.Result
}

func TestControlHistoryStaysBoundedAcrossRenewals(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	f, base := controlFixture(t, store)
	if e := store.Update(ctx, f.credential.InvitationID, func(s *State) error { boundEnrollment(s); return nil }); e != nil {
		t.Fatal(e)
	}
	generation := control.Counter("1")
	var first control.Request
	var sizeAtBound int
	cycles := ControlHistoryKeep*3 + 7
	for n := 0; n < cycles; n++ {
		r := renewalRequest(t, f, base, fmt.Sprintf("operation-renewal-%04d", n), generation, nil)
		if n == 0 {
			first = r
		}
		result := installRenewal(t, f, r)
		generation = result.Payload.CredentialGeneration
		st, e := store.Read(ctx, f.credential.InvitationID)
		if e != nil {
			t.Fatal(e)
		}
		if len(st.Control.Records) > ControlHistoryKeep || len(st.Control.Parents) != len(st.Control.Records) {
			t.Fatalf("cycle %d: history grew to %d records / %d parents", n, len(st.Control.Records), len(st.Control.Parents))
		}
		raw, _ := json.Marshal(st)
		if n == ControlHistoryKeep {
			sizeAtBound = len(raw)
		}
		if sizeAtBound > 0 && len(raw) > sizeAtBound+sizeAtBound/10 {
			t.Fatalf("cycle %d: aggregate keeps growing: %d > %d", n, len(raw), sizeAtBound)
		}
	}
	st, _ := store.Read(ctx, f.credential.InvitationID)
	if raw, _ := json.Marshal(st.Control); len(raw) > 64<<10 {
		t.Fatalf("superseded renewal history retained: %d bytes", len(raw))
	}
	if st.Control.Generation != control.Number(uint64(cycles+1)) || st.Control.RetiredRecords != uint64(cycles-ControlHistoryKeep) {
		t.Fatalf("generation %s retired %d", st.Control.Generation, st.Control.RetiredRecords)
	}
	registry, e := f.issuer.Registry(ctx)
	if e != nil || len(registry.Peers) != 1 {
		t.Fatal(e)
	}
	latest := st.Control.Records[renewalRequest(t, f, base, fmt.Sprintf("operation-renewal-%04d", cycles-1), "0", nil).Key.StorageKey()]
	if latest == nil || registry.Peers[0].Grant.GrantID != latest.Result.Payload.Body.Grant.GrantID {
		t.Fatal("registry no longer projects the latest generation")
	}

	// Replay protection: the retired first operation can never be issued again.
	if st.Control.Records[first.Key.StorageKey()] != nil {
		t.Fatal("oldest record was not retired")
	}
	replay, e := f.issuer.Execute(ctx, first, f.client.Identity, "")
	if e != nil || replay.Result == nil || replay.Result.Payload.State != "failed" || replay.Result.Payload.Body.Code != "generation_conflict" || replay.Result.Payload.Body.Grant != nil || replay.Result.Verify(f.cfg.Issuer.Identity) != nil {
		t.Fatalf("retired operation replay was not rejected by generation: %#v %v", replay, e)
	}
	after, _ := store.Read(ctx, f.credential.InvitationID)
	if after.Control.Generation != st.Control.Generation {
		t.Fatal("retired replay advanced the credential generation")
	}
	// A retained operation still replays its exact signed result.
	retained := renewalRequest(t, f, base, fmt.Sprintf("operation-renewal-%04d", cycles-2), "0", nil)
	record := after.Control.Records[retained.Key.StorageKey()]
	if record == nil {
		t.Fatal("recent record retired")
	}
	retained.Intent = &record.Intent
	retained.SemanticDigest = record.SemanticDigest
	retained = authenticatedControl(t, f, retained)
	again, e := f.issuer.Execute(ctx, retained, f.client.Identity, "")
	if e != nil || again.Result == nil || again.Result.ResultDigest != record.Result.ResultDigest {
		t.Fatalf("retained replay changed: %v", e)
	}
}

func TestControlRetentionKeepsUnacknowledgedAndPredecessorChain(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	f, base := controlFixture(t, store)
	// An old, never acknowledged issuance stays forever and keeps fencing
	// admission, however much newer acknowledged history surrounds it.
	_, _, st := largeIssuanceHistory(t)
	stale := renewalRequest(t, f, base, "operation-unacknowledged", "1", nil)
	old := time.Now().UTC().Add(-90 * 24 * time.Hour).Truncate(time.Second)
	for _, record := range st.Control.Records {
		record.CreatedAt = old.Add(time.Hour)
	}
	st.Control.Records[stale.Key.StorageKey()] = &control.Record{Key: stale.Key, Intent: *stale.Intent, SemanticDigest: stale.SemanticDigest, State: "running", Version: "1", Generation: "1", CreatedAt: old}
	st.Control.Parents[stale.Key.StorageKey()] = &ControlParent{ChildIndex: "0"}
	st.Credential = f.credential
	st.Device = &f.client.Identity
	if e := store.Update(ctx, f.credential.InvitationID, func(s *State) error { *s = *st; return nil }); e != nil {
		t.Fatal(e)
	}
	if retired := pruneControlHistory(st.Control); retired != 1300-ControlHistoryKeep {
		t.Fatalf("retired %d", retired)
	}
	if st.Control.Records[stale.Key.StorageKey()] == nil || st.Control.Parents[stale.Key.StorageKey()] == nil {
		t.Fatal("unacknowledged in-flight record was retired")
	}
	if len(st.Control.Records) != ControlHistoryKeep+1 {
		t.Fatalf("records %d", len(st.Control.Records))
	}
	blocked := renewalRequest(t, f, base, "operation-blocked", st.Control.Generation, nil)
	if _, e := f.issuer.Execute(ctx, blocked, f.client.Identity, ""); e == nil {
		t.Fatal("pending record no longer fences new admission")
	}

	// Predecessor chain: an expired acknowledgement on the newest record is
	// still accepted as predecessor after many records were retired.
	f2, base2 := controlFixture(t, NewMemoryStore())
	generation := control.Counter("1")
	var last control.Request
	for n := 0; n < ControlHistoryKeep+20; n++ {
		last = renewalRequest(t, f2, base2, fmt.Sprintf("operation-chain-%04d", n), generation, nil)
		if n == ControlHistoryKeep+19 {
			break
		}
		generation = installRenewal(t, f2, last).Payload.CredentialGeneration
	}
	out, e := f2.issuer.Execute(ctx, last, f2.client.Identity, "")
	if e != nil || out.Result == nil {
		t.Fatal(e)
	}
	f2.issuer.Now = func() time.Time { return out.Result.UsableUntil().Add(time.Second) }
	ack := control.Ack{ResultDigest: out.Result.ResultDigest, Disposition: "acknowledged_expired", CredentialGeneration: out.Result.Payload.CredentialGeneration, RecordedAt: f2.issuer.Now().UTC().Truncate(time.Second)}
	closed, e := f2.issuer.Execute(ctx, ackRequest(t, f2, last, ack), f2.client.Identity, "")
	if e != nil || closed.Disposition == nil {
		t.Fatal(e)
	}
	next := renewalRequest(t, f2, base2, "operation-chain-successor", out.Result.Payload.CredentialGeneration, &control.Predecessor{Key: last.Key, ResultDigest: out.Result.ResultDigest, DispositionDigest: closed.Disposition.DispositionDigest})
	res, e := f2.issuer.Execute(ctx, next, f2.client.Identity, "")
	if e != nil || res.Result == nil || res.Result.Payload.State != "succeeded" {
		t.Fatalf("predecessor successor failed after retention: %v", e)
	}
}

func TestCompactControlHistoryMigratesExistingAggregateOnce(t *testing.T) {
	ctx := context.Background()
	f, _, st := largeIssuanceHistory(t)
	boundEnrollment(st)
	if e := f.store.Update(ctx, st.Credential.InvitationID, func(s *State) error { *s = *st; return nil }); e != nil {
		t.Fatal(e)
	}
	before, e := f.issuer.Registry(ctx)
	if e != nil || len(before.Peers) != 1 {
		t.Fatal(e)
	}
	f.issuer.cfg.EnableControlV2 = false
	if _, e = f.issuer.CompactControlHistory(ctx); e == nil {
		t.Fatal("compaction ran without control v2")
	}
	f.issuer.cfg.EnableControlV2 = true
	report, e := f.issuer.CompactControlHistory(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if report.Aggregates != 1 || report.Compacted != 1 || report.RecordsBefore != 1300 || report.RecordsAfter != ControlHistoryKeep || report.RetiredRecords != 1300-ControlHistoryKeep {
		t.Fatalf("report %#v", report)
	}
	got, _ := f.store.Read(ctx, st.Credential.InvitationID)
	if len(got.Control.Records) != ControlHistoryKeep || len(got.Control.Parents) != ControlHistoryKeep || got.Control.Generation != "1301" || got.Control.HistoryID != st.Control.HistoryID || !got.Control.HistoryComplete {
		t.Fatal("compaction changed generation, namespace or kept the wrong count")
	}
	for n := 1300 - ControlHistoryKeep + 1; n <= 1300; n++ {
		key := control.Key{DomainID: got.Device.DomainID, DeviceID: got.Device.DeviceID, Namespace: control.CredentialNamespace, OperationID: fmt.Sprintf("operation-history-capacity-%04d", n)}
		if got.Control.Records[key.StorageKey()] == nil || got.Control.Parents[key.StorageKey()] == nil {
			t.Fatalf("newest record %d not retained", n)
		}
	}
	after, e := f.issuer.Registry(ctx)
	if e != nil || Digest(before) != Digest(after) {
		t.Fatal("compaction changed the published registry")
	}
	raw, _ := json.Marshal(got)
	if len(raw) > 1<<20 {
		t.Fatalf("compacted aggregate still %d bytes", len(raw))
	}
	again, e := f.issuer.CompactControlHistory(ctx)
	if e != nil || again.Compacted != 0 || again.RetiredRecords != 0 || again.RecordsAfter != ControlHistoryKeep {
		t.Fatalf("second compaction not idempotent: %#v %v", again, e)
	}
}
