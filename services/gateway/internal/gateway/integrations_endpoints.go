package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
)

const maxIntegrationCredentialRequestBytes = 16 << 10

func (s *Server) listIntegrations(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"integrations": s.integrations.List(r.Context())})
}

func (s *Server) getIntegration(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	status, err := s.integrations.Get(r.Context(), strings.TrimSpace(r.PathValue("id")))
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) addInfoCredential(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	var input integrationconfig.AddInfoCredentialInput
	if err := readIntegrationJSON(w, r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid Info credential request", "code": "credential_invalid"})
		return
	}
	status, err := s.integrations.AddInfoCredential(r.Context(), input)
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, status)
}

func (s *Server) addLocalMindCredential(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	var input integrationconfig.AddLocalMindCredentialInput
	if err := readIntegrationJSON(w, r, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid LocalMind credential request", "code": "credential_invalid"})
		return
	}
	status, err := s.integrations.AddLocalMindCredential(r.Context(), input)
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, status)
}

func (s *Server) activateIntegrationCredential(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	var input struct {
		CredentialID string `json:"credential_id"`
		UseOperator  bool   `json:"use_operator"`
	}
	if err := readIntegrationJSON(w, r, &input); err != nil || (!input.UseOperator && strings.TrimSpace(input.CredentialID) == "") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid active credential request", "code": "credential_invalid"})
		return
	}
	status, err := s.integrations.Activate(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(input.CredentialID), input.UseOperator)
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) checkIntegrationCredential(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	status, err := s.integrations.Check(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("credential_id")))
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) deleteIntegrationCredential(w http.ResponseWriter, r *http.Request) {
	if s.integrations == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("integration configuration is unavailable"))
		return
	}
	status, err := s.integrations.Delete(r.Context(), strings.TrimSpace(r.PathValue("id")), strings.TrimSpace(r.PathValue("credential_id")))
	if err != nil {
		writeIntegrationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func readIntegrationJSON(w http.ResponseWriter, r *http.Request, output any) error {
	if r.Body == nil {
		return io.EOF
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxIntegrationCredentialRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeIntegrationError(w http.ResponseWriter, err error) {
	code := integrationconfig.ErrorCode(err)
	status := http.StatusBadRequest
	switch code {
	case "integration_not_found", "credential_not_found":
		status = http.StatusNotFound
	case "active_credential_replacement_required":
		status = http.StatusConflict
	case "vault_unavailable":
		status = http.StatusServiceUnavailable
	case "credential_auth_failed":
		status = http.StatusUnauthorized
	case "credential_check_unavailable":
		status = http.StatusBadGateway
	}
	writeJSON(w, status, map[string]any{
		"error": err.Error(), "code": code, "retryable": integrationconfig.ErrorRetryable(err),
	})
}
