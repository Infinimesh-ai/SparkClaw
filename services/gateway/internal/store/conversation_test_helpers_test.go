package store

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func mustAddMessage(t testing.TB, repository ConversationRepository, message app.Message) app.Message {
	t.Helper()
	stored, err := repository.AddMessage(t.Context(), message)
	if err != nil {
		t.Fatalf("add message: %v", err)
	}
	return stored
}

func mustListMessages(t testing.TB, repository ConversationRepository, sessionID string) []app.Message {
	t.Helper()
	messages, err := repository.ListMessages(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("list messages: %v", err)
	}
	return messages
}

func mustMessageEventHead(t testing.TB, repository ConversationRepository, sessionID string) string {
	t.Helper()
	cursor, err := repository.MessageEventHead(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("message event head: %v", err)
	}
	return cursor
}

func mustMessageEventsAfter(t testing.TB, repository ConversationRepository, sessionID, after string, limit int) MessageEventPage {
	t.Helper()
	page, err := repository.MessageEventsAfter(t.Context(), sessionID, after, limit)
	if err != nil {
		t.Fatalf("message events after: %v", err)
	}
	return page
}
