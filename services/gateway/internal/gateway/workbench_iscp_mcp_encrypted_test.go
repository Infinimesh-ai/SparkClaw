package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"path/filepath"
	"slices"
	"testing"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
)

// Reuse the production Endpoint cryptography, signed issuer authorization,
// negotiation, Gateway dispatcher and file store. The Relay only queues opaque
// envelopes in process; no provider or hosted service is contacted.
func TestWorkbenchISCPV2EncryptedMCPSettingsIssueAndRecover(t *testing.T) {
	server, repo, local, _ := mcpSettingsFixture(t)
	operations := []string{
		wb.OperationSettingsMCPCatalog,
		wb.OperationSettingsMCPTicketsIssue,
		wb.OperationSettingsMCPTicketsList,
	}
	fixture := startV2EncryptedFixture(t, server, local, nil, operations)
	negotiated, ok := fixture.client.Negotiated()
	if !ok {
		t.Fatal("settings connection was not negotiated")
	}
	for _, operation := range operations {
		if !slices.Contains(negotiated.Operations, operation) || !slices.Contains(negotiated.QualifiedOperations, operation) {
			t.Fatalf("MCP operation missing from negotiated capabilities: %s", operation)
		}
	}

	// Transport permission alone cannot bypass the desktop installation binding.
	requireV2Status(t, fixture.call(t, v2Request(wb.OperationSettingsMCPCatalog, nil)), http.StatusForbidden)
	fixture.bind(t)
	catalog := fixture.call(t, v2Request(wb.OperationSettingsMCPCatalog, nil))
	requireV2Status(t, catalog, http.StatusOK)
	var catalogValue struct {
		DomainID string `json:"domain_id"`
	}
	if json.Unmarshal(catalog.Body, &catalogValue) != nil || catalogValue.DomainID != "mcp-settings-domain" {
		t.Fatal("encrypted catalog did not reach the configured MCP service")
	}

	request := v2Request(wb.OperationSettingsMCPTicketsIssue, []byte(`{"ttl_seconds":60}`))
	created := fixture.call(t, request)
	requireV2Status(t, created, http.StatusCreated)
	var issued mcpaccess.IssuedTicket
	if json.Unmarshal(created.Body, &issued) != nil || issued.Secret == "" || issued.Ticket.OwnerID != "iscp-owner" {
		t.Fatal("encrypted issuance did not return an owner-scoped ticket")
	}

	// A second transport request keeps the durable operation identity, as the
	// desktop does when recovering after losing the first result.
	request.ID = v2Request(wb.OperationIdentity, nil).ID
	replay := fixture.call(t, request)
	requireV2Status(t, replay, http.StatusCreated)
	if !bytes.Equal(replay.Body, created.Body) {
		t.Fatal("retry did not recover the original issuance result")
	}
	tickets, err := repo.ListMCPAccessTickets(fixture.ctx, "iscp-owner")
	if err != nil || len(tickets) != 1 || tickets[0].ID != issued.Ticket.ID || tickets[0].SecretHash == "" {
		t.Fatalf("encrypted retry issued more than one business ticket: count=%d err=%v", len(tickets), err)
	}

	receipt := v2Request(wb.OperationOperationsReceipt, nil)
	receipt.Params = map[string]string{"operation_id": request.OperationID}
	recovered := fixture.call(t, receipt)
	requireV2Status(t, recovered, http.StatusOK)
	var result struct {
		State    string `json:"state"`
		Response struct {
			Status int             `json:"status"`
			Body   json.RawMessage `json:"body"`
		} `json:"response"`
	}
	if json.Unmarshal(recovered.Body, &result) != nil || result.State != "completed" ||
		result.Response.Status != http.StatusCreated || !bytes.Equal(result.Response.Body, created.Body) {
		t.Fatal("receipt lookup did not recover the original encrypted response")
	}
	list := fixture.call(t, v2Request(wb.OperationSettingsMCPTicketsList, nil))
	requireV2Status(t, list, http.StatusOK)
	if bytes.Contains(list.Body, []byte(issued.Secret)) || bytes.Contains(list.Body, []byte("secret_hash")) {
		t.Fatal("ordinary ticket list exposed issuance secret material")
	}
	mcpSettingsAssertNoPlaintext(t, filepath.Dir(server.executionRoot), issued.Secret)

	fixture.network.mu.Lock()
	defer fixture.network.mu.Unlock()
	if len(fixture.network.envelopes) == 0 {
		t.Fatal("test did not traverse encrypted Relay envelopes")
	}
	for _, envelope := range fixture.network.envelopes {
		if bytes.Contains(envelope, []byte(issued.Secret)) || bytes.Contains(envelope, []byte(catalogValue.DomainID)) {
			t.Fatal("Relay observed plaintext MCP business data")
		}
	}
}
