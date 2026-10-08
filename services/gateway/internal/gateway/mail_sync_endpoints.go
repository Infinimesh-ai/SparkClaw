package gateway

import (
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mailsync"
	"net/http"
	"net/url"
)

func WithMailSync(service *mailsync.Service) Option { return func(s *Server) { s.mailSync = service } }
func (s *Server) registerMailSyncRoutes() {
	s.mux.HandleFunc("GET /api/v1/mail/mailboxes", s.mailSyncMailboxes)
	s.mux.HandleFunc("POST /api/v1/mail/{mailbox}/sync", s.syncMail)
	s.mux.HandleFunc("GET /api/v1/mail/{mailbox}/messages/{mail}/attachments/{part}", s.mailSyncAttachment)
}

func (s *Server) mailSyncAttachment(w http.ResponseWriter, r *http.Request) {
	principal, _, err := s.executionReadPrincipal(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "installation_required"})
		return
	}
	if s.emailManagement == nil {
		writeMailSyncError(w, errors.New("mail service unavailable"))
		return
	}
	if len(r.URL.Query()) != 0 || r.PathValue("part") == "original" {
		writeMailSyncError(w, mailsync.ErrInvalid)
		return
	}
	mail, found, err := s.emailManagement.ClientSyncMail(r.Context(), principal.OwnerID, r.PathValue("mail"))
	if err != nil {
		writeMailSyncError(w, err)
		return
	}
	if !found || mail.MailboxID != r.PathValue("mailbox") {
		writeMailSyncError(w, mailsync.ErrNotFound)
		return
	}
	clone := r.Clone(r.Context())
	copyURL := *r.URL
	copyURL.RawQuery = url.Values{"part_id": []string{r.PathValue("part")}}.Encode()
	clone.URL = &copyURL
	s.getEmailMessageFile(w, clone)
}
func (s *Server) mailSyncMailboxes(w http.ResponseWriter, r *http.Request) {
	principal, _, err := s.executionReadPrincipal(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "installation_required"})
		return
	}
	if s.mailSync == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "mail_sync_unavailable"})
		return
	}
	if len(r.URL.Query()) != 0 {
		writeMailSyncError(w, mailsync.ErrInvalid)
		return
	}
	boxes, err := s.mailSync.Mailboxes(r.Context(), principal.OwnerID)
	if err != nil {
		writeMailSyncError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": boxes})
}
func (s *Server) syncMail(w http.ResponseWriter, r *http.Request) {
	principal, err := s.executionPrincipal(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "installation_required"})
		return
	}
	if s.mailSync == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "mail_sync_unavailable"})
		return
	}
	var input struct {
		Cursor *string `json:"cursor"`
		Limit  *int    `json:"limit"`
	}
	if len(r.URL.Query()) != 0 || readEmailJSON(w, r, &input) != nil || input.Cursor == nil || input.Limit == nil {
		writeMailSyncError(w, mailsync.ErrInvalid)
		return
	}
	out, err := s.mailSync.Sync(r.Context(), principal.OwnerID, r.PathValue("mailbox"), *input.Cursor, *input.Limit)
	if err != nil {
		writeMailSyncError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}
func writeMailSyncError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "mail_sync_unavailable"
	switch {
	case errors.Is(err, mailsync.ErrReset):
		status, code = http.StatusConflict, "mail_sync_reset_required"
	case errors.Is(err, mailsync.ErrInvalid):
		status, code = http.StatusBadRequest, "mail_sync_invalid"
	case errors.Is(err, mailsync.ErrNotFound):
		status, code = http.StatusNotFound, "mail_sync_not_found"
	case errors.Is(err, mailsync.ErrLimit):
		status, code = http.StatusInsufficientStorage, "mail_sync_capacity"
	}
	writeJSON(w, status, map[string]any{"code": code})
}
