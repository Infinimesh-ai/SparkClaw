package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpbridge"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func phoneReadRequest(operation string, body []byte) iscpworkbench.Request {
	request := domainTestRequest(operation, body)
	request.OperationID = ""
	return request
}

func TestISCPDomainPhoneProjectionsMatchBridgeShapeAndOwnerScope(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	mine, err := repo.CreateSessionWithScope(ctx, "phone fixture", "iscp-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := repo.CreateSessionWithScope(ctx, "foreign fixture", "other-owner", "", "", false)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, run := range []app.AgentRun{
		{ID: "run_phone_live", SessionID: mine.ID, State: "running", StartedAt: now.Add(-5 * time.Minute), Summary: "同步共享日历"},
		{ID: "run_foreign_live", SessionID: foreign.ID, State: "running", StartedAt: now.Add(-5 * time.Minute), Summary: "foreign canary"},
	} {
		if _, err := repo.SaveRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.SaveApproval(ctx, app.Approval{ID: "approval_phone", SessionID: mine.ID, RunID: "run_phone_live", Tool: "send_email", Status: app.ApprovalStatusPending, Summary: "回复陈敏并确认会议"}); err != nil {
		t.Fatal(err)
	}

	snapshot := handler(ctx, phoneReadRequest(iscpworkbench.OperationPhoneSnapshotGet, nil))
	if snapshot.Status != 200 {
		t.Fatalf("snapshot %+v", snapshot)
	}
	var snap struct {
		ProtocolVersion string `json:"protocol_version"`
		Status          string `json:"status"`
		EndpointID      string `json:"endpoint_id"`
		Result          struct {
			Cards []iscpbridge.SnapshotCard `json:"cards"`
		} `json:"result"`
	}
	if err := json.Unmarshal(snapshot.Body, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.ProtocolVersion != iscpbridge.ProtocolVersion || snap.Status != "ok" || snap.EndpointID != "iscp-desktop-client" {
		t.Fatalf("snapshot envelope %+v", snap)
	}
	cards := map[string]iscpbridge.SnapshotCard{}
	for _, card := range snap.Result.Cards {
		cards[card.ID] = card
	}
	if cards["approvals-pending"].Summary != "回复陈敏并确认会议" || cards["runs-running"].Summary != "同步共享日历" {
		t.Fatalf("snapshot cards %+v", snap.Result.Cards)
	}

	activity := handler(ctx, phoneReadRequest(iscpworkbench.OperationPhoneActivityList, []byte(`{"limit":10}`)))
	var list struct {
		Status string `json:"status"`
		Result struct {
			Activities []iscpbridge.ActivityView `json:"activities"`
		} `json:"result"`
	}
	if activity.Status != 200 || json.Unmarshal(activity.Body, &list) != nil || list.Status != "ok" || len(list.Result.Activities) != 2 {
		t.Fatalf("activity %+v", activity)
	}
	for _, item := range list.Result.Activities {
		if item.SessionID != mine.ID {
			t.Fatalf("foreign activity leaked %+v", item)
		}
	}

	bad := handler(ctx, phoneReadRequest(iscpworkbench.OperationPhoneActivityList, []byte(`{"date":"not-a-date"}`)))
	var failed struct {
		Status string                  `json:"status"`
		Error  *iscpbridge.BridgeError `json:"error"`
	}
	if bad.Status != 400 || json.Unmarshal(bad.Body, &failed) != nil || failed.Error == nil || failed.Error.Code != iscpbridge.CodeInvalidRequest {
		t.Fatalf("bad date %+v", bad)
	}
	unknown := handler(ctx, phoneReadRequest(iscpworkbench.OperationPhoneSnapshotGet, []byte(`{"unexpected":true}`)))
	if unknown.Status != 400 {
		t.Fatalf("unknown snapshot field accepted %+v", unknown)
	}

	other := context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{Authenticated: true, OwnerID: "third-owner", ActorID: "third-owner", ClientID: "third-client"})
	empty := handler(other, phoneReadRequest(iscpworkbench.OperationPhoneSnapshotGet, nil))
	var none struct {
		Result struct {
			Cards []iscpbridge.SnapshotCard `json:"cards"`
		} `json:"result"`
	}
	if empty.Status != 200 || json.Unmarshal(empty.Body, &none) != nil || len(none.Result.Cards) != 0 {
		t.Fatalf("unrelated owner saw cards %+v", empty)
	}
	if unauthenticated := handler(t.Context(), phoneReadRequest(iscpworkbench.OperationPhoneSnapshotGet, nil)); unauthenticated.Status != 401 {
		t.Fatalf("unauthenticated %+v", unauthenticated)
	}
}
