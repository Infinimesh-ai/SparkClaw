package weixin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type weixinOwnerFailureStore struct {
	store.Store
	seen context.Context
	err  error
}

func (s *weixinOwnerFailureStore) GetOwnerProfileByID(ctx context.Context, _ string) (app.OwnerProfile, bool, error) {
	s.seen = ctx
	return app.OwnerProfile{}, false, s.err
}

func TestHandleInboundPropagatesContextAndReturnsOwnerFailure(t *testing.T) {
	privateCause := errors.New("private owner backend detail")
	wrapper := &weixinOwnerFailureStore{Store: store.NewMemoryStore(), err: privateCause}
	dispatcher := NewDispatcher(wrapper, &fakeAgentRuntime{}, config.NotificationChannelConfig{})
	inbound := InboundMessage{
		Binding:    app.NotificationBinding{ID: "binding-owner-failure", OwnerID: app.DefaultOwnerID},
		FromUserID: "wx-owner-failure", Text: "hello", ExternalID: "message-owner-failure",
	}

	type contextKey struct{}
	marker := &struct{}{}
	ctx := context.WithValue(t.Context(), contextKey{}, marker)
	err := dispatcher.HandleInbound(ctx, inbound)
	if !errors.Is(err, privateCause) || !strings.Contains(err.Error(), "weixin owner profile is temporarily unavailable") {
		t.Fatalf("error = %v", err)
	}
	if wrapper.seen == nil || wrapper.seen.Value(contextKey{}) != marker {
		t.Fatal("Weixin did not pass its worker context to OwnerRepository")
	}
	if len(storetest.MustListSessions(t, wrapper)) != 0 {
		t.Fatal("Weixin created a session after owner lookup failure")
	}
}

func TestSyncerOwnerFailureDoesNotAdvanceProviderCursor(t *testing.T) {
	base := store.NewMemoryStore()
	binding := storetest.MustCreateNotificationBinding(t, base, app.NotificationBinding{
		ID: "binding-owner-cursor", OwnerID: app.DefaultOwnerID, Channel: "weixin",
		Status: "active", ProviderCursor: "cursor-before",
	})
	privateCause := errors.New("owner backend unavailable")
	wrapper := &weixinOwnerFailureStore{Store: base, err: privateCause}
	runtime := &fakeAgentRuntime{}
	dispatcher := NewDispatcher(wrapper, runtime, config.NotificationChannelConfig{})
	syncer := NewSyncer(wrapper).WithDispatcher(dispatcher)
	batch := inboundBatch{
		Binding: binding, Cursor: "cursor-after",
		Msgs: []inboundEnvelope{{
			FromUserID: "wx-owner-cursor", ExternalID: "message-owner-cursor",
			Items: []updateItem{{Type: 1, TextItem: updateTextItem{Text: "hello"}}},
		}},
	}

	type contextKey struct{}
	marker := &struct{}{}
	ctx := context.WithValue(t.Context(), contextKey{}, marker)
	syncer.processBatch(ctx, weixinTestRuntimeScope(), batch)
	stored, found := storetest.MustGetNotificationBinding(t, base, binding.ID)
	if !found || stored.ProviderCursor != "cursor-before" || runtime.handledCount() != 0 {
		t.Fatalf("binding=%#v found=%v handled=%d", stored, found, runtime.handledCount())
	}
	if wrapper.seen == nil || wrapper.seen.Value(contextKey{}) != marker {
		t.Fatal("Weixin batch did not pass its context to OwnerRepository")
	}
}
