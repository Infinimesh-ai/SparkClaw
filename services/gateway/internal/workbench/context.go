// Package workbench defines business rules shared by host and installed workbenches.
package workbench

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

// ValidateInput bounds transport-independent input before model/token admission.
// Token capacity is still checked by the active Model Router for every execution.
func ValidateInput(content string) error {
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) || len(content) > app.WorkbenchInputBytes {
		return errors.New("workbench input exceeds the supported UTF-8 input budget")
	}
	return nil
}

// SelectMessages is the common chronological context selection. Repository or
// envelope adapters supply only the admitted conversation; assistant text carries
// no approval, ingress, tool receipt or executable authority.
func SelectMessages(messages []app.Message, currentRun string, limit int) []app.Message {
	if limit <= 0 {
		return nil
	}
	selected := make([]app.Message, 0, min(len(messages), limit))
	for _, message := range messages {
		if currentRun != "" && message.RunID == currentRun {
			continue
		}
		role := strings.TrimSpace(message.Role)
		if role != "user" && role != "assistant" || strings.TrimSpace(message.Content) == "" && len(message.Attachments) == 0 {
			continue
		}
		// Imported envelopes and host history have the same per-message bound.
		message.Content = TruncateUTF8(message.Content, app.WorkbenchInputBytes)
		selected = append(selected, message)
	}
	if len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}
	return selected
}

func TruncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}
