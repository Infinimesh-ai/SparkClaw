package gateway

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestMemoryStoreErrorProjection(t *testing.T) {
	privateCause := errors.New("private memory database host and SQL statement")
	for _, testCase := range []struct {
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{code: store.StoreErrorInvalid, wantStatus: http.StatusBadRequest, wantCopy: "memory request is invalid"},
		{code: store.StoreErrorNotFound, wantStatus: http.StatusNotFound, wantCopy: "memory record not found"},
		{code: store.StoreErrorConflict, wantStatus: http.StatusConflict, wantCopy: "memory candidate was already resolved"},
		{code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "memory request was canceled"},
		{code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "memory operation timed out"},
		{code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable, wantCopy: "memory service is unavailable"},
	} {
		t.Run(string(testCase.code), func(t *testing.T) {
			response := httptest.NewRecorder()
			writeMemoryStoreError(response, &store.StoreError{Code: testCase.code, Operation: store.OperationMemorySearch, Err: privateCause})
			if response.Code != testCase.wantStatus || !strings.Contains(response.Body.String(), testCase.wantCopy) {
				t.Fatalf("code=%q status=%d body=%s", testCase.code, response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), privateCause.Error()) {
				t.Fatalf("memory error projection leaked private Store cause: %s", response.Body.String())
			}
		})
	}
}
