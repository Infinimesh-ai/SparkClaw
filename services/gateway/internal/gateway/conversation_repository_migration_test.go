package gateway

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type conversationRepositoryFaultStore struct {
	store.Store
	addErr error
}

func (s conversationRepositoryFaultStore) AddMessage(context.Context, app.Message) (app.Message, error) {
	return app.Message{}, s.addErr
}

func TestConversationMessageAPIsRedactStoreFailures(t *testing.T) {
	root := t.TempDir()
	cfg := testConfig(root)
	base := store.NewMemoryStore()
	session := storetest.MustCreateSession(t, base, "Conversation failure")
	privateCause := errors.New("private postgres host and SQL statement")
	fault := conversationRepositoryFaultStore{
		Store: base,
		addErr: &store.StoreError{
			Code: store.StoreErrorUnavailable, Operation: store.OperationConversationAddMessage, Err: privateCause,
		},
	}
	tools := toolhub.New(cfg, fault)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(fault, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := New(cfg, fault, tools, runtime)
	testServer := httptest.NewServer(server.Handler())
	t.Cleanup(testServer.Close)

	for _, testCase := range []struct {
		name       string
		path       string
		wantStatus int
		wantEvent  string
	}{
		{name: "request", path: "/api/sessions/" + session.ID + "/messages", wantStatus: http.StatusServiceUnavailable},
		{name: "stream", path: "/api/sessions/" + session.ID + "/messages/stream", wantStatus: http.StatusCreated, wantEvent: "event: error"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response, err := http.Post(testServer.URL+testCase.path, "application/json", bytes.NewBufferString(`{"content":"hello"}`))
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != testCase.wantStatus || !strings.Contains(string(body), "conversation service is unavailable") {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if testCase.wantEvent != "" && !strings.Contains(string(body), testCase.wantEvent) {
				t.Fatalf("missing %q in stream body: %s", testCase.wantEvent, body)
			}
			if strings.Contains(string(body), privateCause.Error()) {
				t.Fatalf("response leaked private Store cause: %s", body)
			}
		})
	}
}
