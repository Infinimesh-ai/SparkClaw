package gateway

import (
	"errors"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// The host repository is scoped by authenticated owner and persisted session,
// never by browser-tab identity. An empty session ID is that owner's welcome draft.
func (s *Server) getWorkbenchDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.store.GetWorkbenchDraft(r.Context(), principalForRequest(r).OwnerID, r.PathValue("id"))
	if err != nil {
		writeWorkbenchDraftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func (s *Server) saveWorkbenchDraft(w http.ResponseWriter, r *http.Request) {
	var input app.WorkbenchDraft
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	draft, err := s.store.SaveWorkbenchDraft(r.Context(), principalForRequest(r).OwnerID, r.PathValue("id"), input)
	if err != nil {
		writeWorkbenchDraftError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

func writeWorkbenchDraftError(w http.ResponseWriter, err error) {
	switch store.StoreErrorCodeOf(err) {
	case store.StoreErrorConflict:
		writeError(w, http.StatusConflict, errors.New("draft changed; reload the current revision before saving"))
	case store.StoreErrorNotFound:
		writeError(w, http.StatusNotFound, errors.New("draft session not found"))
	case store.StoreErrorInvalid:
		writeError(w, http.StatusBadRequest, errors.New("draft content, attachments or revision is invalid"))
	default:
		writeSessionStoreError(w, err)
	}
}
