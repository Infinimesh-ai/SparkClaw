package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type gatewayOnboardingRepository struct {
	*store.MemoryStore
	listContext context.Context
	listErr     error
}

func (r *gatewayOnboardingRepository) ListISCPOnboardings(ctx context.Context, ownerID string) ([]app.ISCPOnboarding, error) {
	r.listContext = ctx
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.MemoryStore.ListISCPOnboardings(ctx, ownerID)
}

func TestListISCPOnboardingsPropagatesRequestContext(t *testing.T) {
	repository := &gatewayOnboardingRepository{MemoryStore: store.NewMemoryStore()}
	server := &Server{iscpPairing: iscppairing.New(repository, iscppairing.Options{})}
	type contextKey struct{}
	requestContext := context.WithValue(context.Background(), contextKey{}, "request-value")
	request := httptest.NewRequest(http.MethodGet, "/api/iscp-pairing/onboardings", nil).WithContext(requestContext)
	response := httptest.NewRecorder()

	server.listISCPOnboardings(response, request)
	if response.Code != http.StatusOK || repository.listContext == nil || repository.listContext.Value(contextKey{}) != "request-value" {
		t.Fatalf("status=%d body=%s context=%v", response.Code, response.Body.String(), repository.listContext)
	}
}

func TestListISCPOnboardingsMapsStoreFailuresWithoutLeakingCause(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		code       store.StoreErrorCode
		wantStatus int
	}{
		{name: "timeout", code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout},
		{name: "durability", code: store.StoreErrorDurability, wantStatus: http.StatusServiceUnavailable},
		{name: "unknown outcome", code: store.StoreErrorUnknownOutcome, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repository := &gatewayOnboardingRepository{
				MemoryStore: store.NewMemoryStore(),
				listErr: &store.StoreError{
					Code: testCase.code, Operation: store.OperationISCPOnboardingList,
					Err: errors.New("postgres://owner:secret@database/private-path"),
				},
			}
			server := &Server{iscpPairing: iscppairing.New(repository, iscppairing.Options{})}
			request := httptest.NewRequest(http.MethodGet, "/api/iscp-pairing/onboardings", nil)
			response := httptest.NewRecorder()

			server.listISCPOnboardings(response, request)
			if response.Code != testCase.wantStatus || strings.Contains(response.Body.String(), "secret") || strings.Contains(response.Body.String(), "private-path") {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}
