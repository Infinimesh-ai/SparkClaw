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

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type sessionRepositoryFaultStore struct {
	store.Store
	createFn          func(context.Context, string) (app.Session, error)
	createWithScopeFn func(context.Context, string, string, string, string, bool) (app.Session, error)
	listFn            func(context.Context) ([]app.Session, error)
	getFn             func(context.Context, string) (app.Session, bool, error)
	updateFn          func(context.Context, string, string) (app.Session, error)
	deleteFn          func(context.Context, string) (app.Session, error)
}

func (s *sessionRepositoryFaultStore) CreateSession(ctx context.Context, title string) (app.Session, error) {
	if s.createFn != nil {
		return s.createFn(ctx, title)
	}
	return s.Store.CreateSession(ctx, title)
}

func (s *sessionRepositoryFaultStore) CreateSessionWithScope(ctx context.Context, title, ownerID, workspaceRoot, source string, hidden bool) (app.Session, error) {
	if s.createWithScopeFn != nil {
		return s.createWithScopeFn(ctx, title, ownerID, workspaceRoot, source, hidden)
	}
	return s.Store.CreateSessionWithScope(ctx, title, ownerID, workspaceRoot, source, hidden)
}

func (s *sessionRepositoryFaultStore) ListSessions(ctx context.Context) ([]app.Session, error) {
	if s.listFn != nil {
		return s.listFn(ctx)
	}
	return s.Store.ListSessions(ctx)
}

func (s *sessionRepositoryFaultStore) GetSession(ctx context.Context, id string) (app.Session, bool, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return s.Store.GetSession(ctx, id)
}

func (s *sessionRepositoryFaultStore) UpdateSessionTitle(ctx context.Context, id, title string) (app.Session, error) {
	if s.updateFn != nil {
		return s.updateFn(ctx, id, title)
	}
	return s.Store.UpdateSessionTitle(ctx, id, title)
}

func (s *sessionRepositoryFaultStore) DeleteSession(ctx context.Context, id string) (app.Session, error) {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	return s.Store.DeleteSession(ctx, id)
}

func TestSessionHTTPHandlersPropagateContextAndMapTypedErrors(t *testing.T) {
	type contextKey struct{}
	marker := &struct{ value string }{value: "request-owned"}
	for _, operation := range []string{"list", "get", "create", "update", "delete"} {
		for _, testCase := range []struct {
			name       string
			code       store.StoreErrorCode
			wantStatus int
			wantCopy   string
		}{
			{name: "invalid", code: store.StoreErrorInvalid, wantStatus: http.StatusBadRequest, wantCopy: "session request is invalid"},
			{name: "not found", code: store.StoreErrorNotFound, wantStatus: http.StatusNotFound, wantCopy: "session not found"},
			{name: "conflict", code: store.StoreErrorConflict, wantStatus: http.StatusConflict, wantCopy: "session cannot be changed"},
			{name: "canceled", code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "session request was canceled"},
			{name: "timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "session operation timed out"},
			{name: "unavailable", code: store.StoreErrorUnavailable, wantStatus: http.StatusServiceUnavailable, wantCopy: "session service is unavailable"},
			{name: "durability", code: store.StoreErrorDurability, wantStatus: http.StatusServiceUnavailable, wantCopy: "session service is unavailable"},
			{name: "unknown", code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable, wantCopy: "session service is unavailable"},
			{name: "corrupt", code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "session service is unavailable"},
			{name: "internal", code: store.StoreErrorInternal, wantStatus: http.StatusServiceUnavailable, wantCopy: "session service is unavailable"},
		} {
			t.Run(operation+"/"+testCase.name, func(t *testing.T) {
				base := store.NewMemoryStore()
				session := storetest.MustCreateSession(t, base, "session")
				fault := &sessionRepositoryFaultStore{Store: base}
				var seen context.Context
				rawCause := errors.New("private database host and SQL statement")
				failure := func(operation store.StoreOperation) error {
					return &store.StoreError{Code: testCase.code, Operation: operation, Err: rawCause}
				}
				switch operation {
				case "list":
					fault.listFn = func(ctx context.Context) ([]app.Session, error) {
						seen = ctx
						return nil, failure(store.OperationSessionList)
					}
				case "get":
					fault.getFn = func(ctx context.Context, _ string) (app.Session, bool, error) {
						seen = ctx
						return app.Session{}, false, failure(store.OperationSessionGet)
					}
				case "create":
					fault.createWithScopeFn = func(ctx context.Context, _, _, _, _ string, _ bool) (app.Session, error) {
						seen = ctx
						return app.Session{}, failure(store.OperationSessionCreateWithScope)
					}
				case "update":
					fault.updateFn = func(ctx context.Context, _, _ string) (app.Session, error) {
						seen = ctx
						return app.Session{}, failure(store.OperationSessionUpdateTitle)
					}
				case "delete":
					fault.deleteFn = func(ctx context.Context, _ string) (app.Session, error) {
						seen = ctx
						return app.Session{}, failure(store.OperationSessionDelete)
					}
				}
				server := &Server{store: fault}
				request, invoke := sessionRepositoryHTTPRequest(t, server, operation, session.ID)
				request = request.WithContext(context.WithValue(request.Context(), contextKey{}, marker))
				response := httptest.NewRecorder()
				invoke(response, request)
				body, err := io.ReadAll(response.Result().Body)
				if err != nil {
					t.Fatal(err)
				}
				if response.Code != testCase.wantStatus || !strings.Contains(string(body), testCase.wantCopy) || strings.Contains(string(body), rawCause.Error()) {
					t.Fatalf("status=%d body=%s", response.Code, body)
				}
				if seen == nil || seen.Value(contextKey{}) != marker {
					t.Fatal("handler did not propagate the request context")
				}
			})
		}
	}
}

func sessionRepositoryHTTPRequest(t testing.TB, server *Server, operation, sessionID string) (*http.Request, func(http.ResponseWriter, *http.Request)) {
	t.Helper()
	switch operation {
	case "list":
		return httptest.NewRequest(http.MethodGet, "/api/sessions", nil), server.listSessions
	case "get":
		request := httptest.NewRequest(http.MethodGet, "/api/sessions/"+sessionID, nil)
		request.SetPathValue("id", sessionID)
		return request, server.getSession
	case "create":
		request := httptest.NewRequest(http.MethodPost, "/api/sessions", bytes.NewBufferString(`{"title":"new"}`))
		request.Header.Set("Content-Type", "application/json")
		return request, server.createSession
	case "update":
		request := httptest.NewRequest(http.MethodPatch, "/api/sessions/"+sessionID, bytes.NewBufferString(`{"title":"renamed"}`))
		request.Header.Set("Content-Type", "application/json")
		request.SetPathValue("id", sessionID)
		return request, server.updateSession
	case "delete":
		request := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sessionID, nil)
		request.SetPathValue("id", sessionID)
		return request, server.deleteSession
	default:
		t.Fatalf("unknown operation %q", operation)
		return nil, nil
	}
}
