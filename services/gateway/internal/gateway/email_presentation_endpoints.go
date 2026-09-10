package gateway

import (
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) getEmailPresentations(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	values := r.URL.Query()
	for key, v := range values {
		if key != "language" && key != "target_kind" && key != "target_id" || key != "target_id" && len(v) != 1 {
			writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
			return
		}
	}
	q := store.EmailPresentationQuery{OwnerID: principalForRequest(r).OwnerID, TargetKind: values.Get("target_kind"), TargetIDs: values["target_id"], Language: values.Get("language")}
	out, err := s.emailManagement.Presentations(r.Context(), q)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) ensureEmailPresentations(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		TargetKind string   `json:"target_kind"`
		TargetIDs  []string `json:"target_ids"`
		Language   string   `json:"language"`
		Retry      bool     `json:"retry"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	q := store.EmailPresentationQuery{OwnerID: principalForRequest(r).OwnerID, TargetKind: input.TargetKind, TargetIDs: input.TargetIDs, Language: input.Language}
	out, err := s.emailManagement.EnsurePresentations(r.Context(), q, input.Retry)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
