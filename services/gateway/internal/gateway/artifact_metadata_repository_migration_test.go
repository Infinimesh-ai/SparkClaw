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

type failingArtifactMetadataStore struct {
	store.Store
	err error
}

func (s failingArtifactMetadataStore) SaveArtifactObject(context.Context, app.ArtifactObject) (app.ArtifactObject, error) {
	return app.ArtifactObject{}, s.err
}

func (s failingArtifactMetadataStore) ListArtifactObjects(context.Context, int) ([]app.ArtifactObject, error) {
	return nil, s.err
}

func (s failingArtifactMetadataStore) FindArtifactObjectByURI(context.Context, string, string, string) (app.ArtifactObject, bool, error) {
	return app.ArtifactObject{}, false, s.err
}

func TestArtifactListProjectsStoreFailure(t *testing.T) {
	backendCause := errors.New("private artifact metadata backend detail")
	repository := failingArtifactMetadataStore{
		Store: store.NewMemoryStore(),
		err:   &store.StoreError{Code: store.StoreErrorUnavailable, Operation: store.OperationArtifactMetadataList, Err: backendCause},
	}
	response := httptest.NewRecorder()
	(&Server{store: repository}).listArtifacts(response, httptest.NewRequest(http.MethodGet, "/api/artifacts", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "artifact metadata service is unavailable") || strings.Contains(response.Body.String(), backendCause.Error()) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestArtifactMetadataStoreErrorProjection(t *testing.T) {
	for _, testCase := range []struct {
		code       store.StoreErrorCode
		wantStatus int
		wantCopy   string
	}{
		{code: store.StoreErrorCanceled, wantStatus: http.StatusRequestTimeout, wantCopy: "artifact metadata request was canceled"},
		{code: store.StoreErrorTimeout, wantStatus: http.StatusGatewayTimeout, wantCopy: "artifact metadata operation timed out"},
		{code: store.StoreErrorCorrupt, wantStatus: http.StatusServiceUnavailable, wantCopy: "artifact metadata service is unavailable"},
	} {
		response := httptest.NewRecorder()
		writeArtifactMetadataStoreError(response, &store.StoreError{Code: testCase.code, Operation: store.OperationArtifactMetadataList})
		if response.Code != testCase.wantStatus || !strings.Contains(response.Body.String(), testCase.wantCopy) {
			t.Fatalf("code=%q status=%d body=%s", testCase.code, response.Code, response.Body.String())
		}
	}
}
