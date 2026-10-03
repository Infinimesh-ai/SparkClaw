package gateway

import (
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3mail"
	"net/http"
)

func WithR3MailSync(service *r3mail.Service) Option { return func(s *Server) { s.r3Mail = service } }
func (s *Server) registerR3MailRoutes() {
	s.mux.HandleFunc("GET /api/r3/mail/mailboxes", s.r3Mailboxes)
	s.mux.HandleFunc("POST /api/r3/mail/{mailbox}/sync", s.r3MailSync)
}
func (s *Server) r3Mailboxes(w http.ResponseWriter, r *http.Request) {
	principal, err := s.r3Principal(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "r3_installation_required"})
		return
	}
	if s.r3Mail == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "r3_mail_unavailable"})
		return
	}
	if len(r.URL.Query()) != 0 {
		writeR3MailError(w, r3mail.ErrInvalid)
		return
	}
	boxes, err := s.r3Mail.Mailboxes(r.Context(), principal.OwnerID)
	if err != nil {
		writeR3MailError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{"mailboxes": boxes})
}
func (s *Server) r3MailSync(w http.ResponseWriter, r *http.Request) {
	principal, err := s.r3Principal(r)
	if err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"code": "r3_installation_required"})
		return
	}
	if s.r3Mail == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"code": "r3_mail_unavailable"})
		return
	}
	var input struct {
		Cursor *string `json:"cursor"`
		Limit  *int    `json:"limit"`
	}
	if len(r.URL.Query()) != 0 || readEmailJSON(w, r, &input) != nil || input.Cursor == nil || input.Limit == nil {
		writeR3MailError(w, r3mail.ErrInvalid)
		return
	}
	out, err := s.r3Mail.Sync(r.Context(), principal.OwnerID, r.PathValue("mailbox"), *input.Cursor, *input.Limit)
	if err != nil {
		writeR3MailError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}
func writeR3MailError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "r3_mail_unavailable"
	switch {
	case errors.Is(err, r3mail.ErrReset):
		status, code = http.StatusConflict, "r3_mail_reset_required"
	case errors.Is(err, r3mail.ErrInvalid):
		status, code = http.StatusBadRequest, "r3_mail_invalid"
	case errors.Is(err, r3mail.ErrNotFound):
		status, code = http.StatusNotFound, "r3_mail_not_found"
	case errors.Is(err, r3mail.ErrLimit):
		status, code = http.StatusInsufficientStorage, "r3_mail_capacity"
	}
	writeJSON(w, status, map[string]any{"code": code})
}
