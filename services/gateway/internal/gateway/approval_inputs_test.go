package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func gatewayCompletionForm() *app.ApprovalInputForm {
	return &app.ApprovalInputForm{SchemaVersion: 1, Entries: []app.ApprovalInputEntry{{ID: "entry_1", Label: "Medicine", Summary: "Quantity 6", Fields: []app.ApprovalInputField{{Name: "expiry", Label: "Expiry", Type: app.ApprovalInputDateOrNone, Required: true}}}}}
}
func TestISCPApprovalCompletionCarriesInputsAndDeduplicates(t *testing.T) {
	var fixture *v2EncryptedFixture
	var effects atomic.Int32
	pending, err := execution.NewPendingApprovalWithForm("approval_fields", "notify.ask_approval", "Complete batch", map[string]any{"batch": "frozen"}, gatewayCompletionForm())
	if err != nil {
		t.Fatal(err)
	}
	fixture = newV2EncryptedFixture(t, func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		service, err := fixture.server.executionService()
		if err != nil {
			return execution.Output{}, err
		}
		decision, values, err := service.AwaitApprovalWithInputs(ctx, e, pending)
		if err != nil {
			return execution.Output{}, err
		}
		if decision != "approve" || values["entry_1"]["expiry"] != "2027-05-31" {
			return execution.Output{}, fmt.Errorf("wrong completion")
		}
		effects.Add(1)
		return execution.Output{Content: "batch completed"}, nil
	})
	fixture.bind(t)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	requireV2Status(t, fixture.call(t, v2Request(wb.OperationSubmit, raw)), 202)
	var state execution.Status
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		res := fixture.call(t, v2Request(wb.OperationLookup, nil))
		requireV2Status(t, res, 200)
		if json.Unmarshal(res.Body, &state) != nil {
			t.Fatal("invalid lookup")
		}
		if len(state.PendingApprovals) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if len(state.PendingApprovals) != 1 || state.PendingApprovals[0].InputForm == nil {
		t.Fatal("form missing")
	}
	submit := func(input map[string]any) wb.Request {
		body, _ := json.Marshal(input)
		r := v2Request(wb.OperationExecutionApproval, body)
		r.Params = map[string]string{"request_id": iscpTestRequest, "approval_id": pending.ApprovalID}
		r.ExpectedRevision = fmt.Sprint(state.Revision)
		return r
	}
	input := map[string]any{"digest": pending.Digest, "decision": "approve", "input_digest": state.InputDigest}
	requireV2Status(t, fixture.call(t, submit(input)), 409)
	input["input_schema_version"] = 1
	input["inputs"] = app.ApprovalInputValues{"entry_1": {"expiry": "2027-02-31"}}
	requireV2Status(t, fixture.call(t, submit(input)), 409)
	input["inputs"] = app.ApprovalInputValues{"entry_1": {"expiry": "2027-05-31"}}
	request := submit(input)
	first := fixture.call(t, request)
	requireV2Status(t, first, 200)
	fixture.server.executions.Wait()
	request.ID = v2Request(wb.OperationIdentity, nil).ID
	replay := fixture.call(t, request)
	requireV2Status(t, replay, 200)
	if effects.Load() != 1 || !bytes.Equal(first.Body, replay.Body) || !bytes.Contains(first.Body, []byte("inputs_digest")) {
		t.Fatalf("completion not exactly once: %d %s", effects.Load(), replay.Body)
	}
}
func TestLANApprovalCompletionUsesSameTypedInputContract(t *testing.T) {
	var server *Server
	var effects atomic.Int32
	pending, _ := execution.NewPendingApprovalWithForm("approval_lan_fields", "notify.ask_approval", "Complete fields", nil, gatewayCompletionForm())
	created, _, _, textHandler := workbenchISCPFixture(t, func(ctx context.Context, e execution.Envelope, _ map[string][]byte) (execution.Output, error) {
		decision, values, err := server.executions.AwaitApprovalWithInputs(ctx, e, pending)
		if err == nil && decision == "approve" && values["entry_1"]["expiry"] == "no_expiry" {
			effects.Add(1)
		}
		return execution.Output{Content: "completed"}, err
	})
	server = created
	bindWorkbenchISCP(t, textHandler)
	raw, _ := json.Marshal(workbenchISCPEnvelope())
	if got := textHandler(t.Context(), workbenchISCPRequest(wb.OperationSubmit, raw)); got.Status != 202 {
		t.Fatal(got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, _ := server.executions.Lookup("iscp-owner", "iscp-desktop-client", iscpTestRequest)
		if len(s.PendingApprovals) > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	request := func(body map[string]any) int {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/api/v1/executions/"+iscpTestRequest+"/approvals/"+pending.ApprovalID, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer synthetic-issued-client-credential-never-used-by-iscp")
		r.Header.Set("X-SparkClaw-Installation", iscpTestInstallation)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-SparkClaw-Digest", execution.Digest(raw))
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, r)
		return w.Code
	}
	body := map[string]any{"digest": pending.Digest, "decision": "approve"}
	if code := request(body); code != 409 {
		t.Fatal("approve-only input", code)
	}
	body["input_schema_version"] = 1
	body["inputs"] = app.ApprovalInputValues{"entry_1": {"expiry": "no_expiry"}}
	if code := request(body); code != 200 {
		t.Fatal("typed completion", code)
	}
	server.executions.Wait()
	if effects.Load() != 1 {
		t.Fatal("completion lost", effects.Load())
	}
}
