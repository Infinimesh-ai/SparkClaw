package gateway

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchV2ResultPreservesOnlyTypedCursorGap(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   wb.ErrorCode
		want   wb.ErrorCode
	}{
		{"cursor_gap", 409, `{"code":"cursor_gap","retryable":false}`, "", wb.ErrorCursorGap},
		{"explicit_code_wins", 409, `{"code":"cursor_gap","retryable":false}`, wb.ErrorRevisionConflict, wb.ErrorRevisionConflict},
		{"other_status", 403, `{"code":"cursor_gap","retryable":false}`, "", wb.ErrorPermissionDenied},
		{"unknown_conflict", 409, `{"code":"other_conflict","retryable":false}`, "", wb.ErrorRevisionConflict},
		{"retryable", 409, `{"code":"cursor_gap","retryable":true}`, "", wb.ErrorRevisionConflict},
		{"missing_retryable", 409, `{"code":"cursor_gap"}`, "", wb.ErrorRevisionConflict},
		{"null_retryable", 409, `{"code":"cursor_gap","retryable":null}`, "", wb.ErrorRevisionConflict},
		{"extra_field", 409, `{"code":"cursor_gap","retryable":false,"other":true}`, "", wb.ErrorRevisionConflict},
		{"trailing_data", 409, `{"code":"cursor_gap","retryable":false}{}`, "", wb.ErrorRevisionConflict},
		{"invalid_code", 409, `{"code":"cursor_gap\n","retryable":false}`, "", wb.ErrorRevisionConflict},
		{"existing_unknown_outcome", 409, `{"code":"operation_outcome_unknown","retryable":false}`, "", wb.ErrorOutcomeUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := workbenchV2Result(wb.Response{Status: tc.status, Body: json.RawMessage(tc.body), Code: tc.code})
			if result.Code != tc.want || result.Retryable || string(result.Body) != tc.body {
				t.Fatalf("result=%+v want code=%s with unchanged body", result, tc.want)
			}
		})
	}
}

func TestWorkbenchISCPV2EncryptedCursorGapMatchesDesktopRecoveryEnvelope(t *testing.T) {
	server, _, config, textHandler := workbenchISCPFixture(t, nil)
	bindWorkbenchISCP(t, textHandler)
	journal, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &iscpDomainAdapter{server: server, config: config, receipts: journal}
	request := domainTestRequest(wb.OperationEventsSnapshot, []byte(`{"request_ids":[],"categories":["tasks"]}`))
	old := adapter.eventsWithSession(domainTestContext(t), request, wb.SessionInfo{GrantRevision: 1, Scopes: []string{"workbench.text"}})
	var previous struct {
		Cursor string `json:"cursor"`
		Epoch  string `json:"epoch"`
	}
	if old.status != 200 || json.Unmarshal(old.body, &previous) != nil || previous.Cursor == "" {
		t.Fatalf("previous Gateway snapshot: %+v", old)
	}
	// Advance the lifecycle epoch before starting any Endpoint goroutines. The
	// new production handler reopens the same encrypted durable event journal.
	server.started = server.started.Add(time.Second)
	f := startV2EncryptedFixture(t, server, config, []string{"events.read"}, []string{wb.OperationEventsSnapshot, wb.OperationEventsPull, wb.OperationEventsAck})
	if negotiated, ok := f.client.Negotiated(); !ok || negotiated.AuthorizationRevision != 1 {
		t.Fatal("fixture must preserve the cursor's original authorization revision")
	}
	f.bind(t)
	gap := f.jsonCall(t, wb.OperationEventsPull, map[string]any{"cursor": previous.Cursor, "limit": 100})
	requireV2Status(t, gap, 409)
	if gap.Code != wb.ErrorCursorGap || gap.Retryable || gap.Validate() != nil {
		t.Fatalf("Gateway normalization or encrypted Endpoint lost cursor_gap: %+v", gap)
	}

	// Desktop consumes this same helper envelope shape. Sharing the fixture
	// prevents its integration test from silently omitting the real error_code.
	wantRaw, err := os.ReadFile("testdata/iscp-cursor-gap-response.json")
	if err != nil {
		t.Fatal(err)
	}
	actualRaw, err := json.Marshal(gap)
	if err != nil {
		t.Fatal(err)
	}
	var actual, want map[string]any
	if json.Unmarshal(actualRaw, &actual) != nil || json.Unmarshal(wantRaw, &want) != nil {
		t.Fatal("invalid shared recovery fixture")
	}
	delete(actual, "id")
	delete(actual, "type")
	delete(actual, "profile")
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("encrypted envelope=%s Desktop fixture=%s", actualRaw, wantRaw)
	}

	reset := f.jsonCall(t, wb.OperationEventsSnapshot, map[string]any{"request_ids": []string{}, "categories": []string{"tasks"}})
	requireV2Status(t, reset, 200)
	var next struct {
		Cursor   string `json:"cursor"`
		Epoch    string `json:"epoch"`
		Revision uint64 `json:"revision"`
		Reset    bool   `json:"reset"`
	}
	if json.Unmarshal(reset.Body, &next) != nil || next.Cursor == previous.Cursor || next.Epoch == previous.Epoch || next.Revision != 1 || !next.Reset {
		t.Fatalf("missing authoritative new-epoch snapshot: %s", reset.Body)
	}
	requireV2Status(t, f.jsonCall(t, wb.OperationEventsAck, map[string]string{"cursor": next.Cursor}), 200)
	requireV2Status(t, f.jsonCall(t, wb.OperationEventsPull, map[string]string{"cursor": next.Cursor}), 200)
	// Replacing a subscription is another legitimate cursor gap; ACKing an old
	// generation must never acknowledge or erase the replacement projection.
	requireV2Status(t, f.jsonCall(t, wb.OperationEventsSnapshot, map[string]any{"request_ids": []string{}, "categories": []string{"tasks"}}), 200)
	stale := f.jsonCall(t, wb.OperationEventsPull, map[string]string{"cursor": next.Cursor})
	if stale.Status != 409 || stale.Code != wb.ErrorCursorGap {
		t.Fatalf("replaced cursor error lost: %+v", stale)
	}
	staleAck := f.jsonCall(t, wb.OperationEventsAck, map[string]string{"cursor": next.Cursor})
	if staleAck.Status != 409 || staleAck.Code != wb.ErrorCursorGap {
		t.Fatalf("stale ACK crossed generation: %+v", staleAck)
	}
}
