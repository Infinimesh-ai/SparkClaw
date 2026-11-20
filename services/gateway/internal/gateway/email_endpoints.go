package gateway

import (
	"context"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) listEmailProviders(w http.ResponseWriter, r *http.Request) {
	if s.email == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
		return
	}
	principal := principalForRequest(r)
	providers, err := s.email.List(r.Context(), principal.OwnerID)
	if err != nil {
		writeEmailError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers})
}

func (s *Server) updateEmailProvider(w http.ResponseWriter, r *http.Request) {
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	var input struct {
		Enabled                *bool  `json:"enabled"`
		Default                *bool  `json:"default"`
		ExpectedVersion        *int64 `json:"expected_version"`
		IntakeEnabled          *bool  `json:"intake_enabled"`
		ExpectedMailboxVersion *int64 `json:"expected_mailbox_version"`
	}
	if err := readEmailJSON(w, r, &input); err != nil || provider == "" {
		writeError(w, http.StatusBadRequest, errors.New("invalid email provider request"))
		return
	}
	if input.IntakeEnabled != nil {
		if input.ExpectedMailboxVersion == nil || *input.ExpectedMailboxVersion < 0 || input.Enabled != nil || input.Default != nil || input.ExpectedVersion != nil {
			writeError(w, http.StatusBadRequest, errors.New("intake changes require expected_mailbox_version and cannot be combined with send settings"))
			return
		}
		s.configureEmailIntake(w, r, provider, *input.IntakeEnabled, *input.ExpectedMailboxVersion)
		return
	}
	if s.email == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
		return
	}
	if input.ExpectedMailboxVersion != nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || input.Enabled == nil && input.Default == nil {
		writeError(w, http.StatusBadRequest, errors.New("provider, expected_version, and at least one setting change are required"))
		return
	}
	principal := principalForRequest(r)
	status, err := s.email.Update(r.Context(), principal.OwnerID, principal.ActorID, provider, emailautomation.UpdateProviderInput{
		Enabled: input.Enabled, Default: input.Default, ExpectedVersion: *input.ExpectedVersion,
	})
	if err != nil {
		writeEmailError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) openEmailLoginBrowser(w http.ResponseWriter, r *http.Request) {
	if s.email == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if provider == "" {
		writeError(w, http.StatusBadRequest, errors.New("email provider is required"))
		return
	}
	principal := principalForRequest(r)
	status, err := s.email.OpenLoginBrowser(r.Context(), principal.OwnerID, principal.ActorID, provider)
	if err != nil {
		writeEmailError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) checkEmailProvider(w http.ResponseWriter, r *http.Request) {
	if s.email == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	if provider == "" {
		writeError(w, http.StatusBadRequest, errors.New("email provider is required"))
		return
	}
	s.emailProviderCheck(r.Context(), principalForRequest(r), provider).write(w)
}

// emailProviderCheck checks a provider's login and reports its sync health.
func (s *Server) emailProviderCheck(ctx context.Context, principal requestPrincipal, provider string) domainReply {
	status, err := s.email.Check(ctx, principal.OwnerID, principal.ActorID, provider)
	if err != nil {
		return emailErrorReply(err)
	}
	health := emailmanagement.SyncHealth{State: emailmanagement.SyncHealthUnavailable, CheckedAt: time.Now().UTC()}
	if s.emailManagement != nil {
		health, err = s.emailManagement.ProviderSyncHealth(ctx, principal.OwnerID, provider)
		if err != nil {
			return emailErrorReply(err)
		}
	}
	return reply(http.StatusOK, struct {
		emailautomation.ProviderStatus
		SyncHealth emailmanagement.SyncHealth `json:"sync_health"`
	}{status, health})
}

func writeEmailError(w http.ResponseWriter, err error) {
	emailErrorReply(err).write(w)
}

func emailErrorReply(err error) domainReply {
	if errors.Is(err, store.ErrEmailProviderSettingConflict) || store.StoreErrorCodeOf(err) == store.StoreErrorConflict {
		return replyError(http.StatusConflict, err)
	}
	switch emailautomation.ErrorCode(err) {
	case app.ToolErrorEmailInvalidInput:
		return replyError(http.StatusBadRequest, err)
	case app.ToolErrorEmailNotConfigured, app.ToolErrorEmailLoginRequired, app.ToolErrorEmailAccountAmbiguous, app.ToolErrorEmailAdmissionStale, app.ToolErrorEmailDraftConflict:
		return replyError(http.StatusConflict, err)
	case app.ToolErrorEmailPageContractChanged, app.ToolErrorEmailDraftVerificationFailed, app.ToolErrorEmailSendControlUnverified, app.ToolErrorEmailScriptInvalidOutput, app.ToolErrorEmailSendNotDispatched:
		return replyError(http.StatusUnprocessableEntity, err)
	case app.ToolErrorEmailProviderUnavailable, app.ToolErrorEmailScriptTimeout, app.ToolErrorEmailBrowserCleanupPending:
		return replyError(http.StatusServiceUnavailable, err)
	case app.ToolErrorEmailSendOutcomeUnknown:
		return replyError(http.StatusBadGateway, err)
	default:
		if code := store.StoreErrorCodeOf(err); code != "" {
			// Store failures carry driver text; the client gets a stable code
			// and a bounded message like the sibling integration endpoints.
			return reply(http.StatusServiceUnavailable, map[string]any{
				"error": "email provider settings are temporarily unavailable", "code": "store_" + string(code), "retryable": true,
			})
		}
		return replyError(http.StatusInternalServerError, err)
	}
}
