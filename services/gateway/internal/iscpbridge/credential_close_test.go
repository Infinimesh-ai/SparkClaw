package iscpbridge

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	c "github.com/Infinimesh-ai/ISCP/pkg/iscp/crypto"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
)

func TestCredentialProviderCloseJoinsRecoveryAndInstallation(t *testing.T) {
	// The certificate is fixture material; no network requests are performed.
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}))
	for _, stage := range []string{"recovery", "flight_installation", "cached_installation"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "enrollment.json")
			synctest.Test(t, func(t *testing.T) {
				now := time.Now().UTC()
				device, _ := identity.NewDevice(c.NewProvider(), "domain", "device", now)
				signer, _ := identity.NewDevice(c.NewProvider(), "domain", "relay-signer", now)
				issuer, _ := identity.NewDevice(c.NewProvider(), "domain", "issuer", now)
				bundle := EnrollmentBundle{Type: EnrollmentBundleType, Mode: BundleModeWorkbenchNodeManaged, DomainID: "domain", DeviceID: "device", RelayID: "relay", RelayBaseURL: server.URL, RelayWebSocketURL: "wss" + strings.TrimPrefix(server.URL, "https") + "/v2/relay/duplex", RelaySignerIdentity: &signer.Identity, InvitationSignerIdentity: &issuer.Identity, TLSCertificatePEM: cert, IssuedAt: now, ExpiresAt: now.Add(time.Hour), Access: RelayCredential{"domain", "device", "access", now.Add(time.Hour)}, Refresh: RelayCredential{"domain", "device", "refresh", now.Add(time.Hour)}}
				binding := CredentialBinding{DomainID: "domain", DeviceID: "device", ResourceBindingID: "client", TargetServiceID: "relay", IssuerPublicKey: issuer.Identity.PublicKey.Public, RelayPublicKey: signer.Identity.PublicKey.Public, TLSCertificateSHA256: c.Base64URL(c.SHA256([]byte(cert)))}
				relay, err := NewRelayCredentialClient(ProfileProduction, path, bundle, device, time.Second)
				if err != nil {
					t.Fatal(err)
				}
				started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
				finish := sync.OnceFunc(func() { close(release) })
				var writes atomic.Int32
				var recoveries, installations atomic.Int32
				finalize := func(ctx context.Context) error {
					close(started)
					<-ctx.Done()
					close(canceled)
					<-release
					// Model the pending-control checkpoint that can finish after
					// cancellation, but must finish before owner shutdown returns.
					if err := os.WriteFile(path+".finalized", []byte("owned cleanup"), 0600); err != nil {
						t.Error(err)
					}
					writes.Add(1)
					return ctx.Err()
				}
				err = relay.ConfigureCredentialProviderWithInstallation(binding, func(ctx context.Context, _ CredentialBinding, _ CredentialGeneration) (CredentialLease, error) {
					recoveries.Add(1)
					if stage == "recovery" {
						return CredentialLease{}, finalize(ctx)
					}
					return CredentialLease{Binding: binding, Enrollment: bundle, CredentialGeneration: "1", AuthorizationRevision: "1", ExpiresAt: bundle.Access.ExpiresAt}, nil
				}, func(ctx context.Context, _ CredentialLease) error {
					call := installations.Add(1)
					if stage == "cached_installation" && call == 1 {
						return nil
					}
					return finalize(ctx)
				})
				if err != nil {
					t.Fatal(err)
				}
				provider := relay.CredentialProvider()
				events := provider.Subscribe(t.Context(), binding)
				defer provider.Close()
				defer finish()
				if stage == "cached_installation" {
					if _, err := provider.EnsureAccess(t.Context(), binding); err != nil {
						t.Fatal(err)
					}
				}
				waiter, cancelWaiter := context.WithCancel(context.Background())
				defer cancelWaiter()
				result := make(chan error, 1)
				go func() { _, err := provider.EnsureAccess(waiter, binding); result <- err }()
				select {
				case <-started:
				case err := <-result:
					t.Fatalf("operation did not reach %s: %v", stage, err)
				}
				if stage != "cached_installation" {
					cancelWaiter()
					if err := <-result; !errors.Is(err, context.Canceled) {
						t.Errorf("waiter cancellation = %v", err)
					}
				}
				const owners = 8
				closed := make(chan error, owners)
				for range owners {
					go func() { closed <- provider.Close() }()
				}
				synctest.Wait()
				if len(closed) != 0 {
					t.Errorf("%d owner Close calls returned before %s finalized", len(closed), stage)
				}
				select {
				case <-canceled:
				default:
					t.Errorf("owner Close did not cancel %s", stage)
					cancelWaiter() // Release the old cached path after recording its failure.
					synctest.Wait()
				}
				// While every Close is joining, new callers must be rejected and
				// must not start another recovery or installation callback.
				rejected := make(chan error, owners)
				for range owners {
					go func() { _, err := provider.EnsureAccess(t.Context(), binding); rejected <- err }()
				}
				synctest.Wait()
				for range owners {
					if err := <-rejected; !errors.Is(err, context.Canceled) {
						t.Errorf("access after owner cancellation = %v", err)
					}
				}
				finish()
				for range owners {
					if err := <-closed; err != nil {
						t.Error(err)
					}
				}
				if stage == "cached_installation" {
					if err := <-result; !errors.Is(err, context.Canceled) {
						t.Errorf("cached installation cancellation = %v", err)
					}
				}
				synctest.Wait()
				if writes.Load() != 1 || recoveries.Load() != 1 {
					t.Errorf("shutdown writes=%d recoveries=%d", writes.Load(), recoveries.Load())
				}
				if err := provider.Close(); err != nil {
					t.Errorf("repeated Close = %v", err)
				}
				for range events {
				}
				if _, ok := <-provider.Subscribe(t.Context(), binding); ok {
					t.Error("closed provider accepted a subscription")
				}
				if err := os.RemoveAll(filepath.Dir(path)); err != nil {
					t.Fatal(err)
				}
				if _, err := provider.EnsureAccess(t.Context(), binding); !errors.Is(err, context.Canceled) {
					t.Errorf("closed provider access = %v", err)
				}
				if err := provider.Invalidate(binding, "1", RotationRequired); !errors.Is(err, context.Canceled) {
					t.Errorf("closed provider invalidation = %v", err)
				}
				if _, err := os.Stat(filepath.Dir(path)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("closed provider recreated storage: %v", err)
				}
			})
		})
	}
}

