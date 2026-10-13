package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestClientDeletionHTTPIsOwnerScopedRevokedOnlyAndIdempotent(t *testing.T) {
	s, repository := credentialServer(t)
	registerCredential(t, repository, "manager", "manager-test-token")
	registerCredential(t, repository, "old-device", "old-device-test-token")
	if _, err := repository.RegisterClient(t.Context(), app.Client{ID: "foreign-device", OwnerID: "other-owner", Name: "foreign", TokenHash: hashSecret("foreign-test-token")}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.RevokeClient(t.Context(), "foreign-device"); err != nil {
		t.Fatal(err)
	}
	network := httptest.NewServer(s.Handler())
	defer network.Close()
	check := func(method, path, token string, want int) {
		t.Helper()
		response := credentialRequest(t, method, network.URL+path, token, "", "")
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("%s %s = %d, want %d", method, path, response.StatusCode, want)
		}
	}
	check("DELETE", "/api/clients/old-device", "", 401)
	check("DELETE", "/api/clients/old-device", "manager-test-token", 409)
	check("DELETE", "/api/clients/foreign-device", "manager-test-token", 404)
	if _, err := repository.RevokeClient(t.Context(), "old-device"); err != nil {
		t.Fatal(err)
	}
	check("DELETE", "/api/clients/old-device", "manager-test-token", 200)
	check("DELETE", "/api/clients/old-device", "manager-test-token", 200)
	check("GET", "/api/workbench/identity", "old-device-test-token", 401)
	response := credentialRequest(t, "GET", network.URL+"/api/clients", "manager-test-token", "", "")
	defer response.Body.Close()
	var result struct {
		Clients []app.Client `json:"clients"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || len(result.Clients) != 1 || result.Clients[0].ID != "manager" {
		t.Fatalf("list after deletion = %#v err=%v", result, err)
	}
}

func TestClientDeletionISCPPreservesNodeRevocationAndSupportsReplay(t *testing.T) {
	f := nodeClientsTestFixture(t)
	if response := f.call(iscpworkbench.OperationClientsIssue, `{"client_name":"second desktop"}`); response.Status != 200 {
		t.Fatal(response)
	}
	remove := v2Request(iscpworkbench.OperationClientsDelete, nil)
	remove.Params = map[string]string{"id": "node-client"}
	if response := f.handler(f.ctx, remove); response.Status != 409 {
		t.Fatalf("active deletion = %+v", response)
	}
	revoke := v2Request(iscpworkbench.OperationClientsRevoke, []byte(`{}`))
	revoke.Params = remove.Params
	if response := f.handler(f.ctx, revoke); response.Status != 200 {
		t.Fatalf("node revocation = %+v", response)
	}
	for range 2 {
		if response := f.handler(f.ctx, remove); response.Status != 200 {
			t.Fatalf("deletion replay = %+v", response)
		}
	}
	clients, err := f.server.store.ListClients(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range clients {
		if client.ID == "node-client" {
			t.Fatal("deleted node remains in management")
		}
	}
	if !f.records[0].RevocationComplete || f.records[0].State != "revoked" || f.revocations != 1 {
		t.Fatal("deletion changed node authorization or repeated revocation")
	}
	remove.Params["owner_id"] = "other-owner"
	if response := f.handler(f.ctx, remove); response.Status != 400 {
		t.Fatalf("broadened deletion = %+v", response)
	}
}
