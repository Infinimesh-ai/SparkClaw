// Package enrolltest provides an in-process node issuer behind a pinned TLS
// test server for enrollment tests. It is test support only.
package enrolltest

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	control "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpcontrol"
	n "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/descriptor"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

// Fixture is one issuer with a fresh 15-minute invitation credential.
type Fixture struct {
	Credential   string
	Issuer       *n.Issuer
	Registration *Registration
	URL          string
}

// New starts the issuer test server. grantTTL bounds the enrollment Grant.
func New(t testing.TB, grantTTL time.Duration) Fixture {
	credential, issuer, registration, url := newFixture(t, false, grantTTL)
	return Fixture{Credential: credential, Issuer: issuer, Registration: registration, URL: url}
}

// Counts returns committed registrations and recoveries.
func (r *Registration) Counts() (commits, recoveries int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Commits, r.Recoveries
}

type Management struct {
	mu     sync.Mutex
	active map[string]bool
}

func (m *Management) Identity(context.Context) (n.Binding, error) {
	return n.Binding{DeploymentID: "deployment", OwnerID: "owner"}, nil
}
func (m *Management) Client(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.active[id], nil
}
func (m *Management) CreateClient(_ context.Context, _ string, key string) (string, error) {
	id := n.StableClientID("owner", key)
	m.mu.Lock()
	m.active[id] = true
	m.mu.Unlock()
	return id, nil
}
func (m *Management) RevokeManagedClient(_ context.Context, id string) error {
	m.mu.Lock()
	m.active[id] = false
	m.mu.Unlock()
	return nil
}

type Registration struct {
	mu         sync.Mutex
	prepared   map[string]n.RegistrationPreparation
	receipts   map[string]n.RegistrationReceipt
	Commits    int
	Recoveries int
	ttl        time.Duration
}

func (r *Registration) Prepare(_ context.Context, p n.RegistrationPreparation) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prepared[p.OperationID] = p
	return nil
}
func (r *Registration) Commit(_ context.Context, p n.RegistrationCommit) (n.RegistrationReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.receipts[p.OperationID]; ok {
		return old, nil
	}
	prep := r.prepared[p.OperationID]
	r.Commits++
	receipt, e := receipt(prep.OperationID, prep.BindingDigest, prep.ReceiptWrapPublicKey, prep.Identity, r.ttl)
	r.receipts[p.OperationID] = receipt
	return receipt, e
}
func (r *Registration) Status(_ context.Context, op, digest string) (n.RegistrationReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.receipts[op]
	if !ok || v.BindingDigest != digest {
		return v, n.ErrDenied
	}
	return v, nil
}
func (r *Registration) Revoke(context.Context, string, string, uint64) error { return nil }
func (r *Registration) Recover(_ context.Context, p n.RegistrationRecovery) (n.RegistrationReceipt, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Recoveries++
	return receipt(p.OperationID, p.BindingDigest, p.ReceiptWrapPublicKey, p.Identity, time.Hour)
}
func receipt(op, digest, wrap string, id identity.DeviceIdentity, ttl time.Duration) (n.RegistrationReceipt, error) {
	now := time.Now().UTC()
	return n.SealRegistrationReceipt(n.RegistrationReceipt{OperationID: op, BindingDigest: digest, IssuedAt: now}, wrap, id, n.RelayCredentials{Access: iscpbridge.RelayCredential{DomainID: id.DomainID, DeviceID: id.DeviceID, Token: "synthetic-access-private", ExpiresAt: now.Add(ttl)}, Refresh: iscpbridge.RelayCredential{DomainID: id.DomainID, DeviceID: id.DeviceID, Token: "synthetic-refresh-private", ExpiresAt: now.Add(ttl)}})
}

