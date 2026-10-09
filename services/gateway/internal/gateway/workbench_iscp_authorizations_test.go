package gateway

import (
	"encoding/json"
	"testing"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchISCPV2CurrentStandingAuthorization(t *testing.T) {
	server, _, config, _ := workbenchISCPFixture(t, nil)
	fixture := startV2EncryptedFixture(t, server, config, []string{"authorization.manage"}, []string{wb.OperationAuthorizationsList, wb.OperationAuthorizationsDelete})
	fixture.bind(t)
	response := fixture.jsonCall(t, wb.OperationAuthorizationsList, map[string]any{})
	requireV2Status(t, response, 200)
	var result struct {
		Authorizations []map[string]any `json:"authorizations"`
	}
	if err := json.Unmarshal(response.Body, &result); err != nil || len(result.Authorizations) != 1 {
		t.Fatalf("authorization projection: %s %v", response.Body, err)
	}
	policy := result.Authorizations[0]
	if policy["lifetime"] != "until_revoked" || policy["state"] != "active" || policy["authorization_revision"] != float64(fixture.revision) || policy["client_id"] != config.Binding.ClientID {
		t.Fatalf("unexpected standing policy: %v", policy)
	}
	for _, forbidden := range []string{"expires_at", "grant", "private_key", "token"} {
		if _, exists := policy[forbidden]; exists {
			t.Fatalf("unexpected field %s", forbidden)
		}
	}
	deletion := fixture.jsonCall(t, wb.OperationAuthorizationsDelete, map[string]any{})
	requireV2Status(t, deletion, 409)
	if deletion.Error != "device_authorization_control_required" {
		t.Fatalf("wrong deletion route: %s", deletion.Error)
	}
	requireV2Status(t, fixture.jsonCall(t, wb.OperationAuthorizationsList, map[string]any{}), 200)
}
