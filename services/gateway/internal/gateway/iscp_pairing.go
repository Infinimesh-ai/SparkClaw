package gateway

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
)

func (s *Server) getISCPPairingStatus(w http.ResponseWriter, r *http.Request) {
	s.mcpPairingStatus(r.Context()).write(w)
}

func (s *Server) mcpPairingStatus(ctx context.Context) domainReply {
	if s.iscpPairing == nil {
		return reply(http.StatusOK, iscppairing.Status{State: "unavailable", DisabledReason: "not_configured", ExpectedTicketType: iscppairing.DefaultTicketType})
	}
	return reply(http.StatusOK, s.iscpPairing.Status(ctx))
}

func (s *Server) listISCPOnboardings(w http.ResponseWriter, r *http.Request) {
	s.mcpPairingOnboardings(r.Context(), principalForRequest(r)).write(w)
}

func (s *Server) mcpPairingOnboardings(ctx context.Context, principal requestPrincipal) domainReply {
	if s.iscpPairing == nil {
		return reply(http.StatusOK, map[string]any{"onboardings": []any{}})
	}
	onboardings, err := s.iscpPairing.List(ctx, principal.OwnerID)
	if err != nil {
		status := iscpPairingFailureStatus(err)
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		return replyError(status, err)
	}
	return reply(http.StatusOK, map[string]any{"onboardings": onboardings})
}

func (s *Server) startISCPPairing(w http.ResponseWriter, r *http.Request) {
	var input iscppairing.StartRequest
	answer, ready := s.mcpPairingReady(r.Context(), principalForRequest(r))
	if ready {
		if err := readJSON(r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		answer = s.mcpPairingStart(r.Context(), principalForRequest(r), input)
	}
	if !answer.failed() {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Pragma", "no-cache")
	}
	answer.write(w)
}

// mcpPairingReady tells whether a pairing may start: MCP over ISCP is on.
func (s *Server) mcpPairingReady(ctx context.Context, principal requestPrincipal) (domainReply, bool) {
	if s.iscpPairing == nil {
		return replyError(http.StatusServiceUnavailable, iscppairing.ErrUnavailable), false
	}
	if s.connectors == nil {
		return replyError(http.StatusServiceUnavailable, errors.New("MCP connector control is unavailable")), false
	}
	connector, err := s.connectors.Status(ctx, principal.OwnerID, "mcp")
	if err != nil || !connector.Enabled {
		return replyError(http.StatusConflict, errors.New("MCP connector is disabled")), false
	}
	if !connector.ISCPEnabled {
		return replyError(http.StatusConflict, errors.New("MCP over ISCP is disabled")), false
	}
	return domainReply{}, true
}

func (s *Server) mcpPairingStart(ctx context.Context, principal requestPrincipal, input iscppairing.StartRequest) domainReply {
	if answer, ready := s.mcpPairingReady(ctx, principal); !ready {
		return answer
	}
	issued, err := s.iscpPairing.Start(ctx, principal.OwnerID, principal.ActorID, input, time.Now().UTC())
	if err != nil {
		status := http.StatusBadGateway
		if failureStatus := iscpPairingFailureStatus(err); failureStatus != 0 {
			status = failureStatus
		} else if !errors.Is(err, iscppairing.ErrAuthority) {
			status = http.StatusBadRequest
		}
		return replyError(status, err)
	}
	return reply(http.StatusCreated, issued)
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
