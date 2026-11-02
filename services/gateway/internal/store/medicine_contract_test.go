package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
)

func medicineTestBatch(id string, n int) medicine.Batch {
	b := medicine.Batch{OperationID: id, Operation: medicine.OperationCreate}
	for i := 0; i < n; i++ {
		b.Items = append(b.Items, medicine.Entry{EntryID: fmt.Sprint("entry", i), Fields: medicine.Fields{Name: fmt.Sprint("药品", i), Category: "other", Quantity: 6, Unit: "tablet", ExpiresOn: "2027-05-31"}})
	}
	return b
}
func medicineContract(t *testing.T, repo medicine.Repository) {
	t.Helper()
	ctx := t.Context()
	owner := "medicine-owner"
	initial, err := repo.ReadMedicineBox(ctx, owner)
	if err != nil || len(initial.Items) != 0 {
		t.Fatal(initial, err)
	}
	batch := medicineTestBatch("first", 2)
	receipt, err := repo.ApplyMedicineBatch(ctx, owner, batch)
	if err != nil || len(receipt.Items) != 2 || receipt.Sequence != 2 {
		t.Fatal(receipt, err)
	}
	original := cloneMedicineValue(receipt)
	receipt.Items[0].After.Name = "caller mutation"
	found, ok, err := repo.GetMedicineOperation(ctx, owner, "first")
	if err != nil || !ok || !reflect.DeepEqual(*found.Batch, original) {
		t.Fatalf("receipt clone or read: %+v %v %v", found, ok, err)
	}
	repeated, err := repo.ApplyMedicineBatch(ctx, owner, batch)
	if err != nil || !reflect.DeepEqual(repeated, original) {
		t.Fatal("non-idempotent replay", repeated, err)
	}
	if _, ok, err := repo.GetMedicineOperation(ctx, "other", "first"); err != nil || ok {
		t.Fatal("cross-owner receipt", err)
	}
	other, err := repo.ReadMedicineBox(ctx, "other")
	if err != nil || len(other.Items) != 0 {
		t.Fatal("cross-owner rows", err)
	}
	changed := medicineTestBatch("first", 2)
	changed.Items[0].Fields.Quantity = 10
	if _, err = repo.ApplyMedicineBatch(ctx, owner, changed); !errors.Is(err, medicine.ErrConflict) {
		t.Fatal("changed replay", err)
	}
	update := medicine.Batch{OperationID: "change", Operation: medicine.OperationUpdate}
	for i, c := range original.Items {
		item := *c.After
		f := item.Fields
		f.Quantity = 4
		update.Items = append(update.Items, medicine.Entry{EntryID: fmt.Sprint("entry", i), ID: item.ID, BaseVersion: item.Version, Fields: f})
	}
	update.Items[1].BaseVersion = 99
	if _, err = repo.ApplyMedicineBatch(ctx, owner, update); !errors.Is(err, medicine.ErrConflict) {
		t.Fatal(err)
	}
	box, err := repo.ReadMedicineBox(ctx, owner)
	if err != nil || box.Sequence != 2 || len(box.Events) != 2 {
		t.Fatal(box, err)
	}
	for _, row := range box.Items {
		if row.Quantity != 6 || row.Version != 1 {
			t.Fatal("partial failed update")
		}
	}
	update.Items[1].BaseVersion = 1
	after, err := repo.ApplyMedicineBatch(ctx, owner, update)
	if err != nil || after.Sequence != 4 {
		t.Fatal(after, err)
	}
	deleted, err := repo.ApplyMedicineBatch(ctx, owner, medicine.Batch{OperationID: "delete", Operation: medicine.OperationDelete, Items: []medicine.Entry{{EntryID: "one", ID: after.Items[0].After.ID, BaseVersion: 2}}})
	if err != nil || !deleted.Items[0].Deleted || deleted.Items[0].Before.Quantity != 4 {
		t.Fatal(deleted, err)
	}
	old := *after.Items[1].After
	old.Quantity = 999
	legacy := medicine.Item{ID: "med_0123456789abcdef0123456789abcdef", Fields: medicine.Fields{Name: "legacy", Category: "other", Quantity: 1, Unit: "box"}, CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	req := medicine.ImportRequest{OperationID: "import", InstallationID: "desktop", Items: []medicine.Item{old, legacy}}
	imported, err := repo.ImportMedicines(ctx, owner, req)
	if err != nil || imported.Imported != 1 || imported.Skipped != 1 || imported.Items[0].Quantity != 4 || imported.Items[1].Version != 1 {
		t.Fatal(imported, err)
	}
	again, err := repo.ImportMedicines(ctx, owner, req)
	if err != nil || !reflect.DeepEqual(again, imported) {
		t.Fatal("import replay", again, err)
	}
	operation, ok, err := repo.GetMedicineOperation(ctx, owner, "import")
	if err != nil || !ok || !reflect.DeepEqual(*operation.Import, imported) {
		t.Fatal(operation, ok, err)
	}
	page, err := medicine.New(repo).Sync(ctx, owner, "", 1)
	if err != nil || !page.More || len(page.Events) != 1 {
		t.Fatal(page, err)
	}
	for page.More {
		page, err = medicine.New(repo).Sync(ctx, owner, page.Cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
	}
	delta, err := medicine.New(repo).Sync(ctx, owner, page.Cursor, 1)
	if err != nil || len(delta.Events) != 0 || delta.Mode != "delta" {
		t.Fatal(delta, err)
	}
}
func TestMedicineMemoryContract(t *testing.T) { medicineContract(t, NewMemoryStore()) }
func TestMedicineFileContractAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	repo, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	medicineContract(t, repo)
	before, err := repo.ReadMedicineBox(t.Context(), "medicine-owner")
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	after, err := reopened.ReadMedicineBox(t.Context(), "medicine-owner")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("restart lost state", err)
	}
	receipt, found, err := reopened.GetMedicineOperation(t.Context(), "medicine-owner", "first")
	if err != nil || !found || len(receipt.Batch.Items) != 2 {
		t.Fatal(receipt, err)
	}
}
func TestMedicinePostgresContract(t *testing.T) {
	dsn := newPostgresMigrationTestSchema(t)
	repo, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	medicineContract(t, repo)
	reopened, err := NewPostgresStore(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	result, found, err := reopened.GetMedicineOperation(t.Context(), "medicine-owner", "first")
	if err != nil || !found || len(result.Batch.Items) != 2 {
		t.Fatal(result, found, err)
	}
}
func TestMedicineConcurrentBatchCAS(t *testing.T) {
	t.Run("memory", func(t *testing.T) { medicineConcurrentBatchCAS(t, NewMemoryStore()) })
	t.Run("file", func(t *testing.T) {
		repo, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
		if err != nil {
			t.Fatal(err)
		}
		medicineConcurrentBatchCAS(t, repo)
	})
	t.Run("postgres", func(t *testing.T) {
		repo, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
		if err != nil {
			t.Fatal(err)
		}
		defer repo.Close()
		medicineConcurrentBatchCAS(t, repo)
	})
}
func medicineConcurrentBatchCAS(t *testing.T, repo medicine.Repository) {
	t.Helper()
	created, err := repo.ApplyMedicineBatch(t.Context(), "owner", medicineTestBatch("create", 1))
	if err != nil {
		t.Fatal(err)
	}
	item := *created.Items[0].After
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{"left", "right"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			f := item.Fields
			f.Quantity = 5
			_, e := repo.ApplyMedicineBatch(context.Background(), "owner", medicine.Batch{OperationID: id, Operation: medicine.OperationUpdate, Items: []medicine.Entry{{EntryID: "e", ID: item.ID, BaseVersion: 1, Fields: f}}})
			results <- e
		}(id)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, medicine.ErrConflict) {
			conflict++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatal(success, conflict)
	}
}
func TestMedicineFileAtomicPersistenceFaults(t *testing.T) {
	for _, stage := range []string{"encode", "write", "file_sync", "rename", "dir_sync"} {
		t.Run(stage, func(t *testing.T) {
			repo, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.ApplyMedicineBatch(t.Context(), "owner", medicineTestBatch("before", 1)); err != nil {
				t.Fatal(err)
			}
			repo.commitOps = &controlledFileCommitOps{failStage: stage, failRemaining: 1, renameApplied: stage == "rename"}
			_, err = repo.ApplyMedicineBatch(t.Context(), "owner", medicineTestBatch("after", 2))
			if err == nil {
				t.Fatal("fault not observed")
			}
			submitted := stage == "rename" || stage == "dir_sync"
			if submitted && StoreErrorCodeOf(err) != StoreErrorUnknownOutcome {
				t.Fatal(err)
			}
			if !submitted && StoreErrorCodeOf(err) != StoreErrorDurability {
				t.Fatal(err)
			}
			result, found, err := repo.GetMedicineOperation(t.Context(), "owner", "after")
			if err != nil || found != submitted {
				t.Fatal(result, found, err)
			}
			box, err := repo.ReadMedicineBox(t.Context(), "owner")
			expected := 1
			if submitted {
				expected = 3
			}
			if err != nil || len(box.Items) != expected || len(box.Events) != expected {
				t.Fatal("partial batch", box, err)
			}
			reopened, err := NewFileStore(repo.path)
			if err != nil {
				t.Fatal(err)
			}
			persisted, err := reopened.ReadMedicineBox(t.Context(), "owner")
			if err != nil || !reflect.DeepEqual(box, persisted) {
				t.Fatal("restart partial", err)
			}
		})
	}
}

