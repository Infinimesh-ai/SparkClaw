package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestISCPDomainEventOutboxReplaysUntilACKAndFencesScopeEpoch(t *testing.T) {
	server, repo, cfg, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	journal, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{server: server, config: cfg, receipts: journal}
	session := iscpworkbench.SessionInfo{GrantRevision: 1, Scopes: []string{"notifications.read", "settings.read"}}
	ctx := domainTestContext(t)
	call := func(op string, body any) iscpDomainResult {
		raw, _ := json.Marshal(body)
		return adapter.eventsWithSession(ctx, domainTestRequest(op, raw), session)
	}
	type packet struct {
		Cursor   string
		Revision uint64
		Epoch    string
		Reset    bool
		Events   []json.RawMessage
	}
	decode := func(r iscpDomainResult) packet {
		t.Helper()
		var out packet
		if r.status != 200 || json.Unmarshal(r.body, &out) != nil {
			t.Fatalf("event %d %s", r.status, r.body)
		}
		return out
	}
	snapshot := decode(call(iscpworkbench.OperationEventsSnapshot, map[string]any{"request_ids": []string{}, "categories": []string{"notifications", "settings"}}))
	if !snapshot.Reset || snapshot.Revision != 1 || snapshot.Epoch == "" {
		t.Fatal(snapshot)
	}
	if r := call(iscpworkbench.OperationEventsAck, map[string]any{"cursor": snapshot.Cursor}); r.status != 200 {
		t.Fatal(r)
	}
	_, err = repo.SaveOwnerProfile(ctx, app.OwnerProfile{ID: "iscp-owner", DisplayName: "private event canary"})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repo.CreatePassiveNotification(ctx, gatewayTestNotification("event-note", "iscp-owner"))
	if err != nil {
		t.Fatal(err)
	}
	result := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": snapshot.Cursor, "limit": 1})
	next := decode(result)
	if next.Revision != 2 || len(next.Events) != 1 || bytes.Contains(result.body, []byte("private event canary")) {
		t.Fatalf("unbounded or private event %s", result.body)
	}
	// Recreate the adapter and encrypted journal while the same Gateway epoch lives.
	journal, err = newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	adapter = &iscpDomainAdapter{server: server, config: cfg, receipts: journal}
	replay := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": snapshot.Cursor})
	if !bytes.Equal(replay.body, result.body) {
		t.Fatalf("unacked packet lost: %s", replay.body)
	}
	if r := call(iscpworkbench.OperationEventsAck, map[string]any{"cursor": next.Cursor}); r.status != 200 {
		t.Fatal(r)
	}
	session.Scopes = []string{"notifications.read"}
	if r := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": next.Cursor}); r.status != 403 {
		t.Fatalf("scope revoked %+v", r)
	}
	session.Scopes = []string{"notifications.read", "settings.read"}
	entries, _ := os.ReadDir(journal.root)
	newer := decode(call(iscpworkbench.OperationEventsSnapshot, map[string]any{"categories": []string{"notifications"}}))
	after, _ := os.ReadDir(journal.root)
	if len(after) != len(entries) {
		t.Fatal("snapshot leaked persistent subscriptions")
	}
	if r := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": next.Cursor}); r.status != 409 {
		t.Fatal("replaced cursor accepted", r)
	}
	session.GrantRevision++
	if r := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": newer.Cursor}); r.status != 409 {
		t.Fatal("cursor crossed authorization revision", r)
	}
	session.GrantRevision--
	server.started = server.started.Add(time.Second)
	if r := call(iscpworkbench.OperationEventsPull, map[string]any{"cursor": newer.Cursor}); r.status != 409 {
		t.Fatal("restart cursor accepted", r)
	}
}

func TestISCPDomainToolsUseExistingExecutionLedgerAndFilteredDefinitions(t *testing.T) {
	var calls atomic.Int32
	server, _, cfg, textHandler := workbenchISCPFixture(t, func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		authorization, ok := ctx.Value(executionAuthorizationKey{}).(executionAuthorization)
		if !ok || !slices.Equal(authorization.AllowedTools, []string{"files.read"}) {
			t.Error("execution lost trusted allowlist")
		}
		calls.Add(1)
		return execution.Output{Content: "controlled tool execution"}, nil
	})
	bindWorkbenchISCP(t, textHandler)
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	policy := iscpauth.Policy{Version: 2, Revision: 1, State: iscpauth.Active, Scopes: []string{"tools.invoke", "tool.files.read"}}
	session := iscpworkbench.SessionInfo{GrantRevision: 1, Scopes: policy.Scopes, QualifiedOperations: []string{iscpworkbench.OperationToolsInvoke}, CheckAuthorization: func(context.Context) (iscpauth.Policy, error) { return policy, nil }}
	ctx := domainTestContext(t)
	list := adapter.toolsWithSession(ctx, domainTestRequest(iscpworkbench.OperationToolsList, nil), session)
	var tools struct{ Tools []app.ToolDefinition }
	if list.status != 200 || json.Unmarshal(list.body, &tools) != nil || len(tools.Tools) != 1 || tools.Tools[0].Name != "files.read" {
		t.Fatalf("unfiltered tools %s", list.body)
	}
	body, _ := json.Marshal(workbenchISCPEnvelope())
	request := domainTestRequest(iscpworkbench.OperationToolsInvoke, body)
	accepted := adapter.toolsWithSession(ctx, request, session)
	if accepted.status != 202 {
		t.Fatalf("submit %d %s", accepted.status, accepted.body)
	}
	server.executions.Wait()
	if replay := adapter.toolsWithSession(ctx, request, session); replay.status != 202 {
		t.Fatal(replay)
	}
	server.executions.Wait()
	if calls.Load() != 1 {
		t.Fatal("replay forked execution ledger")
	}
	status, err := server.executions.Lookup("iscp-owner", "iscp-desktop-client", request.RequestID)
	if err != nil || status.State != "completed" {
		t.Fatal(status, err)
	}
	request.RequestID = "66666666-6666-4666-8666-666666666666"
	if result := adapter.toolsWithSession(ctx, request, session); result.status != 409 {
		t.Fatal("unbound envelope accepted")
	}
	session.Scopes = []string{"tools.invoke"}
	if result := adapter.toolsWithSession(ctx, request, session); result.status != 403 {
		t.Fatal("generic tool scope granted wildcard")
	}
}

func TestISCPDomainObjectPurposeBudgetsKeepExecutionEnvelopeBounded(t *testing.T) {
	for _, operation := range []string{iscpworkbench.OperationSubmit, iscpworkbench.OperationToolsInvoke} {
		if _, ok := domainBodyObjectLimit(operation, "request_body"); ok {
			t.Fatal("execution borrowed generic body budget")
		}
		if limit, ok := domainBodyObjectLimit(operation, "execution_request"); !ok || limit != execution.ContextBytes {
			t.Fatal(limit, ok)
		}
	}
	for purpose, want := range map[string]int64{"context": 1 << 20, "request_body": 8 << 20} {
		if limit, ok := domainBodyObjectLimit(iscpworkbench.OperationBrowserHostReply, purpose); !ok || limit != want {
			t.Fatal(purpose, limit, ok)
		}
	}
	if _, ok := domainBodyObjectLimit(iscpworkbench.OperationBrowserHostReply, "speech_audio"); ok {
		t.Fatal("unrelated object purpose admitted")
	}
}
