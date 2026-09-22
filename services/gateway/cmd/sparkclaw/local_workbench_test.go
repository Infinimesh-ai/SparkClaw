package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDesktopTokenHashMatchesGatewayClientBearerFormat(t *testing.T) {
	const token = "desktop-client-token-that-is-long-enough"
	encoded := desktopTokenHash(token)
	if _, err := base64.RawURLEncoding.DecodeString(encoded); err != nil {
		t.Fatalf("desktop token hash is not base64url: %v", err)
	}
	repository := store.NewMemoryStore()
	client, err := repository.RegisterClient(t.Context(), provisionedClientFixture(token))
	if err != nil {
		t.Fatal(err)
	}
	if client.TokenHash != encoded {
		t.Fatalf("registered token hash = %q, want %q", client.TokenHash, encoded)
	}
	found, ok, err := repository.FindClientByTokenHash(t.Context(), encoded)
	if err != nil || !ok || found.ID != client.ID {
		t.Fatalf("bearer hash lookup = (%#v, %v, %v)", found, ok, err)
	}
}

func TestProvisionedDesktopClientReplaysAndNeverReactivatesRevocation(t *testing.T) {
	repository := store.NewMemoryStore()
	cfg := desktopProvisioningFixture(t, "desktop-client-token-that-is-long-enough")
	if err := registerProvisionedDesktopClient(t.Context(), cfg, repository); err != nil {
		t.Fatal(err)
	}
	if err := registerProvisionedDesktopClient(t.Context(), cfg, repository); err != nil {
		t.Fatalf("idempotent replay failed: %v", err)
	}
	clients, err := repository.ListClients(t.Context())
	if err != nil || len(clients) != 1 {
		t.Fatalf("desktop replay clients=%#v err=%v", clients, err)
	}
	if _, err := repository.RevokeClient(t.Context(), "client-desktop"); err != nil {
		t.Fatal(err)
	}
	if err := registerProvisionedDesktopClient(t.Context(), cfg, repository); err == nil {
		t.Fatal("revoked desktop Client was reactivated")
	}
	revoked, found, err := repository.GetClient(t.Context(), "client-desktop")
	if err != nil || !found || revoked.RevokedAt == nil {
		t.Fatalf("revoked desktop Client changed: %#v found=%v err=%v", revoked, found, err)
	}
}

func TestProvisionedDesktopClientReconcilesUnknownOutcome(t *testing.T) {
	repository := store.NewMemoryStore()
	cfg := desktopProvisioningFixture(t, "desktop-client-token-that-is-long-enough")
	wrapped := &unknownOutcomeDesktopBackend{desktopClientRepository: repository}
	if err := registerProvisionedDesktopClient(t.Context(), cfg, wrapped); err != nil {
		t.Fatalf("unknown outcome was not reconciled: %v", err)
	}
	clients, err := repository.ListClients(t.Context())
	if err != nil || len(clients) != 1 {
		t.Fatalf("unknown outcome clients=%#v err=%v", clients, err)
	}
}

type unknownOutcomeDesktopBackend struct {
	desktopClientRepository
}

func (b *unknownOutcomeDesktopBackend) RegisterClient(ctx context.Context, client app.Client) (app.Client, error) {
	if _, err := b.desktopClientRepository.RegisterClient(ctx, client); err != nil {
		return app.Client{}, err
	}
	return app.Client{}, &store.StoreError{Operation: store.OperationClientRegister, Code: store.StoreErrorUnknownOutcome, Err: errors.New("commit outcome unknown")}
}

func desktopProvisioningFixture(t *testing.T, token string) config.Config {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(directory, "desktop-client.json")
	raw, err := json.Marshal(desktopClientProvisioning{
		SchemaVersion: 1,
		DeploymentID:  "deployment-test",
		ClientID:      "client-desktop",
		OwnerID:       app.DefaultOwnerID,
		ActorID:       app.DefaultOwnerID,
		ClientName:    "SparkClaw Desktop",
		Token:         token,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Gateway.DeploymentID = "deployment-test"
	cfg.Gateway.DesktopClientFile = filename
	return cfg
}

func provisionedClientFixture(token string) app.Client {
	return app.Client{
		ID: "client-desktop", OwnerID: "owner", ActorID: "owner", Name: "Desktop",
		TokenHash: desktopTokenHash(token),
	}
}
