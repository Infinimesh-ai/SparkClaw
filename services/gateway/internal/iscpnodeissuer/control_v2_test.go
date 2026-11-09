package iscpnodeissuer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	control "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpcontrol"
)

func controlFixture(t *testing.T, store Store) (*fixture, control.Request) {
	t.Helper()
	f := setup(t, store)
	f.issuer.cfg.EnableControlV2 = true
	e := store.Update(context.Background(), f.credential.InvitationID, func(s *State) error {
		s.State = "active"
		s.Device = &f.client.Identity
		s.Control = newControlState()
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	intent := control.Intent{Type: "iscp.operation.intent.v2", Version: 2, Namespace: control.CredentialNamespace, OperationID: "renew-control-test", Kind: "credential.renew", SubjectBinding: control.Subject{DomainID: f.client.Identity.DomainID, DeviceID: f.client.Identity.DeviceID, BindingID: f.credential.ClientID}, Target: control.Target{ServiceID: f.cfg.Issuer.Identity.DeviceID, ResourceID: f.credential.ClientID}, TaskAuthorization: control.Standing{Kind: "standing", AuthorizationRevision: "1", Scopes: []string{"settings.read"}}, Payload: control.Payload{RequestedScopes: []string{"settings.read"}}, Preconditions: control.Preconditions{ResourceVersion: "1", CredentialGeneration: "1", AuthorizationRevision: "1"}}
	d, _ := control.Digest("ISCP-OPERATION-INTENT-V2\x00", intent)
	request := control.Request{Type: "iscp.operation.request.v2", Protocol: control.Protocol, AttemptID: "attempt-control-test", Action: "recover", Key: control.Key{DomainID: f.client.Identity.DomainID, DeviceID: f.client.Identity.DeviceID, Namespace: intent.Namespace, OperationID: intent.OperationID}, SemanticDigest: d, Intent: &intent}
	ch := control.Challenge{Type: "iscp.control.challenge.v2", Protocol: control.Protocol, Audience: f.cfg.Issuer.Identity.DeviceID, ChallengeID: control.RandomID(), Challenge: control.RandomID(), Purpose: "recover", ServerTime: time.Now().UTC().Truncate(time.Second), ExpiresAt: time.Now().UTC().Truncate(time.Second).Add(10 * time.Second)}
	delivery, _, _ := control.NewDelivery()
	request.Delivery = &delivery
	if e = control.Authenticate(&request, ch, f.client, "", time.Now()); e != nil {
		t.Fatal(e)
	}
	return f, request
}
func TestControlRenewalStoresStableResultAndSignedExpiredDisposition(t *testing.T) {
	store := NewMemoryStore()
	f, r := controlFixture(t, store)
	ctx := context.Background()
	first, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil {
		t.Fatal(e)
	}
	if first.Result == nil || first.Result.Payload.CredentialGeneration != "2" {
		t.Fatalf("missing issuance: %#v", first)
	}
	original := first.Result.ResultDigest
	f.issuer.Now = func() time.Time { return first.Result.UsableUntil().Add(time.Second) }
	again, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || again.Result.ResultDigest != original {
		t.Fatalf("expired operation changed: %v", e)
	}
	ack := control.Ack{ResultDigest: original, Disposition: "acknowledged_expired", CredentialGeneration: "2", RecordedAt: f.issuer.Now()}
	ar := r
	ar.Action = "ack"
	ar.Intent = nil
	ar.Delivery = nil
	ar.Ack = &ack
	ch := control.Challenge{Audience: f.cfg.Issuer.Identity.DeviceID, ChallengeID: control.RandomID(), Challenge: control.RandomID()}
	_ = control.Authenticate(&ar, ch, f.client, "", time.Now())
	closed, e := f.issuer.Execute(ctx, ar, f.client.Identity, "")
	if e != nil || closed.Disposition == nil {
		t.Fatalf("close failed %v", e)
	}
	retry, e := f.issuer.Execute(ctx, ar, f.client.Identity, "")
	if e != nil || retry.Disposition.DispositionDigest != closed.Disposition.DispositionDigest {
		t.Fatal("lost ACK replay differs")
	}
	next := *r.Intent
	next.OperationID = "renew-control-successor"
	next.Preconditions.CredentialGeneration = "2"
	next.Predecessor = &control.Predecessor{Key: r.Key, ResultDigest: original, DispositionDigest: closed.Disposition.DispositionDigest}
	r.Intent = &next
	r.Key.OperationID = next.OperationID
	r.SemanticDigest, _ = control.Digest("ISCP-OPERATION-INTENT-V2\x00", next)
	res, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || res.Result.Payload.CredentialGeneration != "3" {
		t.Fatalf("successor failed: %v", e)
	}
	st, _ := store.Read(ctx, f.credential.InvitationID)
	if len(st.Control.Records) != 2 || st.Control.Records[ar.Key.StorageKey()].Result.ResultDigest != original {
		t.Fatal("predecessor was dropped or changed")
	}
}

func TestRegistryProjectsLatestControlGenerationWithinEnrollmentSecond(t *testing.T) {
	for _, fraction := range []time.Duration{0, 500 * time.Millisecond} {
		t.Run(fraction.String(), func(t *testing.T) {
			store := NewMemoryStore()
			f, request := controlFixture(t, store)
			ctx := t.Context()
			now := time.Now().UTC().Truncate(time.Second)
			f.issuer.Now = func() time.Time { return now }
			if err := store.Update(ctx, f.credential.InvitationID, func(s *State) error {
				seed, err := f.issuer.signGrant(s, f.client.Identity, now.Add(fraction))
				if err != nil {
					return err
				}
				s.BoundOperation = "original-enrollment"
				s.Operations[s.BoundOperation] = &Operation{Result: &EnrollmentResult{Grant: seed, Binding: Binding{DeploymentID: f.credential.DeploymentID, OwnerID: f.credential.OwnerID, ClientID: f.credential.ClientID}, Scopes: f.credential.Scopes, InstallationID: "ae79ea35-2871-4405-a85b-004956ebc31b"}}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, generation := range []control.Counter{"2", "3"} {
				out, err := f.issuer.Execute(ctx, request, f.client.Identity, "")
				if err != nil || out.Result == nil || out.Result.Payload.CredentialGeneration != generation {
					t.Fatalf("control issuance failed: %+v %v", out, err)
				}
				registry, err := f.issuer.Registry(ctx)
				if err != nil || len(registry.Peers) != 1 || Digest(registry.Peers[0].Grant) != Digest(*out.Result.Payload.Body.Grant) {
					t.Fatalf("registry did not publish accepted generation %s in the same second: %v", generation, err)
				}
				ack := request
				ack.Action, ack.Intent, ack.Delivery = "ack", nil, nil
				ack.Ack = &control.Ack{ResultDigest: out.Result.ResultDigest, Disposition: "installed", CredentialGeneration: generation, RecordedAt: now}
				closed, err := f.issuer.Execute(ctx, ack, f.client.Identity, "")
				if err != nil || closed.Disposition == nil {
					t.Fatal("could not retain installed predecessor", err)
				}
				next := *request.Intent
				next.OperationID += "-next"
				next.Preconditions.CredentialGeneration = generation
				request.Intent = &next
				request.Key.OperationID = next.OperationID
				request.SemanticDigest, _ = control.Digest("ISCP-OPERATION-INTENT-V2\x00", next)
			}
		})
	}
}
func TestControlHistoryMissingAndClosingNeverAdmitOrDisclose(t *testing.T) {
	store := NewMemoryStore()
	f, r := controlFixture(t, store)
	ctx := context.Background()
	_ = store.Update(ctx, f.credential.InvitationID, func(s *State) error { s.Control.HistoryComplete = false; return nil })
	out, e := f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || out.Status == nil || out.Status.Code != "history_unavailable" {
		t.Fatalf("missing history admitted: %v", e)
	}
	_ = store.Update(ctx, f.credential.InvitationID, func(s *State) error { s.Control.HistoryComplete = true; return nil })
	out, e = f.issuer.Execute(ctx, r, f.client.Identity, "")
	if e != nil || out.Result == nil {
		t.Fatal(e)
	}
	_ = store.Update(ctx, f.credential.InvitationID, func(s *State) error { s.State = "revoked"; s.Revision = 2; s.RelayRevoked = false; return nil })
	_, e = f.issuer.Execute(ctx, r, f.client.Identity, "")
	var failure *control.Failure
	if !errors.As(e, &failure) || failure.Code != "authorization_unavailable" {
		t.Fatalf("closing disclosed %v", e)
	}
	_ = store.Update(ctx, f.credential.InvitationID, func(s *State) error { s.RelayRevoked = true; return nil })
	_, e = f.issuer.Execute(ctx, r, f.client.Identity, "")
	if !errors.As(e, &failure) || failure.Code != "authorization_revoked" {
		t.Fatalf("revocation absent: %v", e)
	}
}
func TestControlPostgresRetainsSameResultAcrossReopen(t *testing.T) {
	dsn := os.Getenv("SC_ISCP_NODE_TEST_DSN")
	if dsn == "" {
		t.Skip("isolated PostgreSQL required")
	}
	key := make([]byte, 32)
	key[0] = 43
	store, e := NewPostgresStore(context.Background(), dsn, key)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close() // use a fresh isolated DB supplied by the test runner

	f, r := controlFixture(t, store)
	first, e := f.issuer.Execute(context.Background(), r, f.client.Identity, "")
	if e != nil {
		t.Fatal(e)
	}
	other, e := NewPostgresStore(context.Background(), dsn, key)
	if e != nil {
		t.Fatal(e)
	}
	defer other.Close()
	fresh, e := New(f.issuer.cfg, other, f.management, f.relay)
	if e != nil {
		t.Fatal(e)
	}
	second, e := fresh.Execute(context.Background(), r, f.client.Identity, "")
	if e != nil || second.Result.ResultDigest != first.Result.ResultDigest {
		t.Fatalf("reopen changed issuance: %v", e)
	}
	_, _ = store.pool.Exec(context.Background(), "DELETE FROM sparkclaw_iscp_issuer_state WHERE id=$1", f.credential.InvitationID)
}
