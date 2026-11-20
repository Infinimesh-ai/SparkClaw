package gateway

import (
	"errors"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
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
