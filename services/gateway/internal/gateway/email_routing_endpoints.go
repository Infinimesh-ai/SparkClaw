package gateway

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/http"
)

func (s *Server) listEmailRoutedMessages(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	// Subtype is specific to notification queries; all other filters share validation.
	values := r.URL.Query()
	subtype := values.Get("subtype")
	validity := values.Get("validity")
	if len(values["validity"]) > 1 || (validity != "" && validity != "not_expired" && validity != "expired" && validity != "validity_unknown") {
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	values.Del("validity")
	switch subtype {
	case "", "verification", "promotion", "account_security", "general":
	default:
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	if len(values["subtype"]) > 1 {
		writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
		return
	}
	values.Del("subtype")
	clone := r.Clone(r.Context())
	copied := *r.URL
	copied.RawQuery = values.Encode()
	clone.URL = &copied
	q, err := emailRequestQuery(clone)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	var out emailmanagement.MessagesView
	if r.URL.Path == "/api/email/notifications" {
		q.NotificationSubtype = subtype
		q.Validity = validity
		out, err = s.emailManagement.Notifications(r.Context(), q)
	} else {
		if subtype != "" || validity != "" {
			writeEmailManagementError(w, emailmanagement.ErrInvalidInput)
			return
		}
		out, err = s.emailManagement.InteractionMails(r.Context(), q)
	}
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) getEmailSingleMessage(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	out, err := s.emailManagement.Message(r.Context(), principalForRequest(r).OwnerID, r.PathValue("mail"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) changeEmailClassification(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		Entry               string `json:"entry"`
		ExpectedVersion     int64  `json:"expected_version"`
		RememberSender      bool   `json:"remember_sender"`
		ExpectedRuleVersion int64  `json:"expected_rule_version"`
		CommandKey          string `json:"command_key"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.ChangeClassification(r.Context(), store.EmailClassificationOverride{
		EmailCommand: store.EmailCommand{OwnerID: principalForRequest(r).OwnerID, CommandKey: input.CommandKey}, MailID: r.PathValue("mail"), Entry: input.Entry, ExpectedVersion: input.ExpectedVersion, RememberSender: input.RememberSender, ExpectedRuleVersion: input.ExpectedRuleVersion,
	})
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) emailSenderRules(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	q, err := emailRequestQuery(r)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.SenderRules(r.Context(), q)
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "next_cursor": emailRuleNextCursor(out, q.Limit)})
}
func (s *Server) updateEmailSenderRule(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		Entry           string `json:"entry"`
		Enabled         bool   `json:"enabled"`
		ExpectedVersion int64  `json:"expected_version"`
		CommandKey      string `json:"command_key"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.ChangeSenderRule(r.Context(), store.EmailSenderRuleCommand{EmailCommand: store.EmailCommand{OwnerID: principalForRequest(r).OwnerID, CommandKey: input.CommandKey}, RuleID: r.PathValue("rule"), Entry: input.Entry, Enabled: input.Enabled, ExpectedVersion: input.ExpectedVersion})
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rule": out})
}

func (s *Server) revealEmailVerification(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	out, err := s.emailManagement.Verification(r.Context(), principalForRequest(r).OwnerID, r.PathValue("mail"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, out)
}

func emailRuleNextCursor(rules []app.EmailSenderRule, limit int) string {
	if len(rules) > 0 && len(rules) == limit {
		return rules[len(rules)-1].ID
	}
	return ""
}
