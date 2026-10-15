package gateway

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

type nodeClientsFixture struct {
	server             *Server
	cfg                iscpworkbench.Config
	handler            iscpworkbench.Handler
	ctx                context.Context
	credential         string
	tokenFile, socket  string
	records            []iscpnodeissuer.DeviceRecord
	issuedKeys         map[string]string
	calls, revocations int
	pendingAck         bool
	revokeOperation    string
}

func nodeClientsTestFixture(t *testing.T) *nodeClientsFixture {
	t.Helper()
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	// A short canonical private path also works on macOS's Unix path limit.
	dir, err := os.MkdirTemp("/tmp", "iscp-ui-")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket, tokenFile := filepath.Join(dir, "i.sock"), filepath.Join(dir, "token")
	token := strings.Repeat("operator-only-", 4)
	if err = os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(socket, 0600); err != nil {
		t.Fatal(err)
	}
	provider := c.NewProvider()
	now := time.Now().UTC()
	newDevice := func(id string) identity.Device {
		v, e := identity.NewDevice(provider, "domain", id, now)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	relay, issuer, inviter, responder := newDevice("relay"), newDevice("issuer"), newDevice("inviter"), newDevice("responder")
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	v := iscpnodeissuer.Credential{SchemaVersion: 1, Type: iscpnodeissuer.CredentialType, Origin: "https://127.0.0.1", TLSCertificatePEM: certPEM, DomainID: "domain", RelayID: "relay", RelaySignerIdentity: relay.Identity, IssuerIdentity: issuer.Identity, InvitationSignerIdentity: inviter.Identity, ResponderIdentity: responder.Identity, IssuerRoute: "work2", DeploymentID: cfg.Binding.DeploymentID, OwnerID: cfg.Binding.OwnerID, ClientID: "node-client", InvitationID: "invitation", IssuedAt: now, ExpiresAt: now.Add(15 * time.Minute), Scopes: []string{"settings.read", "settings.write"}, BootstrapSecret: c.Base64URL(c.RandomBytes(32))}
	v, err = iscpnodeissuer.SignCredential(v, inviter)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := iscpnodeissuer.EncodeCredential(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = iscpnodeissuer.ParseCredential(encoded, now, false); err != nil {
		t.Fatal("fixture signed credential", err)
	}
	f := &nodeClientsFixture{server: server, credential: encoded, tokenFile: tokenFile, socket: socket, issuedKeys: map[string]string{}}
	private := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/devices":
			_ = json.NewEncoder(w).Encode(map[string]any{"schema_version": 1, "deployment_id": cfg.Binding.DeploymentID, "devices": f.records})
		case r.Method == "POST" && r.URL.Path == "/v1/invitations":
			var req struct {
				Name       string `json:"client_name"`
				Operation  string `json:"operation_id"`
				Owner      string `json:"owner_id"`
				Deployment string `json:"deployment_id"`
			}
			if domainDecode(mustReadBody(t, r), &req) != nil || req.Owner != cfg.Binding.OwnerID || req.Deployment != cfg.Binding.DeploymentID {
				w.WriteHeader(403)
				return
			}
			f.calls++
			if prior, ok := f.issuedKeys[req.Operation]; ok && prior != req.Name {
				w.WriteHeader(409)
				return
			}
			if _, ok := f.issuedKeys[req.Operation]; !ok {
				f.issuedKeys[req.Operation] = req.Name
				_, err := server.store.RegisterClient(r.Context(), app.Client{ID: "node-client", OwnerID: cfg.Binding.OwnerID, ActorID: cfg.Binding.OwnerID, Name: req.Name, TokenHash: hashSecret("server-only-client-bearer")})
				if err != nil {
					t.Error(err)
					w.WriteHeader(503)
					return
				}
				f.records = append(f.records, iscpnodeissuer.DeviceRecord{ClientID: "node-client", InvitationID: "invitation", State: "issued", Revision: 1})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"credential": encoded, "client_id": "node-client", "invitation_id": "invitation", "revision": 1, "state": "issued"})
		case r.Method == "POST" && r.URL.Path == "/v1/devices/node-client/revoke":
			var req struct {
				Operation string `json:"operation_id"`
				Revision  uint64 `json:"expected_revision"`
			}
			if domainDecode(mustReadBody(t, r), &req) != nil || req.Revision != 1 || f.revokeOperation != "" && f.revokeOperation != req.Operation {
				w.WriteHeader(409)
				return
			}
			f.revokeOperation = req.Operation
			f.revocations++
			f.records[0].State = "revoked"
			f.records[0].Revision = 2
			if _, err := server.store.RevokeClient(r.Context(), "node-client"); err != nil {
				t.Error(err)
				w.WriteHeader(503)
				return
			}
			if f.pendingAck {
				w.WriteHeader(503)
				return
			}
			f.records[0].RevocationComplete = true
			_ = json.NewEncoder(w).Encode(map[string]any{"state": "revoked", "revision": 2, "revocation_complete": true})
		default:
			w.WriteHeader(404)
		}
	})}
	go func() { _ = private.Serve(listener) }()
	t.Cleanup(func() { _ = private.Close() })
	cfg.SchemaVersion = 2
	cfg.Mode = "node-managed"
	cfg.NodeIssuerSocket = socket
	cfg.NodeIssuerTokenFile = tokenFile
	f.cfg = cfg
	f.ctx = context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{OwnerID: cfg.Binding.OwnerID, ActorID: cfg.Binding.OwnerID, ClientID: cfg.Binding.ClientID, Authenticated: true})
	f.handler, err = server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func mustReadBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	var value json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		t.Error(err)
	}
	return value
}
func (f *nodeClientsFixture) call(op string, body string) iscpworkbench.Response {
	q := v2Request(op, []byte(body))
	return f.handler(f.ctx, q)
}

