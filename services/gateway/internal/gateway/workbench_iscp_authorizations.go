package gateway

import (
	"context"
	"encoding/json"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Device credentials authorize only their own standing consent. This is not an
// Owner-wide device directory and never returns Grants or identity key material.
func workbenchV2Authorization(ctx context.Context, request iscpworkbench.Request, session iscpworkbench.SessionInfo, principal requestPrincipal) iscpworkbench.Response {
	if !domainEmpty(request.Body) || session.CheckAuthorization == nil {
		return workbenchV2Error(request, 400, "authorization_status_unavailable")
	}
	policy, err := session.CheckAuthorization(ctx)
	if err != nil || policy.State != iscpauth.Active || policy.Revision != session.GrantRevision || !policy.Allows("authorization.manage") {
		return workbenchV2Error(request, 403, "authorization_closed")
	}
	value := map[string]any{
		"owner_id": principal.OwnerID, "client_id": principal.ClientID,
		"authorization_revision": policy.Revision, "lifetime": policy.Lifetime,
		"state": policy.State, "scopes": policy.Scopes, "deletion_transport": "issuer_device_proof",
	}
	if policy.Lifetime == iscpauth.Bounded {
		value["expires_at"] = policy.ExpiresAt
	}
	body, _ := json.Marshal(map[string]any{"authorizations": []any{value}})
	return iscpworkbench.Response{Type: iscpworkbench.ResponseType, Profile: request.Profile, ID: request.ID, Status: 200, Body: body}
}
