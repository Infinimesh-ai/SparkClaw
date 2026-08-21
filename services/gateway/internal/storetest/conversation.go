package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustAddMessage(t testing.TB, repository store.ConversationRepository, message app.Message) app.Message {
	t.Helper()
	stored, err := repository.AddMessage(t.Context(), message)
	if err != nil {
		t.Fatalf("add message fixture: %v", err)
	}
	return stored
}

func MustListMessages(t testing.TB, repository store.ConversationRepository, sessionID string) []app.Message {
	t.Helper()
	messages, err := repository.ListMessages(t.Context(), sessionID)
	if err != nil {
		t.Fatalf("list message fixtures: %v", err)
	}
	return messages
}
