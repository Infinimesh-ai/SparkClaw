package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
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
	if s.email == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("browser email configuration is unavailable"))
		return
	}
	provider := strings.ToLower(strings.TrimSpace(r.PathValue("provider")))
	var input struct {
		Enabled         *bool  `json:"enabled"`
		Default         *bool  `json:"default"`
		ExpectedVersion *int64 `json:"expected_version"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := readJSON(r, &input); err != nil || provider == "" || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 || input.Enabled == nil && input.Default == nil {
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
	principal := principalForRequest(r)
	status, err := s.email.Check(r.Context(), principal.OwnerID, principal.ActorID, provider)
	if err != nil {
		writeEmailError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func writeEmailError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrEmailProviderSettingConflict) || store.StoreErrorCodeOf(err) == store.StoreErrorConflict {
		writeError(w, http.StatusConflict, err)
		return
	}
	switch emailautomation.ErrorCode(err) {
	case emailautomation.CodeInvalidInput:
		writeError(w, http.StatusBadRequest, err)
	case emailautomation.CodeNotConfigured, emailautomation.CodeLoginRequired, emailautomation.CodeAccountAmbiguous, emailautomation.CodeAdmissionStale, emailautomation.CodeDraftConflict:
		writeError(w, http.StatusConflict, err)
	case emailautomation.CodePageContractChanged, emailautomation.CodeDraftVerifyFailed, emailautomation.CodeSendControlUnverified, emailautomation.CodeScriptInvalidOutput:
		writeError(w, http.StatusUnprocessableEntity, err)
	case emailautomation.CodeProviderUnavailable, emailautomation.CodeScriptTimeout:
		writeError(w, http.StatusServiceUnavailable, err)
	case emailautomation.CodeSendOutcomeUnknown:
		writeError(w, http.StatusBadGateway, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
}
