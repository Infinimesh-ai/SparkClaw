package gateway

import (
	"context"
	"net/http"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
)

// getEmailMessageRenderPreview returns only the immutable semantic projection.
// Raw sender HTML/CSS/URLs and normalized body text are deliberately not
// exposed by this route.
func (s *Server) getEmailMessageRenderPreview(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	out, err := s.emailManagement.RenderPreview(r.Context(), principalForRequest(r).OwnerID, r.PathValue("mail"))
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	writeJSON(w, http.StatusOK, out)
}

// cleanupEmailSource manually removes locally stored originals. There is no
// confirmation step by design: mail metadata, parsed bodies and the cleanup
// state all survive, so the action is reversible in the only sense that matters
// to the window — the message stays readable.
func (s *Server) cleanupEmailSource(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var input struct {
		Scope      string `json:"scope"`
		MailID     string `json:"mail_id"`
		MailboxID  string `json:"mailbox_id"`
		Date       string `json:"date"`
		CommandKey string `json:"command_key"`
	}
	if err := readEmailJSON(w, r, &input); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	// A mailbox-wide or owner-wide cleanup loops over bounded batches, so it
	// gets the same extended budget as the other long email mutations.
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.emailManagement.CleanupSource(ctx, principalForRequest(r).OwnerID, emailmanagement.CleanupRequest{
		Scope: input.Scope, MailID: input.MailID, MailboxID: input.MailboxID, Date: input.Date, CommandKey: input.CommandKey,
	})
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
