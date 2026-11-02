package medicine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func fields(name string) Fields {
	return Fields{Name: name, Category: "other", Quantity: 2, Unit: "tablet", ExpiresOn: "2027-05-31"}
}
func batch(id string, n int) Batch {
	b := Batch{OperationID: id, Operation: OperationCreate}
	for i := 0; i < n; i++ {
		b.Items = append(b.Items, Entry{EntryID: fmt.Sprint("e", i), Fields: fields(fmt.Sprint("药", i))})
	}
	return b
}
func TestBatchAtomicVersionsReceipts(t *testing.T) {
	original := EmptyState("owner")
	now := time.Now()
	bad := batch("bad", 2)
	bad.Items[1].Fields.Quantity = -1
	if _, _, err := ApplyBatch("owner", original, bad, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if len(original.Items) != 0 || len(original.Receipts) != 0 {
		t.Fatal("failure changed original")
	}
	next, result, err := ApplyBatch("owner", original, batch("one", 2), now)
	if err != nil || len(next.Items) != 2 || len(result.Items) != 2 || next.Sequence != 2 {
		t.Fatal(result, err)
	}
	replay, repeated, err := ApplyBatch("owner", next, batch("one", 2), now.Add(time.Hour))
	if err != nil || !reflect.DeepEqual(replay, next) || !reflect.DeepEqual(repeated, result) {
		t.Fatal("replay changed batch", err)
	}
	changed := batch("one", 2)
	changed.Items[0].Fields.Quantity = 3
	if _, _, err := ApplyBatch("owner", next, changed, now); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	update := Batch{OperationID: "update", Operation: OperationUpdate}
	for i, item := range next.Items {
		f := item.Fields
		f.Quantity = 1
		update.Items = append(update.Items, Entry{EntryID: fmt.Sprint("e", i), ID: item.ID, BaseVersion: item.Version, Fields: f})
	}
	update.Items[1].BaseVersion = 99
	if _, _, err := ApplyBatch("owner", next, update, now); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, item := range next.Items {
		if item.Quantity != 2 || item.Version != 1 {
			t.Fatal("partial update")
		}
	}
	update.Items[1].BaseVersion = 1
	updated, receipt, err := ApplyBatch("owner", next, update, now)
	if err != nil || updated.Sequence != 4 || receipt.Items[0].Before.Quantity != 2 || receipt.Items[0].After.Quantity != 1 {
		t.Fatal(receipt, err)
	}
	update.OperationID = "duplicate"
	update.Items[1] = update.Items[0]
	update.Items[1].EntryID = "different"
	if _, _, err := ApplyBatch("owner", next, update, now); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}
func TestValidationAndCapacity(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Fields)
	}{{"missing-expiry", func(f *Fields) { f.ExpiresOn = "" }}, {"date", func(f *Fields) { f.ExpiresOn = "2026-02-30" }}, {"quantity", func(f *Fields) { f.Quantity = 1.001 }}, {"unit", func(f *Fields) { f.Unit = "unknown" }}, {"name", func(f *Fields) { f.Name = "" }}, {"notes", func(f *Fields) { f.Notes = "a\tb" }}} {
		t.Run(test.name, func(t *testing.T) {
			f := fields("test")
			test.change(&f)
			if _, err := ValidateFields(f, false); !errors.Is(err, ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
	f := fields("test")
	f.NoExpiry = true
	f.ExpiresOn = ""
	if _, err := ValidateFields(f, false); err != nil {
		t.Fatal(err)
	}
	f.NoExpiry = false
	if _, err := ValidateFields(f, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyBatch("owner", EmptyState("owner"), batch("large", 21), time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	full := EmptyState("owner")
	full.Items = make([]Item, 1999)
	if _, _, err := ApplyBatch("owner", full, batch("full", 2), time.Now()); !errors.Is(err, ErrCapacity) {
		t.Fatal(err)
	}
}
func TestImportPreservesExistingAndLegacy(t *testing.T) {
	now := time.Now().UTC()
	created, receipt, err := ApplyBatch("owner", EmptyState("owner"), batch("create", 1), now)
	if err != nil {
		t.Fatal(err)
	}
	existing := *receipt.Items[0].After
	legacy := Item{ID: "med_0123456789abcdef0123456789abcdef", Fields: fields("legacy"), CreatedAt: now.Add(-time.Hour)}
	legacy.ExpiresOn = ""
	request := ImportRequest{OperationID: "import", InstallationID: "desktop", Items: []Item{existing, legacy}}
	request.Items[0].Quantity = 999
	next, result, err := ApplyImport("owner", created, request, now)
	if err != nil || result.Imported != 1 || result.Skipped != 1 || result.Items[0].Quantity != 2 || !result.Items[1].CreatedAt.Equal(legacy.CreatedAt) {
		t.Fatal(result, err)
	}
	_, again, err := ApplyImport("owner", next, request, now)
	if err != nil || !reflect.DeepEqual(again, result) {
		t.Fatal(again, err)
	}
}

// The repository fixture exercises paging against a real changing domain state.
type repo struct{ state State }

func (r *repo) ReadMedicineBox(context.Context, string) (Box, error) {
	return CloneState(r.state).Box, nil
}
func (r *repo) ApplyMedicineBatch(context.Context, string, Batch) (Receipt, error) { panic("unused") }
func (r *repo) ImportMedicines(context.Context, string, ImportRequest) (ImportResult, error) {
	panic("unused")
}
func TestSyncSnapshotMutationResetAndOwner(t *testing.T) {
	state, _, err := ApplyBatch("owner", EmptyState("owner"), batch("create", 3), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	r := &repo{state}
	s := New(r)
	first, err := s.Sync(context.Background(), "owner", "", 2)
	if err != nil || !first.More || len(first.Events) != 2 || first.Mode != "snapshot" {
		t.Fatal(first, err)
	}
	last, err := s.Sync(context.Background(), "owner", first.Cursor, 2)
	if err != nil || last.More || len(last.Events) != 1 {
		t.Fatal(last, err)
	}
	delta, err := s.Sync(context.Background(), "owner", last.Cursor, 2)
	if err != nil || len(delta.Events) != 0 || delta.Mode != "delta" {
		t.Fatal(delta, err)
	}
	if _, err = s.Sync(context.Background(), "other", last.Cursor, 2); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	r.state, _, err = ApplyBatch("owner", r.state, batch("new", 1), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Sync(context.Background(), "owner", first.Cursor, 2); !errors.Is(err, ErrReset) {
		t.Fatal(err)
	}
	delta, err = s.Sync(context.Background(), "owner", last.Cursor, 2)
	if err != nil || len(delta.Events) != 1 || delta.Events[0].Sequence != 4 {
		t.Fatal(delta, err)
	}
}

func (r *repo) GetMedicineOperation(context.Context, string, string) (OperationResult, bool, error) {
	panic("unused")
}
