package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
	"github.com/Chiiz0/SparkClaw/services/link/linkclient"
)

// linkDomainTestDevice connects one desktop to a link test backend.
func linkDomainTestDevice(t *testing.T) (*linkTestBackend, *linkclient.Client, context.Context) {
	t.Helper()
	b := newLinkTestBackend(t)
	desk, _, rec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge, Attachments: true, Microphone: true, BrowserSurface: true})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	desk.Start(ctx)
	t.Cleanup(desk.Close)
	if desk.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	return b, desk, ctx
}

// linkDomainCall calls one typed domain method.
func linkDomainCall(ctx context.Context, c *linkclient.Client, method string, args any, key string, out any) error {
	raw, _ := json.Marshal(args)
	var result contract.DomainCallResult
	domain := method[:bytes.IndexByte([]byte(method), '.')]
	if err := c.Request(ctx, contract.OpDomainCall, contract.DomainCallParams{Domain: domain, Method: method, Args: raw, IdempotencyKey: key}, &result); err != nil {
		return err
	}
	if out != nil {
		return json.Unmarshal(result.Result, out)
	}
	return nil
}

func TestLinkMedicineBatchReceiptLookupAndSync(t *testing.T) {
	b, desk, ctx := linkDomainTestDevice(t)
	batch := map[string]any{"operation_id": "medicine-batch", "operation": medicine.OperationCreate, "items": []map[string]any{
		{"entry_id": "one", "fields": medicineFieldsFixture()}, {"entry_id": "two", "fields": medicineFieldsFixture()}}}
	key := newLinkUUID()
	var receipt medicine.Receipt
	if err := linkDomainCall(ctx, desk, "medicine.items.batch", batch, key, &receipt); err != nil || len(receipt.Items) != 2 {
		t.Fatalf("batch %+v %v", receipt, err)
	}
	// The same idempotency key returns the original receipt.
	var replayed medicine.Receipt
	if err := linkDomainCall(ctx, desk, "medicine.items.batch", batch, key, &replayed); err != nil || !reflect.DeepEqual(receipt, replayed) {
		t.Fatalf("replay %+v %v", replayed, err)
	}
	var operation medicine.OperationResult
	if err := linkDomainCall(ctx, desk, "medicine.operations.get", map[string]string{"operation_id": "medicine-batch"}, "", &operation); err != nil || operation.Batch == nil || !reflect.DeepEqual(*operation.Batch, receipt) {
		t.Fatalf("operation %+v %v", operation, err)
	}
	var page medicine.SyncPage
	if err := linkDomainCall(ctx, desk, "medicine.sync", map[string]any{"cursor": "", "limit": 100}, "", &page); err != nil || len(page.Events) != 2 {
		t.Fatalf("sync %+v %v", page, err)
	}
	box, err := b.server.store.ReadMedicineBox(ctx, app.DefaultOwnerID)
	if err != nil || len(box.Items) != 2 || box.Sequence != 2 {
		t.Fatal(box, err)
	}
	if other, err := b.server.store.ReadMedicineBox(ctx, "other"); err != nil || len(other.Items) != 0 {
		t.Fatal("owner isolation", err)
	}
}

func TestLinkMedicineFailuresCarryTheirReason(t *testing.T) {
	_, desk, ctx := linkDomainTestDevice(t)
	err := linkDomainCall(ctx, desk, "medicine.items.create", map[string]any{"operation_id": "bad", "item": map[string]any{"name": "missing fields"}}, newLinkUUID(), nil)
	if cerr := contract.AsError(err); cerr.Code != contract.CodeInvalidRequest || cerr.Reason != "medicine_invalid" {
		t.Fatalf("invalid item: %+v", cerr)
	}
	err = linkDomainCall(ctx, desk, "medicine.operations.get", map[string]string{"operation_id": "missing"}, "", nil)
	if cerr := contract.AsError(err); cerr.Code != contract.CodeNotFound || cerr.Reason != "medicine_not_found" {
		t.Fatalf("missing operation: %+v", cerr)
	}
	// A mutation needs its idempotency key; a read refuses replay_only.
	if err := linkDomainCall(ctx, desk, "medicine.items.create", map[string]any{"operation_id": "x"}, "", nil); contract.AsError(err).Code != contract.CodeInvalidRequest {
		t.Fatal(err)
	}
	if err := linkDomainCall(ctx, desk, "medicine.parse", map[string]string{"text": "", "today": "2026-11-20"}, "", nil); contract.AsError(err).Reason != "medicine_text_invalid" {
		t.Fatal(err)
	}
}
