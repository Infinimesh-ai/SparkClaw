package iscpbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

const (
	testOldOrigin = "https://203.0.113.7"
	testNewOrigin = "https://203.0.113.7:18443"
)

type originFixture struct {
	device  identity.Device
	bundle  EnrollmentBundle
	binding CredentialBinding
	runtime string // path the service opens (AAD), e.g. a container mount
	host    string // the same file as seen by the maintenance host
}

// newOriginFixture seals a real lease with the managed provider at the
// runtime path, then exposes the files at a different host path.
func newOriginFixture(t *testing.T) originFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	device, _ := identity.NewDevice(c.NewProvider(), "domain", "device", now)
	signer, _ := identity.NewDevice(c.NewProvider(), "domain", "relay-signer", now)
	inviter, _ := identity.NewDevice(c.NewProvider(), "domain", "inviter", now)
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	bundle := EnrollmentBundle{Type: EnrollmentBundleType, Mode: BundleModeWorkbenchNodeManaged, DomainID: "domain", DeviceID: "device", RelayID: "relay",
		RelayBaseURL: testOldOrigin, RelayWebSocketURL: NodeRelayWebSocketURL(testOldOrigin), RelaySignerIdentity: &signer.Identity, InvitationSignerIdentity: &inviter.Identity,
		TLSCertificatePEM: certificate, IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Access: RelayCredential{"domain", "device", "access", now.Add(30 * time.Minute)}, Refresh: RelayCredential{"domain", "device", "refresh", now.Add(time.Hour)}}
	binding := CredentialBinding{DomainID: "domain", DeviceID: "device", ResourceBindingID: "client", TargetServiceID: "relay", IssuerPublicKey: inviter.Identity.PublicKey.Public,
		RelayPublicKey: signer.Identity.PublicKey.Public, TLSCertificateSHA256: c.Base64URL(c.SHA256([]byte(certificate)))}
	f := originFixture{device: device, bundle: bundle, binding: binding,
		runtime: filepath.Join(t.TempDir(), "run", "enrollment.json"), host: filepath.Join(t.TempDir(), "host", "enrollment.json")}
	if err := SaveEnrollment(f.runtime, bundle); err != nil {
		t.Fatal(err)
	}
	provider := f.provider(t, bundle).(*managedCredentialProvider)
	lease := CredentialLease{Binding: binding, Enrollment: bundle, CredentialGeneration: "4", AuthorizationRevision: "7", ExpiresAt: bundle.Access.ExpiresAt, StateVersion: 9}
	if err := provider.persist(credentialState{Binding: binding, Lease: &lease, Generation: "4", AuthorizationRevision: "7", Version: 9}); err != nil {
		t.Fatal(err)
	}
	provider.Close()
	f.copyFiles(t, f.runtime, f.host)
	return f
}