var _ medicine.Repository = (*MemoryStore)(nil)
var _ medicine.Repository = (*FileStore)(nil)
var _ medicine.Repository = (*PostgresStore)(nil)

func TestMedicineFileCorruptInventoryRefused(t *testing.T) {
	repo, err := NewFileStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ApplyMedicineBatch(t.Context(), "owner", medicineTestBatch("create", 1)); err != nil {
		t.Fatal(err)
	}
	repo.inner.mu.Lock()
	state := repo.inner.medicineBoxes["owner"]
	state.Items[0].Version = 0
	repo.inner.medicineBoxes["owner"] = state
	repo.inner.mu.Unlock()
	if err = repo.persistSnapshot(); err != nil {
		t.Fatal(err)
	}
	if _, err = NewFileStore(repo.path); err == nil {
		t.Fatal("corrupt medicine inventory was silently accepted")
	}
}

func TestMedicinePostgresReceiptFailureRollsBackWholeBatch(t *testing.T) {
	repo, err := NewPostgresStore(t.Context(), newPostgresMigrationTestSchema(t))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if _, err := repo.ApplyMedicineBatch(t.Context(), "owner", medicineTestBatch("before", 1)); err != nil {
		t.Fatal(err)
	}
	before, err := repo.ReadMedicineBox(t.Context(), "owner")
	if err != nil {
		t.Fatal(err)
	}
	var eventsBefore int
	if err := repo.db.QueryRow(t.Context(), `SELECT count(*) FROM events`).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	// This trigger runs after inventory rows, per-item events and state counters
	// have been written, immediately before the transaction's durable receipt.
	if _, err := repo.db.Exec(t.Context(), `CREATE FUNCTION medicine_receipt_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.operation_id = 'fail-receipt' THEN RAISE EXCEPTION 'injected receipt failure'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER medicine_receipt_failure BEFORE INSERT ON medicine_operation_receipts FOR EACH ROW EXECUTE FUNCTION medicine_receipt_failure()`); err != nil {
		t.Fatal(err)
	}
	batch := medicineTestBatch("fail-receipt", 2)
	if _, err := repo.ApplyMedicineBatch(t.Context(), "owner", batch); err == nil {
		t.Fatal("receipt insertion did not fail")
	}
	after, err := repo.ReadMedicineBox(t.Context(), "owner")
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial failed batch", before, after, err)
	}
	if _, found, err := repo.GetMedicineOperation(t.Context(), "owner", batch.OperationID); err != nil || found {
		t.Fatal("failed transaction retained receipt", found, err)
	}
	var eventsAfter int
	if err := repo.db.QueryRow(t.Context(), `SELECT count(*) FROM events`).Scan(&eventsAfter); err != nil || eventsAfter != eventsBefore {
		t.Fatal("failed transaction emitted global event", eventsBefore, eventsAfter, err)
	}
	if _, err := repo.db.Exec(t.Context(), `DROP TRIGGER medicine_receipt_failure ON medicine_operation_receipts`); err != nil {
		t.Fatal(err)
	}
	receipt, err := repo.ApplyMedicineBatch(t.Context(), "owner", batch)
	if err != nil || receipt.Sequence != before.Sequence+2 {
		t.Fatal(receipt, err)
	}
	replayed, err := repo.ApplyMedicineBatch(t.Context(), "owner", batch)
	if err != nil || !reflect.DeepEqual(receipt, replayed) {
		t.Fatal("replay changed receipt", replayed, err)
	}
}
