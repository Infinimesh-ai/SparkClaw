package gateway

import (
	"context"
	"net/http"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func (a *iscpDomainAdapter) tools(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	session, authenticated := iscpworkbench.SessionFromContext(ctx)
	if !authenticated {
		return domainError(403, "authenticated_session_required")
	}
	return a.toolsWithSession(ctx, request, session)
}
func (a *iscpDomainAdapter) toolsWithSession(ctx context.Context, request iscpworkbench.Request, session iscpworkbench.SessionInfo) iscpDomainResult {
	principal, _, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	authorization := a.server.workbenchExecutionAuthorization(session)
	if request.Operation == iscpworkbench.OperationToolsList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		definitions := []app.ToolDefinition{}
		// Definitions already excludes unavailable runtime dependencies.
		for _, definition := range a.server.tools.Definitions() {
			if slices.Contains(authorization.AllowedTools, definition.Name) {
				definitions = append(definitions, definition)
			}
		}
		return domainJSON(200, map[string]any{"tools": definitions})
	}
	if len(authorization.AllowedTools) == 0 {
		return domainError(403, "tool_permission_denied")
	}
	envelope, err := execution.Decode(request.Body, request.InputDigest)
	if err != nil {
		return domainExecutionError(err)
	}
	if envelope.RequestID != request.RequestID || envelope.DeploymentID != a.config.Binding.DeploymentID || envelope.OwnerID != principal.OwnerID || envelope.ClientID != principal.ClientID || envelope.InstallationID != request.InstallationID {
		return domainError(409, "execution_binding_conflict")
	}
	// Tools use the original execution ledger, continuation, approval and ACK
	// protocol. No alternate direct ToolHub execution path is exposed.
	return domainHTTP(withExecutionAuthorization(ctx, authorization), request, http.MethodPost, a.server.submitExecution, nil)
}