func newFixture(t testing.TB, drop bool, grantTTL time.Duration) (string, *n.Issuer, *Registration, string) {
	t.Helper()
	device := func(id string) identity.Device {
		d, e := identity.NewDevice(c.NewProvider(), "domain", id, time.Now().UTC())
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	signer, inviter, responder, relay := device("issuer"), device("inviter"), device("responder"), device("relay-signer")
	var issuer *n.Issuer
	var controlHandler http.Handler
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/control/") {
			controlHandler.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/.well-known/iscp/relay" {
			now := time.Now().UTC()
			d := descriptor.RelayDescriptor{Type: "iscp.relay.descriptor.v2", DomainID: "domain", RelayID: "relay", BaseURL: server.URL, WebSocketURL: "wss" + strings.TrimPrefix(server.URL, "https") + "/v2/relay/duplex", SigningKeys: []descriptor.PublicKey{{KTY: "Ed25519", Use: "descriptor-signature", KID: relay.Identity.PublicKey.KID, Public: relay.Identity.PublicKey.Public}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
			signed, _ := descriptor.Sign(c.NewProvider(), relay, d.Type, d, now)
			_ = json.NewEncoder(w).Encode(map[string]any{"descriptor": signed})
			return
		}
		if r.URL.Path == "/v2/relay/duplex" {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path != n.ExchangePath {
			t.Error("helper left its TEST1 application origin", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var x n.Exchange
		if n.StrictDecode(raw, &x) != nil {
			w.WriteHeader(400)
			return
		}
		out, e := issuer.Exchange(r.Context(), x)
		if e != nil {
			status := 503
			if errors.Is(e, n.ErrDenied) {
				status = 403
			}
			w.WriteHeader(status)
			return
		}
		if drop && x.Step == "redeem" {
			drop = false
			w.WriteHeader(503)
			return
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(server.Close)
	management := &Management{active: map[string]bool{}}
	registration := &Registration{prepared: map[string]n.RegistrationPreparation{}, receipts: map[string]n.RegistrationReceipt{}, ttl: grantTTL}
	controlRelay := &ControlRelay{signer: relay, actor: device("issuer-service-actor"), registration: registration, records: map[string]*control.Record{}, generations: map[string]uint64{}}
	var err error
	issuerConfig := n.Config{EnableControlV2: true, ControlRelay: controlRelay, Template: n.Credential{Origin: server.URL, TLSCertificatePEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})), DomainID: "domain", RelayID: "relay", RelaySignerIdentity: relay.Identity, IssuerIdentity: signer.Identity, InvitationSignerIdentity: inviter.Identity, IssuerRoute: "work2-node", ResponderIdentity: responder.Identity, DeploymentID: "deployment", OwnerID: "owner"}, Issuer: signer, InvitationSigner: inviter, GrantTTL: grantTTL}
	issuerStore := n.NewMemoryStore()
	issuer, err = n.New(issuerConfig, issuerStore, management, registration)
	if err != nil {
		t.Fatal(err)
	}
	// Only the retained enrollment seed has a one-second Grant. Recovery
	// exercises normal installation latency with a fresh Grant, rather than
	// racing the control wire's whole-second timestamp boundary on every ACK.
	// Both issuer instances share the exact state, identity and authorities.
	controlConfig := issuerConfig
	controlConfig.GrantTTL = time.Minute
	controlIssuer, err := n.New(controlConfig, issuerStore, management, registration)
	if err != nil {
		t.Fatal(err)
	}
	controlHandler = controlIssuer.ControlHandler()
	encoded, err := issuer.CreateInvitation(context.Background(), "Phone", 15*time.Minute, []string{"workbench.read"}, "create-native-phone")
	if err != nil {
		t.Fatal(err)
	}
	return encoded, issuer, registration, server.URL
}

// The helper test exercises the real HTTPS issuer Handler and durable client
// coordinator. This Relay fixture keeps signed child results keyed by original
// operations; the independent adapter suite exercises the actual PG child.
type ControlRelay struct {
	mu            sync.Mutex
	signer, actor identity.Device
	registration  *Registration
	records       map[string]*control.Record
	generations   map[string]uint64
}

func (r *ControlRelay) Actor() identity.DeviceIdentity { return r.actor.Identity }
func (r *ControlRelay) Authority(_ context.Context, s control.Subject, t control.Target) (n.ControlAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.generations[s.DeviceID]
	if g == 0 {
		g = 1
	}
	return n.ControlAuthority{Subject: s, Target: t, Generation: control.Number(g), AuthorizationRevision: "1", ResourceVersion: "1", Scopes: []string{n.Permission}}, nil
}
func (r *ControlRelay) Attempt(_ context.Context, q control.Request, _ string) (control.Outcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	record := r.records[q.Key.StorageKey()]
	if q.Action == "ack" {
		d, err := record.Acknowledge(*q.Ack, control.Number(r.generations[record.Intent.SubjectBinding.DeviceID]), true, now, r.signer)
		return control.Outcome{Disposition: &d}, err
	}
	if record == nil {
		s := q.Intent.SubjectBinding
		g := r.generations[s.DeviceID]
		if g == 0 {
			g = 1
		}
		g++
		r.generations[s.DeviceID] = g
		tokens := &control.RelayCredentials{TargetServiceID: q.Intent.Target.ServiceID, BindingID: s.BindingID, CredentialGeneration: control.Number(g), AuthorizationRevision: "1", Access: control.Credential{DomainID: s.DomainID, DeviceID: s.DeviceID, Token: "synthetic-access-private-" + string(control.Number(g)), ExpiresAt: now.Add(time.Hour)}, Refresh: control.Credential{DomainID: s.DomainID, DeviceID: s.DeviceID, Token: "synthetic-refresh-private-" + string(control.Number(g)), ExpiresAt: now.Add(time.Hour)}}
		result, err := control.NewResult(control.ResultPayload{Key: q.Key, SemanticDigest: q.SemanticDigest, ResultVersion: "1", State: "succeeded", CompletedAt: now, CredentialGeneration: control.Number(g), Body: control.ResultBody{RelayCredentials: tokens}}, r.signer)
		if err != nil {
			return control.Outcome{}, err
		}
		record = &control.Record{Key: q.Key, Intent: *q.Intent, SemanticDigest: q.SemanticDigest, State: "succeeded", Version: "1", Generation: control.Number(g), Result: &result, CreatedAt: now}
		r.records[q.Key.StorageKey()] = record
		r.registration.mu.Lock()
		r.registration.Recoveries++
		r.registration.mu.Unlock()
	}
	return control.Outcome{Result: record.Result}, nil
}
