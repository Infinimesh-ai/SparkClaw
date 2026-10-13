package gateway

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) deleteClient(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	existing, found, err := s.store.GetClient(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		return
	}
	if !found || strings.TrimSpace(existing.OwnerID) != principalForRequest(r).OwnerID {
		writeError(w, http.StatusNotFound, errors.New("client not found"))
		return
	}
	deleted, err := s.store.DeleteClient(r.Context(), id)
	if err != nil && store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
		persisted, found, readErr := s.store.GetClient(r.Context(), id)
		if readErr == nil && found && persisted.DeletedAt != nil {
			deleted, err = persisted, nil
		}
	}
	if err != nil {
		switch store.StoreErrorCodeOf(err) {
		case store.StoreErrorNotFound:
			writeError(w, http.StatusNotFound, errors.New("client not found"))
		case store.StoreErrorConflict:
			writeError(w, http.StatusConflict, errors.New("revoke the client before deleting its record"))
		case store.StoreErrorTimeout:
			writeError(w, http.StatusGatewayTimeout, errors.New("client deletion request timed out"))
		default:
			writeError(w, http.StatusServiceUnavailable, errors.New("clients are temporarily unavailable"))
		}
		return
	}
	if deleted.DeletedAt == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("client deletion requires reconciliation"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"deleted": true})
}
