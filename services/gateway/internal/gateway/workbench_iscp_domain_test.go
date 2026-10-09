package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func domainTestContext(t *testing.T) context.Context {
	return context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: "iscp-owner", ActorID: "iscp-actor", ClientID: "iscp-desktop-client"})
}
func domainTestRequest(operation string, body []byte) iscpworkbench.Request {
	request := workbenchISCPRequest(operation, body)
	request.Profile = "sparkclaw.workbench.transport.v2"
	request.OperationID = request.ID
	return request
}
func TestISCPDomainOwnerCASDurableReplayAndReceiptPrivacy(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	read := handler(ctx, domainTestRequest("settings.owner.get", nil))
	var state struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(read.Body, &state) != nil || read.Status != 200 || state.Revision == "" {
		t.Fatalf("read %+v", read)
	}
	request := domainTestRequest("settings.owner.patch", []byte(`{"display_name":"private owner canary","preferences":{"language":"en"}}`))
	request.ExpectedRevision = state.Revision
	result := handler(ctx, request)
	if result.Status != 200 {
		t.Fatalf("patch %+v", result)
	}
	persisted, found, err := repo.GetOwnerProfileByID(ctx, "iscp-owner")
	if err != nil || !found || persisted.DisplayName != "private owner canary" || persisted.Preferences["language"] != "en" {
		t.Fatalf("not effective %+v %v", persisted, err)
	}
	eventsBefore, _ := repo.EventsAfter(ctx, "", "")
	restarted, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	request.ID = domainTestRequest("id", nil).ID
	replay := restarted(ctx, request)
	if replay.Status != 200 || !bytes.Equal(replay.Body, result.Body) {
		t.Fatalf("lost result did not replay %+v", replay)
	}
	eventsAfter, _ := repo.EventsAfter(ctx, "", "")
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatal("replay wrote a second owner event")
	}
	receiptRequest := domainTestRequest("operations.receipt", nil)
	receiptRequest.Params = map[string]string{"operation_id": request.OperationID}
	receipt := restarted(ctx, receiptRequest)
	if receipt.Status != 403 {
		t.Fatalf("receipt %+v", receipt)
	}
	request.Body = []byte(`{"display_name":"different"}`)
	if got := restarted(ctx, request); got.Status != 409 {
		t.Fatalf("same ID changed input %+v", got)
	}
	stale := domainTestRequest("settings.owner.patch", []byte(`{"display_name":"stale"}`))
	stale.ExpectedRevision = state.Revision
	if got := restarted(ctx, stale); got.Status != 409 {
		t.Fatalf("stale revision %+v", got)
	}
	_ = filepath.Walk(filepath.Join(server.executionRoot, "iscp-domain-receipts"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			raw, _ := os.ReadFile(path)
			if bytes.Contains(raw, []byte("private owner canary")) {
				t.Fatal("unencrypted receipt")
			}
		}
		return nil
	})
}
func TestISCPDomainNotificationsWatermarkExcludesNewArrivals(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	for _, id := range []string{"first", "second"} {
		if _, _, err = repo.CreatePassiveNotification(ctx, gatewayTestNotification(id, "iscp-owner")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = repo.CreatePassiveNotification(ctx, gatewayTestNotification("foreign", "other-owner")); err != nil {
		t.Fatal(err)
	}
	result := handler(ctx, domainTestRequest("notifications.list", nil))
	var page struct {
		Value struct {
			Watermark     string                    `json:"watermark"`
			Notifications []passiveNotificationView `json:"notifications"`
		} `json:"value"`
	}
	if json.Unmarshal(result.Body, &page) != nil || len(page.Value.Notifications) != 2 {
		t.Fatalf("list %+v", result)
	}
	if _, _, err = repo.CreatePassiveNotification(ctx, gatewayTestNotification("later", "iscp-owner")); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"watermark": page.Value.Watermark})
	request := domainTestRequest("notifications.read_all", body)
	read := handler(ctx, request)
	if read.Status != 200 {
		t.Fatalf("read_all %+v", read)
	}
	later, _, _ := repo.GetPassiveNotification(ctx, "iscp-owner", "later")
	foreign, _, _ := repo.GetPassiveNotification(ctx, "other-owner", "foreign")
	if later.ReadAt != nil || foreign.ReadAt != nil {
		t.Fatal("watermark consumed new/foreign item")
	}
	same := handler(ctx, request)
	if same.Status != 200 || !bytes.Equal(same.Body, read.Body) {
		t.Fatalf("read_all replay %+v", same)
	}
	changedOwner := context.WithValue(ctx, requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: "other-owner", ActorID: "other-owner", ClientID: "iscp-desktop-client"})
	request.OperationID = domainTestRequest("id", nil).ID
	if got := handler(changedOwner, request); got.Status != 403 {
		t.Fatalf("cross-owner watermark %+v", got)
	}
}
func TestISCPDomainIntentCrashNeverReplaysMutation(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	journal, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest("settings.owner.patch", []byte(`{"display_name":"never execute"}`))
	request.ExpectedRevision = "original"
	raw, _ := json.Marshal(struct {
		Operation string
		Params    map[string]string
		Revision  string
		Body      json.RawMessage
	}{request.Operation, request.Params, request.ExpectedRevision, request.Body})
	if err = journal.save("iscp-test-deployment\x00iscp-owner\x00iscp-desktop-client\x00"+iscpTestInstallation, request.OperationID, iscpDomainReceipt{Version: 1, Digest: execution.Digest(raw)}); err != nil {
		t.Fatal(err)
	}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	result := handler(domainTestContext(t), request)
	if result.Status != 409 || !bytes.Contains(result.Body, []byte("operation_outcome_unknown")) {
		t.Fatalf("intent was replayed %+v", result)
	}
}

