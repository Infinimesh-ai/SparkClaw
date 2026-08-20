package telegram

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type telegramOwnerFailureStore struct {
	store.Store
	seen context.Context
	err  error
}

func (s *telegramOwnerFailureStore) GetOwnerProfileByID(ctx context.Context, _ string) (app.OwnerProfile, bool, error) {
	s.seen = ctx
	return app.OwnerProfile{}, false, s.err
}

func TestHandleUpdatePropagatesContextAndReturnsRetryableOwnerFailure(t *testing.T) {
	privateCause := errors.New("private owner backend detail")
	wrapper := &telegramOwnerFailureStore{Store: store.NewMemoryStore(), err: privateCause}
	dispatcher := NewDispatcher(wrapper, &recordingRuntime{}, telegramTestConfig(t)).WithClient(&fakeBotAPI{})
	binding := activeTelegramBinding("binding-owner-failure", 9, 9)
	update := Update{UpdateID: 1, Message: telegramTextMessage(1, 9, 9, "hello")}

	type contextKey struct{}
	marker := &struct{}{}
	ctx := context.WithValue(t.Context(), contextKey{}, marker)
	err := dispatcher.HandleUpdate(ctx, binding, update)
	var connectorErr *ConnectorError
	if !errors.As(err, &connectorErr) || connectorErr.Code != CodeBindingUnavailable || !connectorErr.Retryable || !errors.Is(err, privateCause) {
		t.Fatalf("error = %#v", err)
	}
	if wrapper.seen == nil || wrapper.seen.Value(contextKey{}) != marker {
		t.Fatal("Telegram did not pass its worker context to OwnerRepository")
	}
}
