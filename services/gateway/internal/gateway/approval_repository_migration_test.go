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

type approvalListFaultStore struct {
	runtimeTestRepository
	err error
}

func (s approvalListFaultStore) ListApprovals(context.Context, string) ([]app.Approval, error) {
	return nil, s.err
}

func TestApprovalAPIRedactsRepositoryFailures(t *testing.T) {
	privateCause := errors.New("private postgres host and approval SQL")
	base := store.NewMemoryStore()
	fault := approvalListFaultStore{
		runtimeTestRepository: base,
		err:                   &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationApprovalList, Err: privateCause},
	}
	cfg := testConfig(t.TempDir())
	tools := toolhub.New(cfg, fault)
	t.Cleanup(func() { _ = tools.Close() })
	runtime := agent.NewRuntime(fault, tools, policy.New(cfg), modelrouter.New(cfg), trace.NewWriter(cfg.Storage.TraceDir))
	server := httptest.NewServer(New(cfg, fault, tools, runtime).Handler())
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/api/approvals")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	response.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "approval service is unavailable") {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), privateCause.Error()) {
		t.Fatalf("approval API leaked private Store cause: %s", body)
	}
}
