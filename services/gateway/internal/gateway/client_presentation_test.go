package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchISCPLocalSchedulePresentationAndReceipt(t *testing.T) {
	server, repository, _, handler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, handler)
	e := workbenchISCPEnvelope()
	e.Messages = []execution.Message{{ID: "fixture-message-19-1", Role: "user", Content: "显示明天的日程"}}
	e.ClientPresentation = &app.ClientPresentation{SchemaVersion: 1, ScheduleShow: true, Timezone: "Asia/Shanghai", RequestedAt: time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC)}
	raw, _ := json.Marshal(e)
	submit := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationSubmit, raw))
	if submit.Status != http.StatusAccepted {
		t.Fatalf("submit %+v", submit)
	}
	server.executions.Wait()
	lookup := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	var status execution.Status
	if err := json.Unmarshal(lookup.Body, &status); err != nil || status.Result == nil || status.State != "completed" {
		t.Fatalf("lookup %+v status %+v err %v", lookup, status, err)
	}
	var answer execution.Payload
	if err := json.Unmarshal([]byte(status.Result.Payload), &answer); err != nil || answer.ClientAction == nil {
		t.Fatalf("result %+v %v", answer, err)
	}
	action := answer.ClientAction
	if action.RequestID != e.RequestID || action.ConversationID != e.ConversationID || action.DateStart != "2026-10-31" || action.DateEnd != "2026-11-01" {
		t.Fatalf("action %+v", action)
	}
	if len(answer.Files) != 0 || !bytes.Contains([]byte(answer.Content), []byte("已请求")) {
		t.Fatalf("not a presentation handoff: %+v", answer)
	}
	// The production adapter uses its temporary execution store. This owner-wide
	// host store must not receive desktop chat or presentation content.
	sessions, err := repository.ListSessions(t.Context())
	if err != nil || len(sessions) != 0 {
		t.Fatalf("desktop content leaked to host store: %+v %v", sessions, err)
	}
	original := map[string]any{"sequence": status.Result.Sequence, "digest": status.Result.Digest, "durable": true}
	ack, _ := json.Marshal(original)
	if got := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationAck, ack)); got.Status != 200 {
		t.Fatalf("delivery ack %+v", got)
	}
	lookup = handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	if bytes.Contains(lookup.Body, []byte("client_action_receipt")) {
		t.Fatal("delivery implied UI success")
	}
	original["client_action_receipt"] = app.ClientActionReceipt{ActionID: e.RequestID, Status: "shown"}
	ack, _ = json.Marshal(original)
	for range 2 {
		if got := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationAck, ack)); got.Status != 200 {
			t.Fatalf("presentation ack/replay %+v", got)
		}
	}
	lookup = handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationLookup, nil))
	if !bytes.Contains(lookup.Body, []byte(`"status":"shown"`)) || bytes.Contains(lookup.Body, []byte(`"result":`)) {
		t.Fatalf("presentation receipt %+v", lookup)
	}
	original["client_action_receipt"] = app.ClientActionReceipt{ActionID: e.RequestID, Status: "failed"}
	ack, _ = json.Marshal(original)
	if got := handler(t.Context(), workbenchISCPRequest(iscpworkbench.OperationAck, ack)); got.Status != 409 {
		t.Fatalf("conflicting presentation accepted %+v", got)
	}
}
