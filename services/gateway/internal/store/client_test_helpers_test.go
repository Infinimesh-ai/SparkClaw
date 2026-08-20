package store

import (
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustClaimTestClient(t testing.TB, repository ClientRepository, client app.Client) app.Client {
	t.Helper()
	if client.ID == "" {
		client.ID = app.NewID("client_test")
	}
	if client.Name == "" {
		client.Name = "Test Client"
	}
	if client.TokenHash == "" {
		client.TokenHash = app.NewID("client_hash")
	}
	pairing, err := repository.SavePairingCode(t.Context(), app.PairingCode{
		ID: app.NewID("pair_test"), CodeHash: app.NewID("pair_hash"), Status: "pending",
		ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("save test pairing code: %v", err)
	}
	_, claimed, err := repository.ClaimPairingCode(t.Context(), pairing.ID, client)
	if err != nil {
		t.Fatalf("claim test client: %v", err)
	}
	return claimed
}
