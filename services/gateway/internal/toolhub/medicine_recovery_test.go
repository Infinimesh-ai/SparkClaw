package toolhub

import (
	"context"
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type uncertainMedicineStore struct {
	*store.MemoryStore
	writes, lookups       int
	committed, mismatched bool
}

func (s *uncertainMedicineStore) ApplyMedicineBatch(ctx context.Context, owner string, batch medicine.Batch) (medicine.Receipt, error) {
	s.writes++
	if s.committed {
		if _, err := s.MemoryStore.ApplyMedicineBatch(ctx, owner, batch); err != nil {
			return medicine.Receipt{}, err
		}
	}
	return medicine.Receipt{}, &store.StoreError{Code: store.StoreErrorUnknownOutcome, Operation: store.OperationMedicineApply, Err: errors.New("synthetic lost commit acknowledgement")}
}
func (s *uncertainMedicineStore) GetMedicineOperation(ctx context.Context, owner, id string) (medicine.OperationResult, bool, error) {
	s.lookups++
	receipt, found, err := s.MemoryStore.GetMedicineOperation(ctx, owner, id)
	if s.mismatched && receipt.Batch != nil {
		receipt.Batch.Digest = "different-payload"
	}
	return receipt, found, err
}
func TestMedicineUnknownOutcomeOnlyReadsBoundReceipt(t *testing.T) {
	for _, c := range []struct {
		name                           string
		committed, mismatched, success bool
	}{{"committed", true, false, true}, {"not_committed", false, false, false}, {"changed_payload", true, true, false}} {
		t.Run(c.name, func(t *testing.T) {
			repo := &uncertainMedicineStore{MemoryStore: store.NewMemoryStore(), committed: c.committed, mismatched: c.mismatched}
			hub := New(config.Default(), repo).WithMedicineRepository(repo)
			defer hub.Close()
			session, err := repo.CreateSessionWithScope(t.Context(), "Medicine", "recovery-owner", t.TempDir(), "webchat", false)
			if err != nil {
				t.Fatal(err)
			}
			batch := medicine.Batch{OperationID: "medicine_recovery_run", Operation: medicine.OperationCreate, Items: []medicine.Entry{{EntryID: "one", Fields: medicine.Fields{Name: "Synthetic", Category: "other", Quantity: 1, Unit: "box", NoExpiry: true}}}}
			_, err = hub.medicineWrite(t.Context(), map[string]any{"batch": batch}, session.ID, "recovery_run")
			if (err == nil) != c.success || repo.writes != 1 || repo.lookups != 1 {
				t.Fatalf("success=%v writes=%d lookups=%d err=%v", c.success, repo.writes, repo.lookups, err)
			}
			box, err := repo.ReadMedicineBox(t.Context(), session.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if c.committed {
				want = 1
			}
			if len(box.Items) != want {
				t.Fatalf("unexpected records %+v", box)
			}
		})
	}
}
