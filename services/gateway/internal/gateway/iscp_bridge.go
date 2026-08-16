package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
)

const (
	bridgeRequestLimit = 1 << 20
	bridgeRoutePrefix  = "/api/bridge/v1/"
)

func (s *Server) dispatchBridgeRequest(w http.ResponseWriter, r *http.Request) {
	if !isLocalRequest(r) {
		writeError(w, http.StatusForbidden, errors.New("bridge API is restricted to loopback clients"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, bridgeRequestLimit)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request iscpbridge.Request
	if err := decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid bridge request"))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, errors.New("invalid bridge request"))
		return
	}
	principal := principalForRequest(r)
	response := s.bridge.Dispatch(r.Context(), iscpbridge.Principal{
		OwnerID: principal.OwnerID,
		ActorID: principal.ActorID,
	}, request)
	writeJSON(w, iscpbridge.HTTPStatus(response), response)
}
