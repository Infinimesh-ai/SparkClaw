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
	onboardings, err := s.iscpPairing.List(r.Context(), principal.OwnerID)
	if err != nil {
		status := iscpPairingFailureStatus(err)
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"onboardings": onboardings})
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
	if !connector.ISCPEnabled {
		writeError(w, http.StatusConflict, errors.New("MCP over ISCP is disabled"))
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
		if failureStatus := iscpPairingFailureStatus(err); failureStatus != 0 {
			status = failureStatus
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

func iscpPairingFailureStatus(err error) int {
	switch iscppairing.FailureCodeOf(err) {
	case iscppairing.FailureTimeout:
		return http.StatusGatewayTimeout
	case iscppairing.FailureUnavailable, iscppairing.FailureExpired:
		return http.StatusServiceUnavailable
	case iscppairing.FailureConflict:
		return http.StatusConflict
	case iscppairing.FailureInvalid:
		return http.StatusBadRequest
	default:
		return 0
	}
}
