package gateway

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) getWorkbenchIdentity(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if !principal.Authenticated || strings.TrimSpace(principal.ClientID) == "" {
		writeError(w, http.StatusUnauthorized, errors.New("a Client bearer token is required"))
		return
	}
	deploymentID := strings.TrimSpace(s.cfg.Gateway.DeploymentID)
	if deploymentID == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("deployment identity is unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"deployment_id": deploymentID,
		"owner_id":      principal.OwnerID,
		"client_id":     principal.ClientID,
	})
}