func TestISCPDomainApprovalBindsDigestRevisionAndNeverDuplicatesContinuation(t *testing.T) {
	var server *Server
	var effects atomic.Int32
	pending, _ := execution.NewPendingApproval("approval_domain", "files.write", "isolated approval", map[string]any{"name": "safe.txt"})
	execute := func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		decision, err := server.executions.AwaitApproval(ctx, e, pending)
		if err == nil && decision == "approve" {
			effects.Add(1)
		}
		return execution.Output{Content: "finished"}, err
	}
	created, _, cfg, textHandler := workbenchISCPFixture(t, execute)
	server = created
	bindWorkbenchISCP(t, textHandler)
	envelope := workbenchISCPEnvelope()
	raw, _ := json.Marshal(envelope)
	if got := textHandler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw)); got.Status != 202 {
		t.Fatalf("submit %+v", got)
	}
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	list := domainTestRequest("approvals.list", nil)
	list.Params = map[string]string{"request_id": iscpTestRequest}
	var snapshot struct {
		Revision    string                      `json:"revision"`
		InputDigest string                      `json:"input_digest"`
		Approvals   []execution.PendingApproval `json:"approvals"`
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result := handler(ctx, list)
		json.Unmarshal(result.Body, &snapshot)
		if len(snapshot.Approvals) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(snapshot.Approvals) != 1 {
		t.Fatal("approval never appeared")
	}
	body, _ := json.Marshal(map[string]string{"digest": pending.Digest, "decision": "approve", "input_digest": snapshot.InputDigest})
	decide := domainTestRequest("approvals.decide", body)
	decide.Params = map[string]string{"request_id": iscpTestRequest, "approval_id": pending.ApprovalID}
	decide.ExpectedRevision = "0"
	if got := handler(ctx, decide); got.Status != 409 || effects.Load() != 0 {
		t.Fatalf("stale approval %+v", got)
	}
	decide.OperationID = domainTestRequest("next", nil).ID
	decide.ExpectedRevision = snapshot.Revision
	accepted := handler(ctx, decide)
	if accepted.Status != 200 {
		t.Fatalf("approval %+v", accepted)
	}
	server.executions.Wait()
	if got := handler(ctx, decide); got.Status != 200 || !bytes.Equal(got.Body, accepted.Body) || effects.Load() != 1 {
		t.Fatalf("duplicate continuation %+v effects=%d", got, effects.Load())
	}
}
