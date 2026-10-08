package iscplocalenroll

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	iscpcrypto "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/descriptor"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

func TestLocalEnrollmentSignedDiscoveryAndProofWithDockerAliases(t *testing.T) {
	provider := iscpcrypto.NewProvider()
	now := time.Now().UTC()
	signer, err := identity.NewDevice(provider, "local-domain", "local-relay-signer", now)
	if err != nil {
		t.Fatal(err)
	}
	var signerValue atomic.Value
	signerValue.Store(signer)
	var binds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signer := signerValue.Load().(identity.Device)
		switch r.URL.Path {
		case "/.well-known/iscp/relay":
			desc := descriptor.RelayDescriptor{Type: "iscp.relay.descriptor.v2", RelayID: "local-relay", DomainID: "local-domain", BaseURL: "http://iscp-relay:8080", WebSocketURL: "ws://iscp-relay:8080/v2/relay/connect", SigningKeys: []descriptor.PublicKey{{KTY: "Ed25519", Use: "descriptor-signature", KID: signer.Identity.PublicKey.KID, Public: signer.Identity.PublicKey.Public}}, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
			signed, err := descriptor.Sign(provider, signer, desc.Type, desc, now)
			if err != nil {
				t.Error(err)
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"descriptor": signed})
		case "/v2/relay/devices/bind-self":
			var request struct {
				Identity identity.DeviceIdentity `json:"identity"`
				Proof    identity.DeviceProof    `json:"proof"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil {
				t.Error("invalid bind-self request")
				w.WriteHeader(400)
				return
			}
			if err := identity.VerifyProof(provider, request.Identity, request.Proof, "local-relay", request.Proof.Challenge, time.Now().UTC(), time.Minute); err != nil {
				t.Error(err)
				w.WriteHeader(401)
				return
			}
			binds.Add(1)
			credential := func(token string) map[string]any {
				return map[string]any{"domain_id": request.Identity.DomainID, "device_id": request.Identity.DeviceID, "token": token, "expires_at": now.Add(time.Hour), "revoked": false}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access": credential("real-fixture-issued-access"), "refresh": credential("real-fixture-issued-refresh")})
		default:
			t.Errorf("unexpected endpoint (cloud discovery must never run): %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	opts := Options{RelayURL: server.URL, RelayID: "local-relay", DomainID: "local-domain", DeviceID: "desktop-device", IdentityDirectory: filepath.Join(root, "desktop"), EnrollmentFile: filepath.Join(root, "desktop-enrollment.json")}
	summary, err := Enroll(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RelayURL != server.URL || summary.Profile != iscpbridge.ProfileLocalLab || summary.RelaySignerThumbprint != signer.Identity.PublicKey.KID {
		t.Fatalf("invalid enrollment summary: %+v", summary)
	}
	bundle, err := loadEnrollment(opts.EnrollmentFile)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Mode != iscpbridge.BundleModeWorkbenchLocalLab || bundle.TrustRootIdentity.DeviceID != "" || bundle.RelaySignerIdentity == nil || bundle.Access.Token != "real-fixture-issued-access" {
		t.Fatal("reference enrollment invented cloud trust or lost issued credentials")
	}
	for _, path := range []string{opts.EnrollmentFile, filepath.Join(opts.IdentityDirectory, iscpbridge.IdentityKeyFileName), filepath.Join(opts.IdentityDirectory, iscpbridge.IdentityFileName)} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("private material permissions are not0600")
		}
	}
	firstKey := summary.DeviceThumbprint
	opts.RuntimeRelayURL = "http://iscp-relay:8080"
	opts.RuntimeWebSocketURL = "ws://iscp-relay:8080/v2/relay/connect"
	summary, err = Enroll(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DeviceThumbprint != firstKey || summary.RelayURL != opts.RuntimeRelayURL || binds.Load() != 2 {
		t.Fatal("reenrollment changed identity or ignored explicit runtime alias")
	}
	rotated, err := identity.NewDevice(provider, "local-domain", "local-relay-signer", now)
	if err != nil {
		t.Fatal(err)
	}
	signerValue.Store(rotated)
	if _, err := Enroll(context.Background(), opts); err == nil {
		t.Fatal("changed Relay signer silently trusted")
	}
	if binds.Load() != 2 {
		t.Fatal("changed Relay signer reached enrollment")
	}
	wrong := opts
	wrong.DomainID = "wrong-domain"
	wrong.IdentityDirectory = filepath.Join(root, "wrong")
	if _, err := Enroll(context.Background(), wrong); err == nil {
		t.Fatal("wrong Domain accepted")
	}
	if _, err := os.Stat(wrong.IdentityDirectory); !os.IsNotExist(err) {
		t.Fatal("wrong Domain generated identity before discovery rejection")
	}
}
