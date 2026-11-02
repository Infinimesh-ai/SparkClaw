package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func medicineHTTPFixture(t *testing.T) (*Server, *store.MemoryStore) {
	t.Helper()
	repo := store.NewMemoryStore()
	s := &Server{store: repo, mux: http.NewServeMux()}
	s.registerMedicineRoutes()
	return s, repo
}
func medicineRequest(t *testing.T, s *Server, owner, method, path string, value any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if value != nil {
		var err error
		raw, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	r = r.WithContext(context.WithValue(t.Context(), requestPrincipalContextKey{}, requestPrincipal{OwnerID: owner, ActorID: owner, ClientID: "desktop", Authenticated: true}))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, r)
	return w
}
func medicineFieldsFixture() medicine.Fields {
	return medicine.Fields{Name: "布洛芬", Category: "fever", Quantity: 24, Unit: "capsule", ExpiresOn: "2027-05-31"}
}
func medicineDecode[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	var out T
	if w.Code != status {
		t.Fatalf("HTTP %d: %s; want %d", w.Code, w.Body, status)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func TestMedicineInventoryHTTPCRUDAndBatch(t *testing.T) {
	s, repo := medicineHTTPFixture(t)
	f := medicineFieldsFixture()
	item := medicineDecode[medicine.Item](t, medicineRequest(t, s, "owner", "POST", "/api/medicine/items", map[string]any{"operation_id": "create", "item": f}), 200)
	if item.ID == "" || item.Version != 1 {
		t.Fatal(item)
	}
	body := map[string]any{"operation_id": "update", "base_version": 1, "item": f}
	f.Quantity = 22
	body["item"] = f
	updated := medicineDecode[medicine.Item](t, medicineRequest(t, s, "owner", "PUT", "/api/medicine/items/"+item.ID, body), 200)
	if updated.Version != 2 || updated.Quantity != 22 {
		t.Fatal(updated)
	}
	if w := medicineRequest(t, s, "other", "PUT", "/api/medicine/items/"+item.ID, body); w.Code != 404 {
		t.Fatal("cross-owner", w.Code, w.Body)
	}
	bad := medicine.Batch{OperationID: "batch", Operation: medicine.OperationUpdate, Items: []medicine.Entry{{EntryID: "e1", ID: item.ID, BaseVersion: 1, Fields: f}}}
	if w := medicineRequest(t, s, "owner", "POST", "/api/medicine/items/batch", bad); w.Code != 409 {
		t.Fatal(w.Code, w.Body)
	}
	add := medicine.Batch{OperationID: "batchcreate", Operation: medicine.OperationCreate, Items: []medicine.Entry{{EntryID: "e1", Fields: f}, {EntryID: "e2", Fields: f}}}
	receipt := medicineDecode[medicine.Receipt](t, medicineRequest(t, s, "owner", "POST", "/api/medicine/items/batch", add), 200)
	if len(receipt.Items) != 2 {
		t.Fatal(receipt)
	}
	replay := medicineDecode[medicine.Receipt](t, medicineRequest(t, s, "owner", "POST", "/api/medicine/items/batch", add), 200)
	if !reflect.DeepEqual(receipt, replay) {
		t.Fatal("batch replay diverged")
	}
	before, _ := repo.ReadMedicineBox(t.Context(), "owner")
	operation := medicineDecode[medicine.OperationResult](t, medicineRequest(t, s, "owner", "GET", "/api/medicine/operations/batchcreate", nil), 200)
	after, _ := repo.ReadMedicineBox(t.Context(), "owner")
	if operation.Batch == nil || !reflect.DeepEqual(before, after) {
		t.Fatal("receipt read changed state")
	}
	delta := medicineDecode[medicine.SyncPage](t, medicineRequest(t, s, "owner", "POST", "/api/medicine/sync", map[string]any{"cursor": "", "limit": 100}), 200)
	if len(delta.Events) != 3 || delta.Mode != "snapshot" {
		t.Fatal(delta)
	}
	medicineDecode[map[string]bool](t, medicineRequest(t, s, "owner", "DELETE", "/api/medicine/items/"+item.ID, map[string]any{"operation_id": "delete", "base_version": 2}), 200)
}
func TestMedicineInventoryHTTPMissingFieldsAndOwnerInjection(t *testing.T) {
	s, repo := medicineHTTPFixture(t)
	for _, payload := range []any{
		map[string]any{"operation_id": "missing", "item": map[string]any{"name": "x", "category": "other", "unit": "tablet", "expires_on": "2027-01-31", "no_expiry": false}},
		map[string]any{"operation_id": "null", "item": map[string]any{"name": "x", "category": "other", "quantity": nil, "unit": "tablet", "expires_on": "2027-01-31", "no_expiry": false}},
		map[string]any{"operation_id": "owner", "item": medicineFieldsFixture(), "owner_id": "other"},
		map[string]any{"operation_id": "date", "item": medicine.Fields{Name: "x", Category: "other", Quantity: 1, Unit: "tablet"}},
	} {
		if w := medicineRequest(t, s, "owner", "POST", "/api/medicine/items", payload); w.Code != 400 {
			t.Fatal(w.Code, w.Body)
		}
	}
	batch := medicine.Batch{OperationID: "partbad", Operation: medicine.OperationCreate, Items: []medicine.Entry{{EntryID: "one", Fields: medicineFieldsFixture()}, {EntryID: "two", Fields: medicine.Fields{Name: "missing"}}}}
	if w := medicineRequest(t, s, "owner", "POST", "/api/medicine/items/batch", batch); w.Code != 400 {
		t.Fatal(w.Code, w.Body)
	}
	box, _ := repo.ReadMedicineBox(t.Context(), "owner")
	if len(box.Items) != 0 {
		t.Fatal("invalid request partially wrote")
	}
	item := medicine.Item{ID: "med_0123456789abcdef0123456789abcdef", Fields: medicineFieldsFixture(), CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	item.ExpiresOn = ""
	out := medicineDecode[medicine.ImportResult](t, medicineRequest(t, s, "owner", "POST", "/api/medicine/import", medicine.ImportRequest{OperationID: "old", InstallationID: "desktop", Items: []medicine.Item{item}}), 200)
	if out.Imported != 1 || out.Items[0].ID != item.ID || out.Items[0].ExpiresOn != "" {
		t.Fatal(out)
	}
}
func TestISCPMedicineEncryptedBatchReceiptAndSync(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	ops := []string{wb.OperationMedicineItemsBatch, wb.OperationMedicineSync, wb.OperationMedicineOperationsGet}
	f := startV2EncryptedFixture(t, server, cfg, []string{"medicine.read", "medicine.write"}, ops)
	f.bind(t)
	batch := medicine.Batch{OperationID: "medicine-batch", Operation: medicine.OperationCreate, Items: []medicine.Entry{{EntryID: "one", Fields: medicineFieldsFixture()}, {EntryID: "two", Fields: medicineFieldsFixture()}}}
	raw, _ := json.Marshal(batch)
	request := v2Request(wb.OperationMedicineItemsBatch, raw)
	response := f.call(t, request)
	requireV2Status(t, response, 200)
	var receipt medicine.Receipt
	if json.Unmarshal(response.Body, &receipt) != nil || len(receipt.Items) != 2 {
		t.Fatal(string(response.Body))
	}
	request.ID = v2Request(wb.OperationIdentity, nil).ID
	replayed := f.call(t, request)
	requireV2Status(t, replayed, 200)
	if !bytes.Equal(response.Body, replayed.Body) {
		t.Fatal("transport receipt differs")
	}
	lookup := v2Request(wb.OperationMedicineOperationsGet, nil)
	lookup.Params = map[string]string{"operation": "medicine-batch"}
	result := f.call(t, lookup)
	requireV2Status(t, result, 200)
	var operation medicine.OperationResult
	if json.Unmarshal(result.Body, &operation) != nil || operation.Batch == nil || !reflect.DeepEqual(*operation.Batch, receipt) {
		t.Fatal(string(result.Body))
	}
	sync := f.jsonCall(t, wb.OperationMedicineSync, map[string]any{"cursor": "", "limit": 100})
	requireV2Status(t, sync, 200)
	var page medicine.SyncPage
	if json.Unmarshal(sync.Body, &page) != nil || len(page.Events) != 2 {
		t.Fatal(string(sync.Body))
	}
	box, err := repo.ReadMedicineBox(t.Context(), "iscp-owner")
	if err != nil || len(box.Items) != 2 || box.Sequence != 2 {
		t.Fatal(box, err)
	}
	other, err := repo.ReadMedicineBox(t.Context(), "other")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("owner isolation", err)
	}
}
