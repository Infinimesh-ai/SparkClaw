package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"testing"
	"time"
)

func TestWorkbenchAdmissionFreezesBoundedHistoryBeforeWorkerStarts(t *testing.T) {
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "frozen context")
	storetest.MustAddMessage(t, base, app.Message{ID: "prior", SessionID: session.ID, Role: "user", Content: "original context", CreatedAt: time.Now().UTC().Add(-time.Minute)})
	counting := &historyQueryCountingRepository{Repository: base}
	runtime := Runtime{store: counting}
	snapshot, err := runtime.PrepareWorkbenchContext(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	storetest.MustAddMessage(t, base, app.Message{ID: "later", SessionID: session.ID, Role: "assistant", Content: "later completion must not change admitted context", CreatedAt: time.Now().UTC()})
	ctx := WithWorkbenchContext(t.Context(), snapshot)
	history, err := runtime.buildInvocationHistory(ctx, app.AgentRun{ID: "admitted", SessionID: session.ID, StartedAt: snapshot.Before}, "current")
	if err != nil || len(history.MessageCandidates) != 1 || history.MessageCandidates[0].ID != "prior" {
		t.Fatalf("context changed: %+v %v", history, err)
	}
	if messages, tools, episodes := counting.recentCounts(); messages != 1 || tools != 1 || episodes != 1 {
		t.Fatalf("queried mutable history again: %d %d %d", messages, tools, episodes)
	}
	next, err := runtime.PrepareWorkbenchContext(t.Context(), session.ID)
	if err != nil || next.Digest == snapshot.Digest {
		t.Fatalf("next admission lost new context: %s %s %v", snapshot.Digest, next.Digest, err)
	}
}

func TestWorkbenchRuntimeRetainsSnapshotAfterStoreTimeNormalization(t *testing.T) {
	cfg := agentTestConfig()
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "snapshot execution")
	tools := toolhub.New(cfg, base)
	defer tools.Close()
	runtime := NewRuntime(base, tools, policy.New(cfg), modelrouter.New(cfg), nil)
	counting := &historyQueryCountingRepository{Repository: runtime.store}
	runtime.store = counting
	snapshot, err := runtime.PrepareWorkbenchContext(t.Context(), session.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithWorkbenchContext(t.Context(), snapshot)
	result, err := runtime.HandleAdmittedWorkbenchMessage(ctx, session.ID, "owner-message", "admitted-run", "hello", nil, app.MessageIngressContext{OwnerID: app.DefaultOwnerID, Source: app.MessageSourceContext{Kind: app.MessageSourceWeb, Adapter: "web", EndpointID: "web:test"}, ReturnRoute: app.ReturnRoute{Mode: app.ReturnToSource, SourceEndpointID: "web:test"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Run.StartedAt.Equal(snapshot.Before) {
		t.Fatal("run changed admission cutoff")
	}
	if messages, tools, episodes := counting.recentCounts(); messages != 1 || tools != 1 || episodes != 1 {
		t.Fatalf("history was re-read after store normalization: %d %d %d", messages, tools, episodes)
	}
}
