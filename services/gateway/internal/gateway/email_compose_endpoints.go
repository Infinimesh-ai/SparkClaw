package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"io"
	"net/http"
)

func (s *Server) registerEmailComposeRoutes() {
	s.mux.HandleFunc("GET /api/email/compose-capabilities", s.emailComposeCapabilities)
	s.mux.HandleFunc("GET /api/email/sent-sources", s.emailSentSources)
	s.mux.HandleFunc("GET /api/email/drafts", s.listEmailDrafts)
	s.mux.HandleFunc("GET /api/email/drafts/{draft}", s.listEmailDrafts)
	s.mux.HandleFunc("POST /api/email/drafts", s.saveEmailDraft)
	s.mux.HandleFunc("PUT /api/email/drafts/{draft}", s.saveEmailDraft)
	s.mux.HandleFunc("POST /api/email/drafts/{draft}/send", s.sendEmailDraft)
	s.mux.HandleFunc("POST /api/email/drafts/{draft}/reconcile", s.reconcileEmailDraft)
}
func (s *Server) emailComposeCapabilities(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.emailManagement.ComposeCapabilities())
}
func (s *Server) listEmailDrafts(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	if r.PathValue("draft") == "" {
		q, err := emailRequestQuery(r)
		if err != nil {
			writeEmailComposeError(w, err)
			return
		}
		out, err := s.emailManagement.DraftPage(r.Context(), q)
		if err != nil {
			writeEmailComposeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	rows, err := s.emailManagement.Drafts(r.Context(), principalForRequest(r).OwnerID, r.PathValue("draft"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	if r.PathValue("draft") != "" {
		writeJSON(w, http.StatusOK, rows[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}
func decodeEmailCompose(r *http.Request, v any) error {
	// A 200 KiB text body can expand sixfold when JSON escapes each character.
	// Bound the encoded request separately from the decoded field limits.
	body, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
	if err != nil || len(body) > 2<<20 {
		return emailmanagement.ErrInvalidInput
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return emailmanagement.ErrInvalidInput
	}
	return nil
}
func (s *Server) saveEmailDraft(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		ID              string   `json:"id"`
		ExpectedVersion int64    `json:"expected_version"`
		MailboxID       string   `json:"mailbox_id"`
		Mode            string   `json:"mode"`
		ReplyMailID     string   `json:"reply_mail_id"`
		To              []string `json:"to"`
		CC              []string `json:"cc"`
		Subject         string   `json:"subject"`
		Body            string   `json:"body"`
	}
	if decodeEmailCompose(r, &input) != nil {
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	if id := r.PathValue("draft"); id != "" {
		if input.ID != "" && input.ID != id {
			writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
			return
		}
		input.ID = id
	}
	draft, err := s.emailManagement.SaveDraft(r.Context(), principalForRequest(r).OwnerID, store.EmailDraft{ID: input.ID, MailboxID: input.MailboxID, Mode: input.Mode, ReplyMailID: input.ReplyMailID, To: input.To, CC: input.CC, Subject: input.Subject, Body: input.Body}, input.ExpectedVersion)
	if err != nil {
		writeEmailComposeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}
func (s *Server) sendEmailDraft(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		ExpectedVersion int64  `json:"expected_version"`
		IdempotencyKey  string `json:"idempotency_key"`
	}
	if decodeEmailCompose(r, &input) != nil {
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	draft, err := s.emailManagement.SendDraft(r.Context(), principalForRequest(r).OwnerID, r.PathValue("draft"), input.ExpectedVersion, input.IdempotencyKey)
	if err != nil {
		writeEmailComposeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}
func writeEmailComposeError(w http.ResponseWriter, err error) {
	for _, known := range []error{emailmanagement.ErrComposeUnavailable, emailmanagement.ErrNativeReplyUnavailable, emailmanagement.ErrMultipleRecipientsUnavailable} {
		if errors.Is(err, known) {
			writeJSON(w, http.StatusConflict, map[string]any{"code": known.Error(), "error": "Email action is unavailable; the draft is retained.", "retryable": false})
			return
		}
	}
	writeEmailManagementError(w, err)
}

func (s *Server) reconcileEmailDraft(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		SentMailID string `json:"sent_mail_id"`
	}
	if r.ContentLength != 0 {
		if err := decodeEmailCompose(r, &input); err != nil {
			writeEmailComposeError(w, emailmanagement.ErrInvalidInput)
			return
		}
	}
	var out store.EmailDraft
	var err error
	if input.SentMailID != "" {
		out, err = s.emailManagement.ConfirmDraftSource(r.Context(), principalForRequest(r).OwnerID, r.PathValue("draft"), input.SentMailID)
	} else {
		out, err = s.emailManagement.ReconcileDraft(r.Context(), principalForRequest(r).OwnerID, r.PathValue("draft"))
	}
	if err != nil {
		writeEmailComposeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) emailSentSources(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	q, err := emailRequestQuery(r)
	if err != nil {
		writeEmailComposeError(w, err)
		return
	}
	out, err := s.emailManagement.SentSources(r.Context(), q)
	if err != nil {
		writeEmailComposeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
