package gateway

import (
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
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/trace"
)

type gatewayOwnerFailureStore struct {
	store.Store
	err  error
	seen context.Context
}

func (s *gatewayOwnerFailureStore) GetOwnerProfile(ctx context.Context) (app.OwnerProfile, error) {
	s.seen = ctx
	return app.OwnerProfile{}, s.err
}

func TestOwnerEndpointPropagatesRequestContextAndProjectsSafeStoreErrors(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{name: "timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "owner profile request timed out"},
		{name: "unavailable", code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantCopy: "owner profiles are temporarily unavailable"},
		{name: "corrupt", code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "owner profiles are temporarily unavailable"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := testConfig(root)
			base := store.NewMemoryStore()
			rawCause := "private database host and statement"
			wrapped := &gatewayOwnerFailureStore{
				Store: base,
				err: &store.StoreError{
					Code: testCase.code, Operation: store.OperationOwnerProfileGet, Err: errors.New(rawCause),
				},
			}
			tools := toolhub.New(cfg, wrapped)
			runtime := agent.NewRuntime(wrapped, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
			server := New(cfg, wrapped, tools, runtime)

			type contextKey struct{}
			marker := &struct{ value string }{value: "request-owned"}
			request := httptest.NewRequest(http.MethodGet, "/api/owner", nil)
			request = request.WithContext(context.WithValue(request.Context(), contextKey{}, marker))
			recorder := httptest.NewRecorder()
			server.Handler().ServeHTTP(recorder, request)

			body, err := io.ReadAll(recorder.Result().Body)
			if err != nil {
				t.Fatal(err)
			}
			if recorder.Code != testCase.wantStatus || !strings.Contains(string(body), testCase.wantCopy) || strings.Contains(string(body), rawCause) {
				t.Fatalf("status=%d body=%s", recorder.Code, body)
			}
			if wrapped.seen == nil || wrapped.seen.Value(contextKey{}) != marker {
				t.Fatal("owner endpoint did not pass the request-owned context")
			}
		})
	}
}
