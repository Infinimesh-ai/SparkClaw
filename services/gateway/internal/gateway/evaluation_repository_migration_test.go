package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type failingEvaluationStore struct {
	store.Store
	err error
}

func (s failingEvaluationStore) SaveEvalRun(context.Context, app.EvalRun) (app.EvalRun, error) {
	return app.EvalRun{}, s.err
}

func (s failingEvaluationStore) GetEvalRun(context.Context, string) (app.EvalRun, bool, error) {
	return app.EvalRun{}, false, s.err
}

func (s failingEvaluationStore) ListEvalRuns(context.Context) ([]app.EvalRun, error) {
	return nil, s.err
}

func TestEvaluationEndpointsProjectStoreFailures(t *testing.T) {
	backendCause := errors.New("private evaluation backend detail")
	repository := failingEvaluationStore{
		Store: store.NewMemoryStore(),
		err:   &store.StoreError{Code: store.StoreErrorUnavailable, Operation: store.OperationEvaluationList, Err: backendCause},
	}
	server := &Server{cfg: testConfig(t.TempDir()), store: repository}
	for _, testCase := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
		req  *http.Request
	}{
		{name: "save", call: server.runEval, req: httptest.NewRequest(http.MethodPost, "/api/evals/run", strings.NewReader(`{"profile":"chaos"}`))},
		{name: "list", call: server.listEvals, req: httptest.NewRequest(http.MethodGet, "/api/evals", nil)},
		{name: "get", call: server.getEval, req: func() *http.Request {
			req := httptest.NewRequest(http.MethodGet, "/api/evals/eval", nil)
			req.SetPathValue("id", "eval")
			return req
		}()},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			testCase.call(response, testCase.req)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "evaluation service is unavailable") || strings.Contains(response.Body.String(), backendCause.Error()) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestEvaluationStoreErrorProjection(t *testing.T) {
	for _, testCase := range []struct {
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "evaluation request was canceled"},
		{code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "evaluation operation timed out"},
		{code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "evaluation service is unavailable"},
	} {
		response := httptest.NewRecorder()
		writeEvaluationStoreError(response, &store.StoreError{Code: testCase.code, Operation: store.OperationEvaluationGet})
		if response.Code != testCase.wantStatus || !strings.Contains(response.Body.String(), testCase.wantCopy) {
			t.Fatalf("code=%q status=%d body=%s", testCase.code, response.Code, response.Body.String())
		}
	}
}
