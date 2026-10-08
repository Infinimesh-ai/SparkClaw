package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/envelope"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
)

// This substitutes only Relay delivery. Both endpoints run SDK cryptography,
// signed local grant verification, fixed-direction handshake and manifests.
// It does not claim hosted enrollment, PoP or real Relay network acceptance.
type workbenchEncryptedRelayNetwork struct {
	mu             sync.Mutex
	inbox          map[string]chan json.RawMessage
	envelopes      []json.RawMessage
	dropNextResult atomic.Bool
}

type workbenchEncryptedRelay struct {
	network *workbenchEncryptedRelayNetwork
	device  string
}

func (r *workbenchEncryptedRelay) Submit(ctx context.Context, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var frame envelope.SecureEnvelope
	if err := json.Unmarshal(raw, &frame); err != nil {
		return err
	}
	r.network.mu.Lock()
	r.network.envelopes = append(r.network.envelopes, append(json.RawMessage(nil), raw...))
	inbox := r.network.inbox[frame.RecipientDeviceID]
	r.network.mu.Unlock()
	if inbox == nil {
		return errors.New("unknown isolated Relay recipient")
	}
	if frame.PayloadType == iscpworkbench.ResponseType && r.network.dropNextResult.CompareAndSwap(true, false) {
		return nil
	}
	select {
	case inbox <- raw:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *workbenchEncryptedRelay) RunOnceReady(ctx context.Context, receive func(context.Context, json.RawMessage) error, ready func()) error {
	ready()
	for {
		select {
		case raw := <-r.network.inbox[r.device]:
			if err := receive(ctx, raw); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func workbenchEncryptedConfigurations(t *testing.T, binding *iscpworkbench.Binding) (iscpworkbench.Config, iscpworkbench.Config) {
	t.Helper()
	now := time.Now().UTC()
	provider := iscpcrypto.NewProvider()
	desktop, err := identity.NewDevice(provider, "isolated-domain", "iscp-desktop-device", now)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := identity.NewDevice(provider, "isolated-domain", "iscp-backend-device", now)
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := identity.NewDevice(provider, "isolated-test-issuer", "iscp-local-test-issuer", now)
	if err != nil {
		t.Fatal(err)
	}
	cloud, err := identity.NewDevice(provider, "isolated-cloud-trust", "iscp-cloud-trust", now)
	if err != nil {
		t.Fatal(err)
	}
	thumbprint, err := identity.Thumbprint(desktop.Identity)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := trust.SignGrant(provider, issuer, trust.Grant{GrantID: "local-test-desktop-grant", SubjectDeviceID: desktop.Identity.DeviceID, Audience: backend.Identity.DeviceID, ConfirmationThumbprint: thumbprint, Permissions: []string{iscpworkbench.Permission}, RelayConstraints: []string{"isolated-relay"}, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writeJSON := func(path string, value any) {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil || os.WriteFile(path, raw, 0600) != nil {
			t.Fatalf("write isolated material %s: %v", filepath.Base(path), err)
		}
	}
	makeConfig := func(local identity.Device, peer identity.DeviceIdentity, role string) iscpworkbench.Config {
		t.Helper()
		dir := filepath.Join(root, local.Identity.DeviceID)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		writeJSON(filepath.Join(dir, iscpbridge.IdentityFileName), local.Identity)
		key := base64.RawURLEncoding.EncodeToString(local.Private.BytesForDevStore())
		if err := os.WriteFile(filepath.Join(dir, iscpbridge.IdentityKeyFileName), []byte(key), 0600); err != nil {
			t.Fatal(err)
		}
		cfg := iscpworkbench.Config{SchemaVersion: 1, Mode: "local-test", Role: role, IdentityDirectory: dir, IdentityKeyBackend: iscpbridge.IdentityKeyBackendFile, EnrollmentFile: filepath.Join(dir, "enrollment.json"), PeerIdentityFile: filepath.Join(dir, "peer.json"), IssuerIdentityFile: filepath.Join(dir, "issuer.json"), GrantFile: filepath.Join(dir, "grant.json")}
		bundle := iscpbridge.EnrollmentBundle{Type: iscpbridge.EnrollmentBundleType, DomainID: local.Identity.DomainID, DeviceID: local.Identity.DeviceID, RelayID: "isolated-relay", RelayBaseURL: "https://isolated-relay.invalid", RelayWebSocketURL: "wss://isolated-relay.invalid/v2/relay/connect", TrustRootIdentity: cloud.Identity, Access: iscpbridge.RelayCredential{DomainID: local.Identity.DomainID, DeviceID: local.Identity.DeviceID, Token: "isolated-synthetic-access", ExpiresAt: now.Add(time.Hour)}, Refresh: iscpbridge.RelayCredential{DomainID: local.Identity.DomainID, DeviceID: local.Identity.DeviceID, Token: "isolated-synthetic-refresh", ExpiresAt: now.Add(time.Hour)}, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
		writeJSON(cfg.EnrollmentFile, bundle)
		writeJSON(cfg.PeerIdentityFile, peer)
		writeJSON(cfg.IssuerIdentityFile, issuer.Identity)
		writeJSON(cfg.GrantFile, grant)
		return cfg
	}
	clientConfig := makeConfig(desktop, backend.Identity, iscpworkbench.RoleInitiator)
	serverConfig := makeConfig(backend, desktop.Identity, iscpworkbench.RoleResponder)
	serverConfig.Binding = binding
	return clientConfig, serverConfig
}

func TestWorkbenchISCPEncryptedEndpointUsesRealGatewayAndRecoversLostSubmit(t *testing.T) {
	server, repository, localConfig, handler := workbenchISCPFixture(t, nil)
	clientConfig, serverConfig := workbenchEncryptedConfigurations(t, localConfig.Binding)
	network := &workbenchEncryptedRelayNetwork{inbox: map[string]chan json.RawMessage{"iscp-desktop-device": make(chan json.RawMessage, 32), "iscp-backend-device": make(chan json.RawMessage, 32)}}
	ready := make(chan struct{}, 4)
	client, err := iscpworkbench.NewEndpointWithRelay(clientConfig, &workbenchEncryptedRelay{network, "iscp-desktop-device"}, nil, func(state string) {
		if state == "transport_ready" {
			ready <- struct{}{}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	responder, err := iscpworkbench.NewEndpointWithRelay(serverConfig, &workbenchEncryptedRelay{network, "iscp-backend-device"}, handler, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	responderCtx, release, err := server.clientConnectionContext(ctx, localConfig.Binding.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	clientDone, responderDone := make(chan error, 1), make(chan error, 1)
	go func() { clientDone <- client.Run(ctx) }()
	go func() { responderDone <- responder.Run(responderCtx) }()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		_ = responder.Close()
		<-clientDone
	})
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("encrypted handshake and capability negotiation timed out")
	}
	call := func(operation string, body []byte) iscpworkbench.Response {
		t.Helper()
		response, err := client.Call(ctx, workbenchISCPRequest(operation, body))
		if err != nil {
			t.Fatalf("encrypted %s: %v", operation, err)
		}
		return response
	}
	if response := call(iscpworkbench.OperationIdentity, nil); response.Status != http.StatusOK || !bytes.Contains(response.Body, []byte("iscp-desktop-client")) {
		t.Fatalf("encrypted identity: %+v", response)
	}
	if response := call(iscpworkbench.OperationBind, []byte(`{"schema_version":1,"installation_id":"`+iscpTestInstallation+`"}`)); response.Status != http.StatusOK {
		t.Fatalf("encrypted installation binding: %+v", response)
	}
	raw, _ := json.MarshalIndent(workbenchISCPEnvelope(), "", "  ")
	network.dropNextResult.Store(true)
	lostCtx, stopLost := context.WithTimeout(ctx, 150*time.Millisecond)
	_, err = client.Call(lostCtx, workbenchISCPRequest(iscpworkbench.OperationSubmit, raw))
	stopLost()
	if err == nil {
		t.Fatal("isolated Relay did not drop the acceptance response")
	}
	server.executions.Wait()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("fresh desktop handshake did not recover the lost response")
	}
	if response := call(iscpworkbench.OperationIdentity, nil); response.Status != http.StatusOK {
		t.Fatalf("recovered identity: %+v", response)
	}
	if response := call(iscpworkbench.OperationBind, []byte(`{"schema_version":1,"installation_id":"`+iscpTestInstallation+`"}`)); response.Status != http.StatusOK {
		t.Fatalf("recovered installation binding: %+v", response)
	}
	response := call(iscpworkbench.OperationLookup, nil)
	var status execution.Status
	if err := json.Unmarshal(response.Body, &status); err != nil || response.Status != http.StatusOK || status.State != "completed" || status.Result == nil || status.InputDigest != execution.Digest(raw) {
		t.Fatalf("original request recovery: %+v %v", response, err)
	}
	if execution.Digest([]byte(status.Result.Payload)) != status.Result.Digest {
		t.Fatal("encrypted result digest does not match durable payload")
	}
	ack, _ := json.Marshal(map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true})
	if response := call(iscpworkbench.OperationAck, ack); response.Status != http.StatusOK {
		t.Fatalf("encrypted durable ACK: %+v", response)
	}
	response = call(iscpworkbench.OperationLookup, nil)
	if !bytes.Contains(response.Body, []byte(`"state":"delivered"`)) || bytes.Contains(response.Body, []byte(`"result"`)) {
		t.Fatalf("durable ACK projection: %+v", response)
	}
	ledger, err := os.ReadFile(filepath.Join(server.executionRoot, "control.json"))
	var control struct {
		Fences map[string]json.RawMessage `json:"fences"`
	}
	if err != nil || json.Unmarshal(ledger, &control) != nil || len(control.Fences) != 1 {
		t.Fatalf("lost-response recovery created duplicate admissions: %v", err)
	}
	network.mu.Lock()
	for _, raw := range network.envelopes {
		if bytes.Contains(raw, []byte("synthetic desktop original")) || bytes.Contains(raw, []byte("iscp-owner")) || bytes.Contains(raw, []byte("iscp-desktop-client")) {
			t.Error("isolated Relay observed plaintext business scope or content")
		}
	}
	network.mu.Unlock()
	if _, err := repository.RevokeClient(ctx, localConfig.Binding.ClientID); err != nil {
		t.Fatal(err)
	}
	server.cancelClientConnections(localConfig.Binding.ClientID)
	select {
	case <-responderDone:
	case <-ctx.Done():
		t.Fatal("Client revocation did not close encrypted responder session")
	}
	if response := handler(ctx, workbenchISCPRequest(iscpworkbench.OperationLookup, nil)); response.Status != http.StatusUnauthorized {
		t.Fatalf("revoked session still delivered: %+v", response)
	}
}