func TestCredentialProviderCloseCancelsCheckpointLockWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "enrollment.json")
	synctest.Test(t, func(t *testing.T) {
		root, cancel := context.WithCancel(context.Background())
		defer cancel()
		provider := &managedCredentialProvider{
			root: root, cancel: cancel, path: path + ".credentials.v2",
			relay: &RelayClient{enrollmentPath: path},
			recovery: func(ctx context.Context, _ CredentialBinding, _ CredentialGeneration) (CredentialLease, error) {
				<-ctx.Done()
				return CredentialLease{}, ctx.Err()
			},
		}
		unlock, err := lockCredentialFile(root, provider.path+".lock")
		if err != nil {
			t.Fatal(err)
		}
		release := sync.OnceFunc(unlock)
		defer release()
		result := make(chan error, 1)
		go func() { _, err := provider.EnsureAccess(context.Background(), CredentialBinding{}); result <- err }()
		synctest.Wait()
		closed := make(chan struct{})
		go func() { _ = provider.Close(); close(closed) }()
		synctest.Wait()
		select {
		case <-closed:
		default:
			t.Error("owner Close waited for the external checkpoint lock instead of canceling its waiter")
		}
		release()
		<-closed
		if err := <-result; !errors.Is(err, context.Canceled) {
			t.Errorf("checkpoint lock waiter = %v", err)
		}
	})
}
