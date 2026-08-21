package storetest

import (
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func MustSaveExternalChatSession(t testing.TB, repository store.ExternalChatRepository, session app.ExternalChatSession) app.ExternalChatSession {
	t.Helper()
	saved, err := repository.SaveExternalChatSession(t.Context(), session)
	if err != nil {
		t.Fatalf("save external chat session fixture: %v", err)
	}
	return saved
}

func MustSaveExternalChatMessage(t testing.TB, repository store.ExternalChatRepository, message app.ExternalChatMessage) app.ExternalChatMessage {
	t.Helper()
	saved, err := repository.SaveExternalChatMessage(t.Context(), message)
	if err != nil {
		t.Fatalf("save external chat message fixture: %v", err)
	}
	return saved
}

func MustFindExternalChatSession(t testing.TB, repository store.ExternalChatRepository, bindingID, externalChatID, externalThreadID string) (app.ExternalChatSession, bool) {
	t.Helper()
	session, found, err := repository.FindExternalChatSession(t.Context(), bindingID, externalChatID, externalThreadID)
	if err != nil {
		t.Fatalf("find external chat session fixture: %v", err)
	}
	return session, found
}

func MustFindExternalChatMessage(t testing.TB, repository store.ExternalChatRepository, chatSessionID, externalMessageID string) (app.ExternalChatMessage, bool) {
	t.Helper()
	message, found, err := repository.FindExternalChatMessageByExternalID(t.Context(), chatSessionID, externalMessageID)
	if err != nil {
		t.Fatalf("find external chat message fixture: %v", err)
	}
	return message, found
}

func MustListExternalChatMessages(t testing.TB, repository store.ExternalChatRepository, chatSessionID string, limit int) []app.ExternalChatMessage {
	t.Helper()
	messages, err := repository.ListExternalChatMessages(t.Context(), chatSessionID, limit)
	if err != nil {
		t.Fatalf("list external chat message fixtures: %v", err)
	}
	return messages
}
