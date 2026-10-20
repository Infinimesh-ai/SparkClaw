package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/connector"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/identity"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

func mcpSettingsFixture(t *testing.T) (*Server, *store.FileStore, iscpworkbench.Config, iscpworkbench.Handler) {
	t.Helper()
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	server.cfg.MCPAccess.LocalDomainID = "mcp-settings-domain"
	registry := connector.NewRegistry(server.cfg, repo)
	if err := registry.Register(connector.Registration{
		Channel: "mcp", SetupKind: app.ConnectorSetupExternal, Binding: mcpaccess.ConnectorAdapter{},
		Provider: mcpaccess.NewProvider(repo), ExternalManaged: true,
	}); err != nil {
		t.Fatal(err)
	}
	server.connectors = registry
	server.mcpAccess = mcpaccess.New(repo, nil, nil).WithChannelEnabled(func(owner string) bool { return registry.Enabled(owner, "mcp") })
	if _, err := registry.SetEnabled(t.Context(), "iscp-owner", "iscp-actor", "mcp", true, 0); err != nil {
		t.Fatal(err)
	}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return server, repo, cfg, handler
}

func TestISCPDomainMCPSettingsRejectsUntypedInput(t *testing.T) {
	adapter := &iscpDomainAdapter{server: &Server{}}
	tests := []struct {
		name, operation, body string
		params                map[string]string
	}{
		{"null read", "settings.mcp.pairing.status", `null`, nil},
		{"null ticket issue", "settings.mcp.tickets.issue", `null`, nil},
		{"null record mutation", "settings.mcp.tickets.revoke", `null`, map[string]string{"ticket_id": "ticket"}},
		{"array body", "settings.mcp.tickets.issue", `[]`, nil},
		{"scalar body", "settings.mcp.tickets.issue", `"ticket"`, nil},
		{"boolean body", "settings.mcp.catalog", `false`, nil},
		{"whitespace body", "settings.mcp.catalog", " \r\n ", nil},
		{"read body", "settings.mcp.catalog", `{"url":"https://other.invalid"}`, nil},
		{"read params", "settings.mcp.tickets.list", `{}`, map[string]string{"owner_id": "other"}},
		{"missing ticket", "settings.mcp.tickets.revoke", `{}`, nil},
		{"wrong binding key", "settings.mcp.bindings.delete", `{}`, map[string]string{"id": "binding"}},
		{"extra path key", "settings.mcp.tickets.delete", `{}`, map[string]string{"ticket_id": "ticket", "method": "GET"}},
		{"escaped path", "settings.mcp.tickets.delete", `{}`, map[string]string{"ticket_id": "..%2fother"}},
		{"slash path", "settings.mcp.bindings.revoke", `{}`, map[string]string{"binding_id": "../other"}},
		{"record body", "settings.mcp.records.delete", `{"owner_id":"other"}`, nil},
		{"missing transport version", "settings.mcp.transports", `{"iscp_enabled":true,"lan_access_enabled":false}`, nil},
		{"negative version", "settings.mcp.transports", `{"iscp_enabled":true,"lan_access_enabled":false,"expected_version":-1}`, nil},
		{"unknown transport", "settings.mcp.transports", `{"iscp_enabled":true,"lan_access_enabled":false,"expected_version":1,"url":"x"}`, nil},
		{"unknown issue field", "settings.mcp.tickets.issue", `{"owner_id":"other"}`, nil},
		{"issue expiry overflow", "settings.mcp.tickets.issue", `{"ttl_seconds":9223372036854775807}`, nil},
		{"short issue expiry", "settings.mcp.tickets.issue", `{"ttl_seconds":59}`, nil},
		{"issue params", "settings.mcp.tickets.issue", `{}`, map[string]string{"ticket_id": "ticket"}},
		{"unknown pairing field", "settings.mcp.pairing.start", `{"display_name":"client","authority_host":"other"}`, nil},
		{"empty pairing name", "settings.mcp.pairing.start", `{"display_name":"  "}`, nil},
		{"pairing name exceeds backend byte limit", "settings.mcp.pairing.start", `{"display_name":"` + strings.Repeat("客", 41) + `"}`, nil},
		{"pairing control chars", "settings.mcp.pairing.start", `{"display_name":"client\nother"}`, nil},
		{"long pairing expiry", "settings.mcp.pairing.start", `{"display_name":"client","ttl_seconds":1801}`, nil},
		{"trailing data", "settings.mcp.pairing.status", `{} {}`, nil},
		{"oversized body", "settings.mcp.pairing.start", strings.Repeat(" ", 4097), nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := domainTestRequest(test.operation, []byte(test.body))
			request.Params = test.params
			if result := adapter.mcpSettings(domainTestContext(t), request); result.status != http.StatusBadRequest {
				t.Fatalf("untyped request reached business handler: %d %s", result.status, result.body)
			}
		})
	}
	if result := adapter.mcpSettings(domainTestContext(t), domainTestRequest("settings.mcp.arbitrary", nil)); result.status != http.StatusNotImplemented {
		t.Fatalf("unknown operation was accepted: %d", result.status)
	}
}