func TestNodeManagedClientsOriginalResponseAndSecretFreeDurableBoundary(t *testing.T) {
	f := nodeClientsTestFixture(t)
	request := v2Request(iscpworkbench.OperationClientsIssue, []byte(`{"client_name":"second desktop"}`))
	first := f.handler(f.ctx, request)
	if first.Status != 200 {
		t.Fatalf("issue: %+v, calls %d", first, f.calls)
	}
	var response struct {
		Client app.Client `json:"client"`
		Token  string     `json:"token"`
	}
	if json.Unmarshal(first.Body, &response) != nil || response.Client.ID != "node-client" || response.Token != f.credential || strings.Contains(string(first.Body), "revision") || strings.Contains(string(first.Body), "TokenHash") {
		t.Fatalf("original response differs: %s", first.Body)
	}
	second := f.handler(f.ctx, request)
	if second.Status != 200 || string(first.Body) != string(second.Body) || len(f.issuedKeys) != 1 {
		t.Fatalf("issuer original operation replay: %+v", second)
	}
	request.Body = []byte(`{"client_name":"changed"}`)
	if f.handler(f.ctx, request).Status != 409 {
		t.Fatal("same operation changed name admitted")
	}
	_, err := f.server.store.RegisterClient(f.ctx, app.Client{ID: "foreign", OwnerID: "other-owner", Name: "foreign", TokenHash: "private"})
	if err != nil {
		t.Fatal(err)
	}
	list := f.call(iscpworkbench.OperationClientsList, "")
	if list.Status != 200 || strings.Contains(string(list.Body), "foreign") || !strings.Contains(string(list.Body), "node-client") {
		t.Fatalf("owner list: %+v", list)
	}
	// The plaintext domain directory may contain ordinary non-secret receipts,
	// but must never retain this complete invitation or its bootstrap authority.
	filepath.WalkDir(f.server.executionRoot, func(name string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			raw, _ := os.ReadFile(name)
			if strings.Contains(string(raw), f.credential) {
				t.Errorf("credential entered plaintext journal %s", name)
			}
		}
		return nil
	})
}

func TestNodeManagedRevokeRequiresRelayCompletionAndRetainsLegacyOwnerFiltering(t *testing.T) {
	f := nodeClientsTestFixture(t)
	if f.call(iscpworkbench.OperationClientsIssue, `{"client_name":"second desktop"}`).Status != 200 {
		t.Fatal("issue")
	}
	q := v2Request(iscpworkbench.OperationClientsRevoke, []byte(`{}`))
	q.Params = map[string]string{"id": "node-client"}
	f.pendingAck = true
	if r := f.handler(f.ctx, q); r.Status != 503 {
		t.Fatalf("early success without Relay ack: %+v", r)
	}
	f.pendingAck = false
	if r := f.handler(f.ctx, q); r.Status != 200 || !strings.Contains(string(r.Body), "revoked_at") {
		t.Fatalf("original revoke replay did not finish: %+v", r)
	}
	if f.revocations != 2 || f.revokeOperation != q.OperationID {
		t.Fatal("revoke operation changed")
	}
	_, err := f.server.store.RegisterClient(f.ctx, app.Client{ID: "legacy-owned", OwnerID: f.cfg.Binding.OwnerID, Name: "legacy", TokenHash: "private"})
	if err != nil {
		t.Fatal(err)
	}
	q = v2Request(iscpworkbench.OperationClientsRevoke, []byte(`{}`))
	q.Params = map[string]string{"id": "legacy-owned"}
	if r := f.handler(f.ctx, q); r.Status != 200 {
		t.Fatalf("legacy revoke: %+v", r)
	}
	q.Params = map[string]string{"id": "other-owner"}
	if r := f.handler(f.ctx, q); r.Status != 404 {
		t.Fatalf("foreign revoke: %+v", r)
	}
}

