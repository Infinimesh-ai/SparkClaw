package gateway

import (
	"context"
	"errors"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func (s *Server) executionProgress(w http.ResponseWriter, r *http.Request) {
	p, _, err := s.executionReadPrincipal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	var input execution.ProgressInput
	if readExecutionJSON(r, &input) != nil || !input.Valid() || !execution.UUID(r.PathValue("request")) {
		writeError(w, 400, errors.New("invalid execution progress input"))
		return
	}
	ctx, release, err := s.clientConnectionContext(r.Context(), p.ClientID)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	defer release()
	service, err := s.executionService()
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	result, err := service.ReadProgress(ctx, p.OwnerID, p.ClientID, r.Header.Get("X-SparkClaw-Installation"), r.PathValue("request"), input)
	if err != nil {
		writeExecutionError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func (a *iscpDomainAdapter) executionProgress(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	var input execution.ProgressInput
	requestID := request.Params["request_id"]
	if requestID == "" {
		requestID = request.RequestID
	}
	if !execution.UUID(requestID) || (request.RequestID != "" && request.RequestID != requestID) || domainDecode(request.Body, &input) != nil || !input.Valid() {
		return domainError(400, "invalid_input")
	}
	principal, service, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	result, err := service.ReadProgress(ctx, principal.OwnerID, principal.ClientID, request.InstallationID, requestID, input)
	if err != nil {
		return domainExecutionError(err)
	}
	return domainJSON(200, result)
}
