package gateway

import (
	"context"
	"net/http"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// Provider login opens an owner-visible backend browser. Reading mailbox data
// or changing settings alone never authorizes that browser side effect.
func workbenchOperationPermitted(spec iscpworkbench.OperationSpec, scopes []string) bool {
	if !slices.Contains(scopes, spec.Scope) {
		return false
	}
	switch spec.Name {
	case iscpworkbench.OperationMailProvidersUpdate, iscpworkbench.OperationMailProvidersCheck:
		return slices.Contains(scopes, "mail.read")
	case iscpworkbench.OperationMailProvidersLogin:
		return slices.Contains(scopes, "mail.read") && slices.Contains(scopes, "settings.write")
	default:
		return true
	}
}

func (a *iscpDomainAdapter) mailProviders(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if a.server.email == nil {
		return domainError(503, "mail_settings_unavailable")
	}
	if request.Operation == iscpworkbench.OperationMailProvidersList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodGet, a.server.listEmailProviders, nil)
	}
	provider := request.Params["provider"]
	if !app.KnownEmailProvider(provider) {
		return domainError(400, "invalid_email_provider")
	}
	params := map[string]string{"provider": provider}
	switch request.Operation {
	case iscpworkbench.OperationMailProvidersUpdate:
		// This operation controls sending-provider configuration only. Intake is
		// a distinct mailbox binding operation and must not hitchhike on PATCH.
		var input struct {
			Enabled         *bool  `json:"enabled"`
			Default         *bool  `json:"default"`
			ExpectedVersion *int64 `json:"expected_version"`
		}
		if domainDecode(request.Body, &input) != nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || input.Enabled == nil && input.Default == nil {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPatch, a.server.updateEmailProvider, params)
	case iscpworkbench.OperationMailProvidersCheck:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.checkEmailProvider, params)
	case iscpworkbench.OperationMailProvidersLogin:
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.openEmailLoginBrowser, params)
	}
	return domainError(501, "capability_unavailable")
}
