package gateway

import (
	"errors"
	"net/http"
	"strings"
)

func (s *Server) getWorkbenchIdentity(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if !principal.Authenticated || (strings.TrimSpace(principal.ClientID) == "" && principal.LocalAccessID == "") {
		writeError(w, http.StatusUnauthorized, errors.New("a Client bearer token is required"))
		return
	}
	deploymentID := strings.TrimSpace(s.cfg.Gateway.DeploymentID)
	if deploymentID == "" {
		writeError(w, http.StatusServiceUnavailable, errors.New("deployment identity is unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	mode := "credential"
	if principal.LocalAccessID != "" {
		mode = "local"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"deployment_id":   deploymentID,
		"owner_id":        principal.OwnerID,
		"client_id":       principal.ClientID,
		"access_mode":     mode,
		"local_access_id": principal.LocalAccessID,
	})
}
