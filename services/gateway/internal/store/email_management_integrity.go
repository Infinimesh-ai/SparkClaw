package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var errEmailCorrupt = errors.New("corrupt email record")

func validateEmailRecord(r EmailRecord) error {
	if r.Owner == "" || r.Owner != normalizeConnectorOwner(r.Owner) || r.ID == "" || strings.ContainsAny(r.ID, "\x00\r\n") || !containsEmail([]string{"draft", "send_snapshot", "mailbox", "mail", "capture", "representation", "render_preview", "context", "conversation", "decision", "concern", "concern_link", "target", "dependency", "reference", "refresh", "job", "thread", "sync", "sync_failure", "view", "command", "counter", "summary", "sender_rule", "presentation"}, r.Kind) {
		return errEmailCorrupt
	}
	var identity struct {
		ID      string `json:"id"`
		OwnerID string `json:"owner_id"`
	}
	if len(r.Data) == 0 || r.Data[0] != '{' || json.Unmarshal(r.Data, &identity) != nil {
		return errEmailCorrupt
	}
	if identity.OwnerID != "" && identity.OwnerID != r.Owner {
		return errEmailCorrupt
	}
	if identity.ID != "" && identity.ID != r.ID && r.Kind != "concern_link" {
		return errEmailCorrupt
	}
	return nil
}
func validateEmailRecords(records map[string]EmailRecord) error {
	for key, r := range records {
		if key != emailRecordKey(r.Owner, r.Kind, r.ID) {
			return fmt.Errorf("%w: mismatched identity", errEmailCorrupt)
		}
		if err := validateEmailRecord(r); err != nil {
			return err
		}
	}
	return nil
}
