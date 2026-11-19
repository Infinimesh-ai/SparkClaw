package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestWorkbenchISCPV2LargeTypedRequestAndReceiptUseObjects(t *testing.T) {
	f := newV2EncryptedFixture(t, nil)
	f.bind(t)
	read := f.call(t, v2Request(wb.OperationSettingsOwnerGet, nil))
	requireV2Status(t, read, 200)
	var before struct {
		Revision string `json:"revision"`
	}
	if json.Unmarshal(read.Body, &before) != nil || before.Revision == "" {
		t.Fatal("missing revision")
	}
	preferences := map[string]string{}
	for n := 0; n < 50; n++ {
		preferences[fmt.Sprintf("p%02d", n)] = strings.Repeat("界", 500)
	}
	raw, err := json.Marshal(map[string]any{"display_name": "large typed fixture", "preferences": preferences})
	if err != nil || len(raw) <= wb.MaxBodyBytes {
		t.Fatal("fixture is not a large valid typed body")
	}
	ref := f.upload(t, "request_body", "owner.json", "application/json", raw)
	change := v2Request(wb.OperationSettingsOwnerPatch, nil)
	change.Object, change.ExpectedRevision = &ref, before.Revision
	changed := f.call(t, change)
	requireV2Status(t, changed, 200)
	if changed.Object == nil {
		t.Fatal("large typed result omitted object")
	}
	received := f.download(t, *changed.Object)
	var updated struct {
		Revision string           `json:"revision"`
		Value    app.OwnerProfile `json:"value"`
	}
	if json.Unmarshal(received, &updated) != nil || updated.Revision == before.Revision || len(updated.Value.Preferences) != 50 || updated.Value.DisplayName != "large typed fixture" {
		t.Fatal("large typed body was not applied")
	}
	for k, v := range preferences {
		if updated.Value.Preferences[k] != v {
			t.Fatalf("preference %s bytes changed", k)
		}
	}
	persisted, found, err := f.server.store.GetOwnerProfileByID(t.Context(), "iscp-owner")
	if err != nil || !found || !persisted.UpdatedAt.Equal(updated.Value.UpdatedAt) {
		t.Fatal("typed result did not reflect persisted owner profile")
	}
	change.ID = v2Request(wb.OperationIdentity, nil).ID
	replay := f.call(t, change)
	requireV2Status(t, replay, 200)
	if replay.Object == nil || *replay.Object != *changed.Object || !bytes.Equal(f.download(t, *replay.Object), received) {
		t.Fatal("large mutation replay changed its original receipt")
	}
	persistedAfter, found, err := f.server.store.GetOwnerProfileByID(t.Context(), "iscp-owner")
	if err != nil || !found || !persistedAfter.UpdatedAt.Equal(persisted.UpdatedAt) {
		t.Fatal("operation replay repeated the business write")
	}
	receipt := v2Request(wb.OperationOperationsReceipt, nil)
	receipt.Params = map[string]string{"operation_id": change.OperationID}
	receiptResponse := f.call(t, receipt)
	requireV2Status(t, receiptResponse, 200)
	if receiptResponse.Object == nil {
		t.Fatal("large durable receipt lookup omitted object")
	}
	if !json.Valid(f.download(t, *receiptResponse.Object)) {
		t.Fatal("durable receipt lost its JSON result")
	}
}
