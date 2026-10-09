package gateway

import (
	"context"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type iscpDomainCapability struct {
	ID                string   `json:"id"`
	Supported         bool     `json:"supported"`
	Permitted         bool     `json:"permitted"`
	DependenciesReady bool     `json:"dependencies_ready"`
	Qualified         bool     `json:"qualified"`
	Enabled           bool     `json:"enabled"`
	Reason            string   `json:"reason,omitempty"`
	Operations        []string `json:"operations"`
}

func (a *iscpDomainAdapter) capabilities(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if !domainEmpty(request.Body) {
		return domainError(400, "invalid_input")
	}
	session, ok := iscpworkbench.SessionFromContext(ctx)
	if !ok {
		return domainError(401, "authenticated_session_required")
	}
	type family struct {
		id         string
		operations []string
		ready      bool
	}
	families := []family{
		{"settings_owner", []string{iscpworkbench.OperationSettingsOwnerGet, iscpworkbench.OperationSettingsOwnerPatch}, true},
		{"settings_connectors", []string{iscpworkbench.OperationSettingsConnectorsList, iscpworkbench.OperationSettingsConnectorsPatch}, a.server.connectors != nil},
		{"settings_credentials", []string{iscpworkbench.OperationSettingsIntegrationsList, iscpworkbench.OperationSettingsCredentialsAdd, iscpworkbench.OperationSettingsCredentialsActivate, iscpworkbench.OperationSettingsCredentialsCheck, iscpworkbench.OperationSettingsCredentialsDelete}, a.server.integrations != nil},
		{"notifications", []string{iscpworkbench.OperationNotificationsList, iscpworkbench.OperationNotificationsRead, iscpworkbench.OperationNotificationsReadAll}, true},
	}
	capabilities := make([]iscpDomainCapability, 0, len(families))
	operations := []string{}
	for _, family := range families {
		capability := iscpDomainCapability{ID: family.id, Operations: family.operations, Supported: true, Permitted: true, Qualified: true, DependenciesReady: family.ready}
		for _, name := range family.operations {
			spec, found := iscpworkbench.LookupOperation(name)
			if !found {
				capability.Supported = false
				continue
			}
			if !slices.Contains(session.Scopes, spec.Scope) {
				capability.Permitted = false
			}
			if !slices.Contains(a.config.QualifiedCapabilities, name) {
				capability.Qualified = false
			}
			operations = append(operations, name)
		}
		capability.Enabled = capability.Supported && capability.Permitted && capability.Qualified && capability.DependenciesReady
		switch {
		case !capability.Supported:
			capability.Reason = "capability_unavailable"
		case !capability.Permitted:
			capability.Reason = "permission_denied"
		case !capability.DependenciesReady:
			capability.Reason = "dependency_unavailable"
		case !capability.Qualified:
			capability.Reason = "qualification_required"
		}
		capabilities = append(capabilities, capability)
	}
	return domainJSON(200, map[string]any{"schema_version": 2, "profile": session.Profile, "session_id": session.SessionID, "deployment_id": a.config.Binding.DeploymentID, "revision": domainRevision(capabilities), "authorization_revision": session.GrantRevision, "expires_at": domainNow().Add(2 * time.Minute), "permissions": session.Scopes, "capabilities": capabilities, "operations": operations, "limits": map[string]any{"message_bytes": iscpworkbench.MaxMessageBytes, "notification_watermark_items": 500, "operation_receipt_retention": "until_manual_authorization_deletion"}})
}
