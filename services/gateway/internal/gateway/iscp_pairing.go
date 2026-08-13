package gateway

import (
	"errors"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
)

func (s *Server) getISCPPairingStatus(w http.ResponseWriter, r *http.Request) {
	if s.iscpPairing == nil {
		writeJSON(w, http.StatusOK, iscppairing.Status{State: "unavailable", DisabledReason: "not_configured", ExpectedTicketType: iscppairing.DefaultTicketType})
		return
	}
	writeJSON(w, http.StatusOK, s.iscpPairing.Status(r.Context()))
}

func (s *Server) listISCPOnboardings(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if s.iscpPairing == nil {
		writeJSON(w, http.StatusOK, map[string]any{"onboardings": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"onboardings": s.iscpPairing.List(principal.OwnerID)})
}

func (s *Server) startISCPPairing(w http.ResponseWriter, r *http.Request) {
	if s.iscpPairing == nil {
		writeError(w, http.StatusServiceUnavailable, iscppairing.ErrUnavailable)
		return
	}
	principal := principalForRequest(r)
	if s.connectors == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("MCP connector control is unavailable"))
		return
	}
	connector, err := s.connectors.Status(principal.OwnerID, "mcp")
	if err != nil || !connector.Enabled {
		writeError(w, http.StatusConflict, errors.New("MCP connector is disabled"))
		return
	}
	var input iscppairing.StartRequest
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	issued, err := s.iscpPairing.Start(r.Context(), principal.OwnerID, principal.ActorID, input, time.Now().UTC())
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, iscppairing.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		} else if !errors.Is(err, iscppairing.ErrAuthority) {
			status = http.StatusBadRequest
		}
		writeError(w, status, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusCreated, issued)
}
