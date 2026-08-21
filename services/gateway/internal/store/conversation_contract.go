package store

import (
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var errMessageJSONDecode = errors.New("decode persisted message JSON")

func prepareMessage(message app.Message, now time.Time) (app.Message, error) {
	if strings.TrimSpace(message.ID) == "" {
		message.ID = app.NewID("m")
	}
	if strings.TrimSpace(message.SessionID) == "" {
		return app.Message{}, errors.New("message session ID is required")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	message.CreatedAt = message.CreatedAt.UTC().Truncate(time.Microsecond)
	return cloneMessage(message), nil
}

func cloneMessage(message app.Message) app.Message {
	message.Attachments = append([]app.MessageAttachment(nil), message.Attachments...)
	message.RequestedMedia = append([]app.MessageMediaLocator(nil), message.RequestedMedia...)
	return message
}

func cloneMessages(messages []app.Message) []app.Message {
	out := make([]app.Message, len(messages))
	for index, message := range messages {
		out[index] = cloneMessage(message)
	}
	return out
}

func cloneMessageMap(values map[string][]app.Message) map[string][]app.Message {
	out := make(map[string][]app.Message, len(values))
	for sessionID, messages := range values {
		out[sessionID] = cloneMessages(messages)
	}
	return out
}

func findMessageByID(values map[string][]app.Message, id string) (app.Message, bool) {
	for _, messages := range values {
		for _, message := range messages {
			if message.ID == id {
				return cloneMessage(message), true
			}
		}
	}
	return app.Message{}, false
}