func TestISCPDomainMCPSettingsTicketsPersistAndRecoverOneIssuance(t *testing.T) {
	server, repo, cfg, handler := mcpSettingsFixture(t)
	ctx := domainTestContext(t)
	transports := domainTestRequest("settings.mcp.transports", []byte(`{"iscp_enabled":true,"lan_access_enabled":true,"expected_version":1}`))
	if result := handler(ctx, transports); result.Status != http.StatusOK {
		t.Fatalf("update transports: %d %s", result.Status, result.Body)
	}
	if result := handler(ctx, transports); result.Status != http.StatusOK {
		t.Fatalf("replay transports: %d", result.Status)
	}
	current, err := server.connectors.Status(ctx, "iscp-owner", "mcp")
	if err != nil || current.Version != 2 || !current.ISCPEnabled || !current.LANAccessEnabled {
		t.Fatalf("transport write did not apply once: %+v %v", current, err)
	}
	stale := domainTestRequest(transports.Operation, transports.Body)
	if result := handler(ctx, stale); result.Status != http.StatusConflict {
		t.Fatalf("stale transport write: %d", result.Status)
	}
	catalog := handler(ctx, domainTestRequest("settings.mcp.catalog", nil))
	if catalog.Status != http.StatusOK || !bytes.Contains(catalog.Body, []byte(`"transport_version":2`)) {
		t.Fatalf("catalog omitted effective transports: %d %s", catalog.Status, catalog.Body)
	}
	issue := domainTestRequest("settings.mcp.tickets.issue", []byte(`{"ttl_seconds":60}`))
	first := handler(ctx, issue)
	var issued mcpaccess.IssuedTicket
	if first.Status != http.StatusCreated || json.Unmarshal(first.Body, &issued) != nil || issued.Secret == "" {
		t.Fatalf("ticket issuance failed: %d", first.Status)
	}
	journal, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	restarted := (&iscpDomainAdapter{server: server, config: cfg, receipts: journal}).handle
	issue.ID = domainTestRequest("new-transport-request", nil).ID
	replayed := restarted(ctx, issue)
	if replayed.Status != http.StatusCreated || !bytes.Equal(replayed.Body, first.Body) {
		t.Fatalf("lost issuance response was not recovered: %d", replayed.Status)
	}
	tickets, err := repo.ListMCPAccessTickets(ctx, "iscp-owner")
	if err != nil || len(tickets) != 1 || tickets[0].SecretHash == "" || tickets[0].SecretHash == issued.Secret || tickets[0].ActorID != "iscp-actor" {
		t.Fatalf("ticket persistence or owner attribution failed: count=%d err=%v", len(tickets), err)
	}
	list := handler(ctx, domainTestRequest("settings.mcp.tickets.list", nil))
	if list.Status != http.StatusOK || bytes.Contains(list.Body, []byte(issued.Secret)) || bytes.Contains(list.Body, []byte("secret_hash")) {
		t.Fatal("ticket list leaked secret material")
	}
	mcpSettingsAssertNoPlaintext(t, filepath.Dir(server.executionRoot), issued.Secret)

	other := context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{
		Authenticated: true, OwnerID: "other-owner", ActorID: "other-actor", ClientID: "other-client",
	})
	revoke := domainTestRequest("settings.mcp.tickets.revoke", nil)
	revoke.Params = map[string]string{"ticket_id": issued.Ticket.ID}
	if result := handler(other, revoke); result.Status != http.StatusNotFound {
		t.Fatalf("other owner accessed ticket: %d", result.Status)
	}
	if result := handler(ctx, revoke); result.Status != http.StatusOK {
		t.Fatalf("ticket revoke: %d %s", result.Status, result.Body)
	}
	remove := domainTestRequest("settings.mcp.tickets.delete", nil)
	remove.Params = revoke.Params
	if result := handler(ctx, remove); result.Status != http.StatusOK {
		t.Fatalf("ticket delete: %d %s", result.Status, result.Body)
	}
	if tickets, err = repo.ListMCPAccessTickets(ctx, "iscp-owner"); err != nil || len(tickets) != 0 {
		t.Fatalf("ticket was not removed: count=%d err=%v", len(tickets), err)
	}
}

