package iscpbridge

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type bridgeOwnerFailureStore struct {
	store.Store
	seen context.Context
	err  error
}

func (s *bridgeOwnerFailureStore) GetOwnerProfileByID(ctx context.Context, _ string) (app.OwnerProfile, bool, error) {
	s.seen = ctx
	return app.OwnerProfile{}, false, s.err
}

func TestGatewayAdapterSessionCreatePropagatesContextAndReturnsRetryableOwnerFailure(t *testing.T) {
	privateCause := errors.New("private owner backend detail")
	wrapper := &bridgeOwnerFailureStore{Store: store.NewMemoryStore(), err: privateCause}
	adapter := NewGatewayAdapter(wrapper, func() AgentRuntime { return &adapterRuntime{started: make(chan struct{}, 1)} })
	request := validRequest(TypeSessionCreate, "request-owner-failure", "endpoint-app", "", "create-owner-failure", SessionCreatePayload{Title: "Owner context"})

	type contextKey struct{}
	marker := &struct{}{}
	ctx := context.WithValue(t.Context(), contextKey{}, marker)
	response := adapter.Dispatch(ctx, Principal{OwnerID: app.DefaultOwnerID, ActorID: app.DefaultOwnerID}, request)
	if response.Status != "error" || response.Error == nil || response.Error.Code != CodeTemporarilyUnavailable || !response.Error.Retryable ||
		response.Error.Message != "owner profile is temporarily unavailable" {
		t.Fatalf("response = %#v", response)
	}
	if wrapper.seen == nil || wrapper.seen.Value(contextKey{}) != marker {
		t.Fatal("Bridge did not pass its Dispatch context to OwnerRepository")
	}
}
