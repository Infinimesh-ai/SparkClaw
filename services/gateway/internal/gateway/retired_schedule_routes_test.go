package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBackendCannotAcceptFutureWorkbenchSchedules(t *testing.T) {
	server, _ := credentialServer(t)
	for _, route := range []string{"/api/r3/schedules/lease", "/api/r3/schedules/request/renew", "/api/r3/schedules/request/cancel", "/api/v1/schedules/lease"} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, route, strings.NewReader(`{}`)))
		if response.Code != http.StatusNotFound {
			t.Fatalf("retired future schedule route %s returned %d", route, response.Code)
		}
	}
}