func TestISCPDomainMCPSettingsBindingLifecycleUsesOwnerAuthority(t *testing.T) {
	_, repo, _, handler := mcpSettingsFixture(t)
	ctx := domainTestContext(t)
	issuedResult := handler(ctx, domainTestRequest("settings.mcp.tickets.issue", nil))
	var issued mcpaccess.IssuedTicket
	if issuedResult.Status != http.StatusCreated || json.Unmarshal(issuedResult.Body, &issued) != nil {
		t.Fatalf("issue binding ticket: %d", issuedResult.Status)
	}
	ticket, found, err := repo.GetMCPAccessTicket(ctx, issued.Ticket.ID)
	if err != nil || !found {
		t.Fatalf("issued ticket missing: %v", err)
	}
	binding, err := repo.RedeemMCPAccessTicket(ctx, ticket.SecretHash, app.MCPPeerIdentity{
		DomainID: "mcp-settings-domain", DeviceID: "external-client", KeyThumbprint: "external-thumbprint", ISCPSessionID: "external-session",
	}, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	list := handler(ctx, domainTestRequest("settings.mcp.bindings.list", nil))
	if list.Status != http.StatusOK || !bytes.Contains(list.Body, []byte(binding.ID)) {
		t.Fatalf("binding list: %d %s", list.Status, list.Body)
	}
	other := context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{
		Authenticated: true, OwnerID: "other-owner", ActorID: "other-actor", ClientID: "other-client",
	})
	for _, operation := range []string{"settings.mcp.bindings.revoke", "settings.mcp.bindings.delete"} {
		request := domainTestRequest(operation, nil)
		request.Params = map[string]string{"binding_id": binding.ID}
		if result := handler(other, request); result.Status != http.StatusNotFound {
			t.Fatalf("other owner changed binding: %s %d", operation, result.Status)
		}
		if result := handler(ctx, request); result.Status != http.StatusOK {
			t.Fatalf("owner binding mutation: %s %d %s", operation, result.Status, result.Body)
		}
	}
	if _, found, err = repo.GetMCPBinding(ctx, binding.ID); err != nil || found {
		t.Fatalf("binding was not removed: %v", err)
	}
	cleared := handler(ctx, domainTestRequest("settings.mcp.records.delete", nil))
	if cleared.Status != http.StatusOK {
		t.Fatalf("clear access records: %d %s", cleared.Status, cleared.Body)
	}
	if tickets, err := repo.ListMCPAccessTickets(ctx, "iscp-owner"); err != nil || len(tickets) != 0 {
		t.Fatalf("ticket records survived clear: count=%d err=%v", len(tickets), err)
	}
}

