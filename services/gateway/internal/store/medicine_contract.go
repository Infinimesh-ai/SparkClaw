package store

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"time"
)

// MedicineRepository is implemented by every authoritative store, including the
// default file store. Workbench-scoped temporary stores are not substituted for it.
type MedicineRepository = medicine.Repository

func cloneMedicineBoxes(rows map[string]medicine.State) map[string]medicine.State {
	out := map[string]medicine.State{}
	for owner, state := range rows {
		out[owner] = medicine.CloneState(state)
	}
	return out
}
func (s *MemoryStore) medicineStateLocked(owner string) medicine.State {
	state, ok := s.medicineBoxes[owner]
	if !ok {
		return medicine.EmptyState(owner)
	}
	return state
}
func (s *MemoryStore) ReadMedicineBox(ctx context.Context, owner string) (medicine.Box, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineRead, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMedicineRead, ctx); err != nil {
		return medicine.Box{}, err
	}
	if err := medicine.ValidateOwner(owner); err != nil {
		return medicine.Box{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMedicineRead, ctx); err != nil {
		return medicine.Box{}, err
	}
	return medicine.CloneState(s.medicineStateLocked(owner)).Box, nil
}
func (s *MemoryStore) ApplyMedicineBatch(ctx context.Context, owner string, batch medicine.Batch) (medicine.Receipt, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineApply, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMedicineApply, ctx); err != nil {
		return medicine.Receipt{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMedicineApply, ctx); err != nil {
		return medicine.Receipt{}, err
	}
	next, receipt, err := medicine.ApplyBatch(owner, s.medicineStateLocked(owner), batch, time.Now().UTC())
	if err != nil {
		return medicine.Receipt{}, err
	}
	if next.Sequence != s.medicineStateLocked(owner).Sequence {
		s.appendEventLocked("medicine.changed", "", "", map[string]any{"owner_id": owner, "operation_id": batch.OperationID})
	}
	s.medicineBoxes[owner] = next
	return cloneMedicineValue(receipt), nil
}
func (s *MemoryStore) ImportMedicines(ctx context.Context, owner string, req medicine.ImportRequest) (medicine.ImportResult, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineImport, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMedicineImport, ctx); err != nil {
		return medicine.ImportResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := operationContextError(OperationMedicineImport, ctx); err != nil {
		return medicine.ImportResult{}, err
	}
	next, result, err := medicine.ApplyImport(owner, s.medicineStateLocked(owner), req, time.Now().UTC())
	if err != nil {
		return medicine.ImportResult{}, err
	}
	if next.Sequence != s.medicineStateLocked(owner).Sequence {
		s.appendEventLocked("medicine.changed", "", "", map[string]any{"owner_id": owner, "operation_id": req.OperationID})
	}
	s.medicineBoxes[owner] = next
	return cloneMedicineValue(result), nil
}
func cloneMedicineValue[T any](value T) T {
	raw, _ := json.Marshal(value)
	var out T
	_ = json.Unmarshal(raw, &out)
	return out
}
func medicineOperation(state medicine.State, id string) (medicine.OperationResult, bool) {
	if row, ok := state.Receipts[id]; ok {
		return cloneMedicineValue(medicine.OperationResult{OperationID: id, Batch: &row}), true
	}
	if row, ok := state.Imports[id]; ok {
		return cloneMedicineValue(medicine.OperationResult{OperationID: id, Import: &row.Result}), true
	}
	return medicine.OperationResult{}, false
}
func (s *MemoryStore) GetMedicineOperation(ctx context.Context, owner, id string) (medicine.OperationResult, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineOperationGet, s.operationTimeouts)
	defer cancel()
	if err := operationContextError(OperationMedicineOperationGet, ctx); err != nil {
		return medicine.OperationResult{}, false, err
	}
	if medicine.ValidateOwner(owner) != nil || medicine.ValidateOwner(id) != nil {
		return medicine.OperationResult{}, false, medicine.ErrInvalid
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := operationContextError(OperationMedicineOperationGet, ctx); err != nil {
		return medicine.OperationResult{}, false, err
	}
	out, ok := medicineOperation(s.medicineStateLocked(owner), id)
	return out, ok, nil
}
func (s *FileStore) ReadMedicineBox(ctx context.Context, owner string) (medicine.Box, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMedicineRead, 1)
	if err != nil {
		return medicine.Box{}, err
	}
	defer release()
	return s.inner.ReadMedicineBox(ctx, owner)
}
func (s *FileStore) GetMedicineOperation(ctx context.Context, owner, id string) (medicine.OperationResult, bool, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMedicineOperationGet, 1)
	if err != nil {
		return medicine.OperationResult{}, false, err
	}
	defer release()
	return s.inner.GetMedicineOperation(ctx, owner, id)
}
func (s *FileStore) ApplyMedicineBatch(ctx context.Context, owner string, batch medicine.Batch) (medicine.Receipt, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMedicineApply, fileAdmissionCapacity)
	if err != nil {
		return medicine.Receipt{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMedicineApply, func(ctx context.Context) (medicine.Receipt, error) {
		return s.inner.ApplyMedicineBatch(ctx, owner, batch)
	})
}
func (s *FileStore) ImportMedicines(ctx context.Context, owner string, req medicine.ImportRequest) (medicine.ImportResult, error) {
	ctx, release, err := s.admitMigrated(ctx, OperationMedicineImport, fileAdmissionCapacity)
	if err != nil {
		return medicine.ImportResult{}, err
	}
	defer release()
	return runFileCommand(s, ctx, OperationMedicineImport, func(ctx context.Context) (medicine.ImportResult, error) {
		return s.inner.ImportMedicines(ctx, owner, req)
	})
}
