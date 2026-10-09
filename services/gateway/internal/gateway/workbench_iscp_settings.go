package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (a *iscpDomainAdapter) owner(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	profile, found, err := a.server.store.GetOwnerProfileByID(ctx, principal.OwnerID)
	if err != nil {
		return domainError(503, "settings_unavailable")
	}
	if !found {
		profile = app.OwnerProfile{ID: principal.OwnerID, DisplayName: "Owner", Preferences: map[string]string{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{}}
	}
	if request.Operation == iscpworkbench.OperationSettingsOwnerGet {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainValue(profile)
	}
	var input struct {
		DisplayName string            `json:"display_name"`
		Email       string            `json:"email"`
		Preferences map[string]string `json:"preferences"`
	}
	if domainDecode(request.Body, &input) != nil {
		return domainError(400, "invalid_input")
	}
	if request.ExpectedRevision == "" || request.ExpectedRevision != domainRevision(profile) {
		return domainError(409, "revision_conflict")
	}
	candidate, err := normalizeOwnerProfileInput(profile, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		return domainError(400, "invalid_input")
	}
	updated, err := a.server.store.SaveOwnerProfile(store.WithOwnerProfilePrecondition(ctx, found, profile.UpdatedAt), candidate)
	updated, err = store.ReconcileOwnerProfileWrite(ctx, a.server.store, updated, err)
	if errors.Is(err, store.ErrOwnerProfileConflict) {
		return domainError(409, "revision_conflict")
	}
	if err != nil {
		return domainError(503, "settings_outcome_unknown")
	}
	return domainValue(updated)
}
func (a *iscpDomainAdapter) connectors(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if a.server.connectors == nil {
		return domainError(503, "connector_unavailable")
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	if request.Operation == iscpworkbench.OperationSettingsConnectorsList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		statuses, err := a.server.connectors.ListStatus(ctx, principal.OwnerID)
		if err != nil {
			return domainError(503, "connector_unavailable")
		}
		return domainValue(map[string]any{"connectors": statuses})
	}
	channel := request.Params["channel"]
	if channel == "" || strings.TrimSpace(channel) != channel {
		return domainError(400, "invalid_input")
	}
	var input struct {
		Enabled         *bool  `json:"enabled"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	if domainDecode(request.Body, &input) != nil || input.Enabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		return domainError(400, "invalid_input")
	}
	current, err := a.server.connectors.Status(ctx, principal.OwnerID, channel)
	if err != nil {
		return domainError(404, "connector_not_found")
	}
	if request.ExpectedRevision != "" && request.ExpectedRevision != domainRevision(current) {
		return domainError(409, "revision_conflict")
	}
	updated, err := a.server.connectors.SetEnabled(ctx, principal.OwnerID, principal.ActorID, channel, *input.Enabled, *input.ExpectedVersion)
	if errors.Is(err, store.ErrConnectorSettingConflict) {
		return domainError(409, "revision_conflict")
	}
	if err != nil {
		return domainError(503, "connector_outcome_unknown")
	}
	return domainValue(updated)
}
func (a *iscpDomainAdapter) integrations(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	controller := a.server.integrations
	if controller == nil {
		return domainError(503, "integration_unavailable")
	}
	if request.Operation == iscpworkbench.OperationSettingsIntegrationsList {
		if !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		statuses := controller.List(ctx)
		revisions := map[string]string{}
		for _, status := range statuses {
			revisions[status.ID] = integrationconfig.StatusRevision(status)
		}
		return domainValue(map[string]any{"integrations": statuses, "resource_revisions": revisions})
	}
	id := request.Params["integration_id"]
	if id != integrationconfig.InfoID && id != integrationconfig.LocalMindID {
		return domainError(404, "integration_not_found")
	}
	current, err := controller.Get(ctx, id)
	if err != nil {
		return domainError(503, "integration_unavailable")
	}
	if request.ExpectedRevision == "" || request.ExpectedRevision != integrationconfig.StatusRevision(current) {
		return domainError(409, "revision_conflict")
	}
	ctx = integrationconfig.WithStatusPrecondition(ctx, request.ExpectedRevision)
	var status integrationconfig.Status
	switch request.Operation {
	case iscpworkbench.OperationSettingsCredentialsAdd:
		if id == integrationconfig.InfoID {
			var input integrationconfig.AddInfoCredentialInput
			if domainDecode(request.Body, &input) != nil {
				return domainError(400, "invalid_input")
			}
			status, err = controller.AddInfoCredential(ctx, input)
		} else {
			var input integrationconfig.AddLocalMindCredentialInput
			if domainDecode(request.Body, &input) != nil {
				return domainError(400, "invalid_input")
			}
			status, err = controller.AddLocalMindCredential(ctx, input)
		}
	case iscpworkbench.OperationSettingsCredentialsActivate:
		var input struct {
			CredentialID string `json:"credential_id"`
			UseOperator  bool   `json:"use_operator"`
		}
		if domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		status, err = controller.Activate(ctx, id, input.CredentialID, input.UseOperator)
	case iscpworkbench.OperationSettingsCredentialsCheck, iscpworkbench.OperationSettingsCredentialsDelete:
		if !domainEmpty(request.Body) || request.Params["credential_id"] == "" {
			return domainError(400, "invalid_input")
		}
		if request.Operation == iscpworkbench.OperationSettingsCredentialsCheck {
			status, err = controller.Check(ctx, id, request.Params["credential_id"])
		} else {
			status, err = controller.Delete(ctx, id, request.Params["credential_id"])
		}
	}
	if err != nil {
		code := integrationconfig.ErrorCode(err)
		if code == "" {
			code = "integration_unavailable"
		}
		httpStatus := 400
		if code == "active_credential_replacement_required" || code == "credential_revision_conflict" {
			httpStatus = 409
		}
		if integrationconfig.ErrorRetryable(err) {
			httpStatus = 503
		}
		return domainError(httpStatus, code)
	}
	return domainValue(status)
}