type mcpSettingsPairingAuthority struct {
	gatewayPairingAuthority
	calls atomic.Int32
}

func (a *mcpSettingsPairingAuthority) IssuePairingTicket(ctx context.Context, input iscppairing.AuthorityRequest) (iscppairing.AuthorityResult, error) {
	a.calls.Add(1)
	return a.gatewayPairingAuthority.IssuePairingTicket(ctx, input)
}

func TestISCPDomainMCPSettingsPairingRecoverableWithoutPlaintextTicket(t *testing.T) {
	server, repo, cfg, handler := mcpSettingsFixture(t)
	ctx := domainTestContext(t)
	now := time.Now().UTC()
	const secret = "pairing-signature-private-canary"
	authority := &mcpSettingsPairingAuthority{gatewayPairingAuthority: gatewayPairingAuthority{result: iscppairing.AuthorityResult{
		AuthorityRef: "authority-ref", Ticket: provisioning.PairingTicket{
			Type: provisioning.TypePairingTicket, TicketID: "pairing-ticket", DomainID: "mcp-settings-domain", RelayID: "relay-a", TrustRootID: "root-a", MaxUses: 1,
			IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute), Signature: identity.Signature{Alg: "Ed25519", KID: "root-key", Value: secret},
		},
	}}}
	server.iscpPairing = iscppairing.New(repo, iscppairing.Options{
		Enabled: true, DomainID: "mcp-settings-domain", AuthorityHost: "authority.invalid", ExpectedTicketType: provisioning.TypePairingTicket, Authority: authority,
	})
	status := handler(ctx, domainTestRequest("settings.mcp.pairing.status", nil))
	if status.Status != http.StatusOK || !bytes.Contains(status.Body, []byte(`"ready":true`)) {
		t.Fatalf("pairing status: %d %s", status.Status, status.Body)
	}
	disabled := handler(ctx, domainTestRequest("settings.mcp.pairing.start", []byte(`{"display_name":"external client"}`)))
	if disabled.Status != http.StatusConflict || authority.calls.Load() != 0 {
		t.Fatal("disabled ISCP transport reached pairing authority")
	}
	if _, err := server.connectors.SetMCPTransports(ctx, "iscp-owner", "iscp-actor", true, false, 1); err != nil {
		t.Fatal(err)
	}
	// The backend's name limit is bytes, including for non-ASCII names.
	body, err := json.Marshal(iscppairing.StartRequest{DisplayName: strings.Repeat("客", 40), TTLSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("settings.mcp.pairing.start", body)
	first := handler(ctx, request)
	if first.Status != http.StatusCreated || !bytes.Contains(first.Body, []byte(secret)) {
		t.Fatalf("pairing issuance: %d", first.Status)
	}
	journal, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	restarted := (&iscpDomainAdapter{server: server, config: cfg, receipts: journal}).handle
	if replay := restarted(ctx, request); replay.Status != http.StatusCreated || !bytes.Equal(replay.Body, first.Body) || authority.calls.Load() != 1 {
		t.Fatalf("pairing replay reissued ticket: status=%d calls=%d", replay.Status, authority.calls.Load())
	}
	list := handler(ctx, domainTestRequest("settings.mcp.pairing.onboardings", nil))
	if list.Status != http.StatusOK || !bytes.Contains(list.Body, []byte("pairing-ticket")) || bytes.Contains(list.Body, []byte(secret)) {
		t.Fatal("pairing receipt list leaked or omitted ticket metadata")
	}
	mcpSettingsAssertNoPlaintext(t, filepath.Dir(server.executionRoot), secret)
}

func mcpSettingsAssertNoPlaintext(t *testing.T, root string, secret string) {
	t.Helper()
	sealed := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("private ticket material persisted in plaintext at %s", path)
		}
		if strings.HasSuffix(path, ".sealed") {
			sealed++
		}
		return nil
	})
	if err != nil || sealed == 0 {
		t.Fatalf("receipt persistence verification failed: sealed=%d err=%v", sealed, err)
	}
}
