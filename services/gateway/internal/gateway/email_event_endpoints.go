package gateway

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/http"
)

func (s *Server) changeEmailAssignment(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var in struct {
		ConversationID  string `json:"conversation_id"`
		Title           string `json:"title"`
		ExpectedVersion int64  `json:"expected_version"`
		CommandKey      string `json:"command_key"`
	}
	if err := readEmailJSON(w, r, &in); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.ChangeAssignment(r.Context(), store.EmailManualAssignment{EmailCommand: store.EmailCommand{OwnerID: principalForRequest(r).OwnerID, CommandKey: in.CommandKey}, MailID: r.PathValue("mail"), ConversationID: in.ConversationID, Title: in.Title, ExpectedVersion: in.ExpectedVersion})
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
func (s *Server) renameEmailConversation(w http.ResponseWriter, r *http.Request) {
	if !s.emailManagementReady(w) {
		return
	}
	var in struct {
		Title           string `json:"title"`
		ExpectedVersion int64  `json:"expected_version"`
		CommandKey      string `json:"command_key"`
	}
	if err := readEmailJSON(w, r, &in); err != nil {
		writeEmailManagementError(w, err)
		return
	}
	out, err := s.emailManagement.RenameConversation(r.Context(), store.EmailConversationRename{EmailCommand: store.EmailCommand{OwnerID: principalForRequest(r).OwnerID, CommandKey: in.CommandKey}, ConversationID: r.PathValue("conversation"), Title: in.Title, ExpectedVersion: in.ExpectedVersion})
	if err != nil {
		writeEmailManagementError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