func (f originFixture) provider(t *testing.T, bundle EnrollmentBundle) CredentialProvider {
	t.Helper()
	client, err := NewRelayCredentialClient(ProfileProduction, f.runtime, bundle, f.device, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	recover := func(context.Context, CredentialBinding, CredentialGeneration) (CredentialLease, error) {
		t.Fatal("migration must not trigger credential recovery")
		return CredentialLease{}, nil
	}
	if err = client.ConfigureCredentialProvider(f.binding, recover); err != nil {
		t.Fatal(err)
	}
	return client.CredentialProvider()
}

func (f originFixture) copyFiles(t *testing.T, from, to string) {
	t.Helper()
	for _, suffix := range []string{"", ".credentials.v2"} {
		raw, err := os.ReadFile(from + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if err = writeSecretFile(to+suffix, raw); err != nil {
			t.Fatal(err)
		}
	}
}

func (f originFixture) migrate(dryRun bool) (RelayOriginMigrationResult, error) {
	return MigrateRelayOrigin(context.Background(), RelayOriginMigration{EnrollmentFile: f.host, RuntimeEnrollmentFile: f.runtime, Device: f.device,
		FromBaseURL: testOldOrigin, BaseURL: testNewOrigin, DryRun: dryRun})
}

func TestMigrateRelayOriginRetainsSealedCASAndReloadsAtRuntimePath(t *testing.T) {
	f := newOriginFixture(t)
	got, err := f.migrate(false)
	if err != nil || got != (RelayOriginMigrationResult{EnrollmentChanged: true, StatePresent: true, LeasePresent: true, StateChanged: true}) {
		t.Fatalf("migration %+v %v", got, err)
	}
	f.copyFiles(t, f.host, f.runtime)
	loaded, err := readNodeEnrollment(f.runtime)
	if err != nil || loaded.RelayBaseURL != testNewOrigin || loaded.RelayWebSocketURL != "wss://203.0.113.7:18443/v2/relay/duplex" {
		t.Fatalf("enrollment %+v %v", loaded, err)
	}
	if loaded.Access != f.bundle.Access || loaded.Refresh != f.bundle.Refresh || loaded.TLSCertificatePEM != f.bundle.TLSCertificatePEM {
		t.Fatal("migration changed credentials or pins")
	}
	// The real provider opens the re-sealed state at the original path and
	// installs the moved lease without recovery or a version change.
	provider := f.provider(t, loaded).(*managedCredentialProvider)
	defer provider.Close()
	state := provider.state
	if state.Version != 9 || state.Generation != "4" || state.AuthorizationRevision != "7" || state.Lease == nil || state.Lease.StateVersion != 9 ||
		state.Lease.Enrollment.RelayBaseURL != testNewOrigin || state.Lease.Enrollment.Access != f.bundle.Access || provider.relay.Enrollment().RelayBaseURL != testNewOrigin {
		t.Fatalf("reloaded state %+v", state)
	}
	again, err := f.migrate(false)
	if err != nil || again != (RelayOriginMigrationResult{StatePresent: true, LeasePresent: true}) {
		t.Fatalf("repeat %+v %v", again, err)
	}
}

func TestMigrateRelayOriginRefusesMismatchesWithoutWriting(t *testing.T) {
	f := newOriginFixture(t)
	before := snapshotFiles(t, f.host)
	for name, m := range map[string]RelayOriginMigration{
		"wrong original":   {FromBaseURL: "https://203.0.113.7:8443", BaseURL: testNewOrigin},
		"other host":       {FromBaseURL: testOldOrigin, BaseURL: "https://198.51.100.1:18443"},
		"same origin":      {FromBaseURL: testOldOrigin, BaseURL: testOldOrigin},
		"default port":     {FromBaseURL: testOldOrigin, BaseURL: "https://203.0.113.7:443"},
		"host path as aad": {FromBaseURL: testOldOrigin, BaseURL: testNewOrigin, RuntimeEnrollmentFile: f.host},
	} {
		m.EnrollmentFile, m.Device = f.host, f.device
		if m.RuntimeEnrollmentFile == "" {
			m.RuntimeEnrollmentFile = f.runtime
		}
		if _, err := MigrateRelayOrigin(context.Background(), m); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	other, _ := identity.NewDevice(c.NewProvider(), "domain", "device", time.Now())
	if _, err := MigrateRelayOrigin(context.Background(), RelayOriginMigration{EnrollmentFile: f.host, RuntimeEnrollmentFile: f.runtime, Device: other, FromBaseURL: testOldOrigin, BaseURL: testNewOrigin}); err == nil {
		t.Fatal("foreign device key accepted")
	}
	if dry, err := f.migrate(true); err != nil || !dry.StateChanged || !dry.EnrollmentChanged {
		t.Fatalf("dry run %+v %v", dry, err)
	}
	if !bytes.Equal(snapshotFiles(t, f.host), before) {
		t.Fatal("refused or dry migration wrote files")
	}
}

func TestMigrateRelayOriginKeepsUnknownFieldsAndResumesPartialRun(t *testing.T) {
	f := newOriginFixture(t)
	// A field this build does not model must survive (other nodes may run a
	// different release that wrote it).
	raw, _ := os.ReadFile(f.host)
	var bundle map[string]any
	_ = json.Unmarshal(raw, &bundle)
	bundle["future_field"] = map[string]any{"kept": true}
	raw, _ = json.Marshal(bundle)
	if err := writeSecretFile(f.host, raw); err != nil {
		t.Fatal(err)
	}
	// Interrupted after the sealed state moved: the enrollment still has the
	// original origin and a repeat completes it.
	state, _ := os.ReadFile(f.host + ".credentials.v2")
	if _, err := f.migrate(false); err != nil {
		t.Fatal(err)
	}
	moved, _ := os.ReadFile(f.host + ".credentials.v2")
	if err := writeSecretFile(f.host, raw); err != nil {
		t.Fatal(err)
	}
	got, err := f.migrate(false)
	if err != nil || got != (RelayOriginMigrationResult{EnrollmentChanged: true, StatePresent: true, LeasePresent: true}) || bytes.Equal(state, moved) {
		t.Fatalf("resume %+v %v", got, err)
	}
	raw, _ = os.ReadFile(f.host)
	_ = json.Unmarshal(raw, &bundle)
	if bundle["relay_base_url"] != testNewOrigin || bundle["future_field"] == nil {
		t.Fatalf("bundle %v", bundle)
	}
}

func snapshotFiles(t *testing.T, path string) []byte {
	t.Helper()
	var out []byte
	for _, suffix := range []string{"", ".credentials.v2"} {
		raw, err := os.ReadFile(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		out = append(append(out, raw...), 0)
	}
	return out
}

func readNodeEnrollment(path string) (EnrollmentBundle, error) {
	var bundle EnrollmentBundle
	raw, err := os.ReadFile(path)
	if err == nil {
		err = strictUnmarshal(raw, &bundle)
	}
	if err == nil {
		err = bundle.ValidateCredentials(time.Now())
	}
	return bundle, err
}