func TestNodeManagedClientsClosedInputsTokenPermissionsAndScopes(t *testing.T) {
	f := nodeClientsTestFixture(t)
	for _, body := range []string{`{"client_name":"name","owner_id":"other"}`, `{"client_name":"name","scopes":["admin"]}`, `{"client_name":"name","origin":"https://elsewhere"}`, `{"client_name":"` + strings.Repeat("x", 81) + `"}`} {
		if r := f.call(iscpworkbench.OperationClientsIssue, body); r.Status != 400 {
			t.Fatalf("extended input: %+v", r)
		}
	}
	q := v2Request(iscpworkbench.OperationClientsList, nil)
	q.Params = map[string]string{"url": "https://elsewhere"}
	if f.handler(f.ctx, q).Status != 400 {
		t.Fatal("caller URL admitted")
	}
	q = v2Request(iscpworkbench.OperationClientsRevoke, []byte(`{}`))
	q.Params = map[string]string{"id": "../escape"}
	if f.handler(f.ctx, q).Status != 400 {
		t.Fatal("path escape admitted")
	}
	if err := os.Chmod(f.tokenFile, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.server.NewWorkbenchISCPDomainHandler(f.cfg); err == nil {
		t.Fatal("public token file admitted")
	}
	if err := os.Chmod(f.tokenFile, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := f.cfg
	cfg.NodeIssuerTokenFile = ""
	closed, err := f.server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if closed(f.ctx, v2Request(iscpworkbench.OperationClientsList, nil)).Status != 501 {
		t.Fatal("unconfigured devices advertised")
	}
}

func TestNodeManagedClientsCapabilityGateAndOrdinaryHTTPRemainSeparate(t *testing.T) {
	f := nodeClientsTestFixture(t)
	f.cfg.QualifiedCapabilities = []string{iscpworkbench.OperationClientsList, iscpworkbench.OperationClientsIssue, iscpworkbench.OperationClientsRevoke}
	management, err := newNodeClientManagement(f.cfg, f.server.cfg.Gateway.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{server: f.server, config: f.cfg, nodeClients: management}
	session := iscpworkbench.SessionInfo{Profile: iscpworkbench.ProfileV2, SessionID: "session", Scopes: []string{"settings.read", "settings.write"}, GrantRevision: 1}
	result := adapter.capabilitiesWithSession(f.ctx, v2Request(iscpworkbench.OperationCapabilitiesGet, nil), session)
	var report struct {
		Capabilities []iscpDomainCapability `json:"capabilities"`
	}
	if json.Unmarshal(result.body, &report) != nil {
		t.Fatal("report")
	}
	enabled := false
	for _, row := range report.Capabilities {
		if row.ID == "settings_devices" {
			enabled = row.Enabled
		}
	}
	if !enabled {
		t.Fatal("qualified private management unavailable")
	}
	adapter.nodeClients = nil
	result = adapter.capabilitiesWithSession(f.ctx, v2Request(iscpworkbench.OperationCapabilitiesGet, nil), session)
	_ = json.Unmarshal(result.body, &report)
	for _, row := range report.Capabilities {
		if row.ID == "settings_devices" && row.Enabled {
			t.Fatal("unconfigured private management advertised")
		}
	}
	request := httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"client_name":"legacy HTTP client"}`)).WithContext(f.ctx)
	request.Header.Set("Idempotency-Key", iscpTestRequest)
	response := httptest.NewRecorder()
	f.server.issueClient(response, request)
	if response.Code != 201 || strings.Contains(response.Body.String(), iscpnodeissuer.CredentialPrefix) || f.calls != 0 {
		t.Fatal("HTTP primitive recursed into node issuer")
	}
}
