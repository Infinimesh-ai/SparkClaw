package gateway

import (
	"context"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

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
	}
	return domainError(501, "capability_unavailable")
}
