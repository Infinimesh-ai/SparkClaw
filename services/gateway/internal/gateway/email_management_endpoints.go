package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (s *Server) emailManagementReady(w http.ResponseWriter) bool {
	if s.emailManagement != nil {
		return true
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "Email management is unavailable.", "code": "email_management_unavailable", "retryable": true})
	return false
}
func emailRequestQuery(r *http.Request) (store.EmailQuery, error) {
	q := store.EmailQuery{OwnerID: principalForRequest(r).OwnerID, Limit: 30}
	for key, values := range r.URL.Query() {
		if len(values) != 1 {
			return q, emailmanagement.ErrInvalidInput
		}
		switch key {
		case "mailbox_id":
			q.MailboxID = values[0]
		case "q":
			q.Search = strings.TrimSpace(values[0])
		case "cursor":
			q.After = values[0]
		case "limit":
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || n > 100 {
				return q, emailmanagement.ErrInvalidInput
			}
			q.Limit = n
		default:
			return q, emailmanagement.ErrInvalidInput
		}
	}
	return q, nil
}
func (s *Server) listEmailConversations(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	q, err := emailRequestQuery(r)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	q.Entry = "interaction"
	out, err := s.emailManagement.QueryConversations(r.Context(), q)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) getEmailConversation(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	out, err := s.emailManagement.Conversation(r.Context(), principalForRequest(r).OwnerID, r.PathValue("conversation"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) listEmailMessages(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	q, err := emailRequestQuery(r)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	q.ConversationID = r.PathValue("conversation")
	out, err := s.emailManagement.Messages(r.Context(), q)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) listEmailPending(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	q, err := emailRequestQuery(r)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.Pending(r.Context(), q)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) getEmailSyncStatus(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	out, err := s.emailManagement.Status(r.Context(), principalForRequest(r).OwnerID)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func readEmailJSON(w http.ResponseWriter, r *http.Request, input any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return emailmanagement.ErrInvalidInput
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return emailmanagement.ErrInvalidInput
	}
	return nil
}
func (s *Server) markEmailMessagesViewed(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		MailIDs []string `json:"mail_ids"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.View(r.Context(), principalForRequest(r).OwnerID, input.MailIDs)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) scheduleEmailSync(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		MailboxID string `json:"mailbox_id"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.Sync(r.Context(), principalForRequest(r).OwnerID, input.MailboxID)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) reanalyzeEmailMessage(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct{}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.Reanalyze(r.Context(), principalForRequest(r).OwnerID, r.PathValue("mail"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, out)
}
func (s *Server) getEmailMessageFile(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	query := r.URL.Query()
	if len(query) > 1 || (len(query) == 1 && len(query["part_id"]) != 1) || len(query.Get("part_id")) > 256 {
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	partID := query.Get("part_id")
	if partID == "" {
		partID = "original"
	}
	file, name, err := s.emailManagement.OpenFile(r.Context(), principalForRequest(r).OwnerID, r.PathValue("mail"), partID)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filepath.Base(name)}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, name, info.ModTime(), file)
}
func (s *Server) configureEmailIntake(w http.ResponseWriter, r *http.Request, provider string, enabled bool, version int64) {
	if !s.emailManagementReady(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	mailbox, err := s.emailManagement.Configure(ctx, principalForRequest(r).OwnerID, provider, enabled, version)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mailbox": emailmanagement.ProjectMailbox(mailbox)})
}
func writeEmailManagementError(w http.ResponseWriter, err error) {
	if emailautomation.ErrorCode(err) != "" {
		writeEmailError(w, err)
		return
	}
	code, status, message, retryable := "email_management_unavailable", http.StatusServiceUnavailable, "Email management is temporarily unavailable.", true
	switch {
	case errors.Is(err, emailmanagement.ErrInvalidInput), store.StoreErrorCodeOf(err) == store.StoreErrorInvalid:
		code, status, message, retryable = "email_invalid_request", http.StatusBadRequest, "The email request is invalid.", false
	case errors.Is(err, emailmanagement.ErrNotFound), errors.Is(err, os.ErrNotExist), store.StoreErrorCodeOf(err) == store.StoreErrorNotFound:
		code, status, message, retryable = "email_not_found", http.StatusNotFound, "The email record or source was not found.", false
	case errors.Is(err, emailmanagement.ErrNotEnabled):
		code, status, message, retryable = "email_receiving_paused", http.StatusConflict, "Enable receiving for a verified account before syncing.", false
	case errors.Is(err, emailmanagement.ErrConflict), store.StoreErrorCodeOf(err) == store.StoreErrorConflict:
		code, status, message = "email_conflict", http.StatusConflict, "Email changed. Refresh and retry."
	case errors.Is(err, emailmanagement.ErrProjectionBusy):
		code = "email_projection_busy"
	case errors.Is(err, context.DeadlineExceeded):
		code = "email_timeout"
	}
	writeJSON(w, status, map[string]any{"error": message, "code": code, "retryable": retryable})
}
