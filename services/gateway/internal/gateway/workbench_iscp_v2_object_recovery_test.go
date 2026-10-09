package gateway

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpobjects"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Infinimesh-ai/ISCP/pkg/iscp/trust"
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

func TestWorkbenchISCPV2PartialObjectSurvivesSignedGrantRenewal(t *testing.T) {
	server, _, local, _ := workbenchISCPFixture(t, nil)
	f := startV2EncryptedFixture(t, server, local, nil, nil, 10*time.Second)
	f.bind(t)
	initial, ok := f.client.Negotiated()
	if !ok {
		t.Fatal("missing initial capabilities")
	}
	raw := bytes.Repeat([]byte("renewed-original-bytes"), 1000)
	id := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	open := iscpobjects.OpenRequest{TransferID: id, Purpose: "execution_input", Name: "renewal.bin", MediaType: "application/octet-stream", Size: int64(len(raw)), SHA256: execution.Digest(raw)}
	requireV2Status(t, f.jsonCall(t, wb.OperationTransferOpen, open), 200)
	send := func(offset int) {
		t.Helper()
		chunk := raw[offset:min(offset+iscpobjects.ChunkBytes, len(raw))]
		requireV2Status(t, f.jsonCall(t, wb.OperationTransferChunk, iscpobjects.ChunkRequest{TransferID: id, Index: offset / iscpobjects.ChunkBytes, Offset: int64(offset), SHA256: execution.Digest(chunk), DataBase64: base64.StdEncoding.EncodeToString(chunk)}), 200)
	}
	send(0)
	// Let the original genuinely signed seed expire while the authenticated
	// lifecycle performs its normal HTTP proof-bound renewal on both peers.
	if delay := time.Until(f.seed.ExpiresAt.Add(300 * time.Millisecond)); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		<-timer.C
	}
	for _, cfg := range f.configs {
		encoded, err := os.ReadFile(cfg.GrantFile)
		var renewed trust.Grant
		if err != nil || json.Unmarshal(encoded, &renewed) != nil || renewed.GrantID == f.seed.GrantID || renewed.Signature.Value == f.seed.Signature.Value || !renewed.ExpiresAt.After(f.seed.ExpiresAt) || renewed.RevocationEpoch != f.seed.RevocationEpoch {
			t.Fatal("grant was not really renewed with the same authorization revision")
		}
	}
	current, ok := f.client.Negotiated()
	if !ok || current.SessionID != initial.SessionID || current.AuthorizationRevision != initial.AuthorizationRevision {
		t.Fatal("silent renewal discarded the active object binding")
	}
	status := f.jsonCall(t, wb.OperationTransferStatus, map[string]string{"transfer_id": id})
	requireV2Status(t, status, 200)
	var checkpoint iscpobjects.Checkpoint
	if json.Unmarshal(status.Body, &checkpoint) != nil || checkpoint.AcknowledgedBytes != iscpobjects.ChunkBytes || checkpoint.State != "uploading" {
		t.Fatalf("renewal lost acknowledged chunks: %s", status.Body)
	}
	send(0) // Same chunk is an idempotent receipt after the new Grant.
	for offset := iscpobjects.ChunkBytes; offset < len(raw); offset += iscpobjects.ChunkBytes {
		send(offset)
	}
	committed := f.jsonCall(t, wb.OperationTransferCommit, map[string]string{"transfer_id": id})
	requireV2Status(t, committed, 200)
	if json.Unmarshal(committed.Body, &checkpoint) != nil || checkpoint.State != "committed" || !bytes.Equal(f.download(t, checkpoint.Object), raw) {
		t.Fatal("resumed transfer changed original bytes")
	}
}
