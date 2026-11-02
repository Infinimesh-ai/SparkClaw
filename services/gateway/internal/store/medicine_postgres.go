package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/jackc/pgx/v5"
	"time"
)

const medicineItemColumns = `id,name,category,quantity,unit,COALESCE(to_char(expires_on,'YYYY-MM-DD'),''),no_expiry,location,usage,notes,version,created_at,updated_at`

func scanMedicineItem(row onboardingPostgresRow) (medicine.Item, error) {
	var item medicine.Item
	err := row.Scan(&item.ID, &item.Name, &item.Category, &item.Quantity, &item.Unit, &item.ExpiresOn, &item.NoExpiry, &item.Location, &item.Usage, &item.Notes, &item.Version, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}
func loadMedicineState(ctx context.Context, tx onboardingPostgresTx, owner, operation string, write bool) (medicine.State, error) {
	state := medicine.EmptyState(owner)
	lock := " FOR SHARE"
	if write {
		lock = " FOR UPDATE"
		if _, err := tx.Exec(ctx, `INSERT INTO medicine_box_state(owner_id,epoch) VALUES($1,$2) ON CONFLICT DO NOTHING`, owner, state.Epoch); err != nil {
			return state, err
		}
	}
	err := tx.QueryRow(ctx, `SELECT epoch,sequence,floor,receipt_count FROM medicine_box_state WHERE owner_id=$1`+lock, owner).Scan(&state.Epoch, &state.Sequence, &state.Floor, &state.ReceiptCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	rows, err := tx.Query(ctx, `SELECT `+medicineItemColumns+` FROM medicine_items WHERE owner_id=$1 ORDER BY id COLLATE "C"`, owner)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		item, e := scanMedicineItem(rows)
		if e != nil {
			rows.Close()
			return state, e
		}
		state.Items = append(state.Items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	rows, err = tx.Query(ctx, `SELECT sequence,item_id,deleted,item FROM medicine_events WHERE owner_id=$1 ORDER BY sequence`, owner)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var event medicine.Event
		var raw []byte
		if err = rows.Scan(&event.Sequence, &event.ID, &event.Deleted, &raw); err != nil {
			rows.Close()
			return state, err
		}
		if err = json.Unmarshal(raw, &event.Item); err != nil {
			rows.Close()
			return state, err
		}
		state.Events = append(state.Events, event)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, err
	}
	if operation != "" {
		var kind string
		var raw []byte
		err = tx.QueryRow(ctx, `SELECT kind,receipt FROM medicine_operation_receipts WHERE owner_id=$1 AND operation_id=$2`, owner, operation).Scan(&kind, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			err = nil
		} else if err == nil {
			err = loadMedicineReceipt(&state, operation, kind, raw)
		}
	}
	return state, err
}
func loadMedicineReceipt(state *medicine.State, id, kind string, raw []byte) error {
	switch kind {
	case "batch":
		var receipt medicine.Receipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		if receipt.OperationID != id {
			return medicine.ErrInvalid
		}
		state.Receipts[id] = receipt
	case "import":
		var receipt medicine.ImportReceipt
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		if receipt.Result.OperationID != id {
			return medicine.ErrInvalid
		}
		state.Imports[id] = receipt
	default:
		return medicine.ErrInvalid
	}
	return nil
}
func (s *PostgresStore) ReadMedicineBox(ctx context.Context, owner string) (medicine.Box, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineRead, s.operationTimeouts)
	defer cancel()
	if err := medicine.ValidateOwner(owner); err != nil {
		return medicine.Box{}, err
	}
	session, tx, release, err := beginPostgresTransaction(ctx, OperationMedicineRead, s.medicinePostgres)
	if err != nil {
		return medicine.Box{}, err
	}
	defer releasePostgresSession(session, release)
	state, err := loadMedicineState(ctx, tx, owner, "", false)
	err = rollbackPostgresTransaction(ctx, session, tx, release, err)
	if err != nil {
		return medicine.Box{}, classifyPostgresReadError(OperationMedicineRead, ctx, err)
	}
	return state.Box, nil
}
func (s *PostgresStore) GetMedicineOperation(ctx context.Context, owner, id string) (medicine.OperationResult, bool, error) {
	ctx, cancel := operationContext(ctx, OperationMedicineOperationGet, s.operationTimeouts)
	defer cancel()
	if medicine.ValidateOwner(owner) != nil || medicine.ValidateOwner(id) != nil {
		return medicine.OperationResult{}, false, medicine.ErrInvalid
	}
	var kind string
	var raw []byte
	err := s.medicinePostgres.QueryRow(ctx, `SELECT kind,receipt FROM medicine_operation_receipts WHERE owner_id=$1 AND operation_id=$2`, owner, id).Scan(&kind, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return medicine.OperationResult{}, false, nil
	}
	if err != nil {
		return medicine.OperationResult{}, false, classifyPostgresReadError(OperationMedicineOperationGet, ctx, err)
	}
	state := medicine.EmptyState(owner)
	if err = loadMedicineReceipt(&state, id, kind, raw); err != nil {
		return medicine.OperationResult{}, false, storeError(ctx, OperationMedicineOperationGet, StoreErrorCorrupt, err)
	}
	result, found := medicineOperation(state, id)
	return result, found, nil
}
func persistMedicineState(ctx context.Context, tx onboardingPostgresTx, owner, id string, before, after medicine.State) error {
	// Receipts are immutable. Reconciliation performs no inventory/event write.
	if _, ok := before.Receipts[id]; ok {
		return nil
	}
	if _, ok := before.Imports[id]; ok {
		return nil
	}
	old := map[string]medicine.Item{}
	for _, item := range before.Items {
		old[item.ID] = item
	}
	for _, item := range after.Items {
		previous, exists := old[item.ID]
		delete(old, item.ID)
		if exists && previous == item {
			continue
		}
		_, err := tx.Exec(ctx, `INSERT INTO medicine_items(owner_id,id,name,category,quantity,unit,expires_on,no_expiry,location,usage,notes,version,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,nullif($7,'')::date,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT(owner_id,id) DO UPDATE SET name=EXCLUDED.name,category=EXCLUDED.category,quantity=EXCLUDED.quantity,unit=EXCLUDED.unit,expires_on=EXCLUDED.expires_on,no_expiry=EXCLUDED.no_expiry,location=EXCLUDED.location,usage=EXCLUDED.usage,notes=EXCLUDED.notes,version=EXCLUDED.version,updated_at=EXCLUDED.updated_at`, owner, item.ID, item.Name, item.Category, item.Quantity, item.Unit, item.ExpiresOn, item.NoExpiry, item.Location, item.Usage, item.Notes, item.Version, item.CreatedAt, item.UpdatedAt)
		if err != nil {
			return err
		}
	}
	for itemID := range old {
		if _, err := tx.Exec(ctx, `DELETE FROM medicine_items WHERE owner_id=$1 AND id=$2`, owner, itemID); err != nil {
			return err
		}
	}
	for _, event := range after.Events {
		if event.Sequence <= before.Sequence {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO medicine_events(owner_id,sequence,item_id,deleted,item) VALUES($1,$2,$3,$4,$5)`, owner, event.Sequence, event.ID, event.Deleted, mustJSON(event.Item)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM medicine_events WHERE owner_id=$1 AND sequence<=$2`, owner, after.Floor); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE medicine_box_state SET sequence=$2,floor=$3,receipt_count=$4 WHERE owner_id=$1`, owner, after.Sequence, after.Floor, after.ReceiptCount); err != nil {
		return err
	}
	kind := "batch"
	var value any = after.Receipts[id]
	if receipt, ok := after.Imports[id]; ok {
		kind = "import"
		value = receipt
	}
	if _, err := tx.Exec(ctx, `INSERT INTO medicine_operation_receipts(owner_id,operation_id,kind,receipt) VALUES($1,$2,$3,$4)`, owner, id, kind, mustJSON(value)); err != nil {
		return err
	}
	if after.Sequence != before.Sequence {
		return appendRunEvent(tx, ctx, "medicine.changed", "", "", map[string]any{"owner_id": owner, "operation_id": id})
	}
	return nil
}
func medicinePostgresChange[T any](s *PostgresStore, ctx context.Context, owner, id string, operation StoreOperation, apply func(medicine.State) (medicine.State, T, error)) (T, error) {
	var zero T
	ctx, cancel := operationContext(ctx, operation, s.operationTimeouts)
	defer cancel()
	if medicine.ValidateOwner(owner) != nil || medicine.ValidateOwner(id) != nil {
		return zero, medicine.ErrInvalid
	}
	session, tx, release, err := beginPostgresTransaction(ctx, operation, s.medicinePostgres)
	if err != nil {
		return zero, err
	}
	defer releasePostgresSession(session, release)
	before, err := loadMedicineState(ctx, tx, owner, id, true)
	if err != nil {
		return zero, classifyPostgresReadError(operation, ctx, rollbackPostgresTransaction(ctx, session, tx, release, err))
	}
	after, result, err := apply(before)
	if err != nil {
		return zero, rollbackPostgresTransaction(ctx, session, tx, release, err)
	}
	if err = persistMedicineState(ctx, tx, owner, id, before, after); err != nil {
		return zero, classifyPostgresReadError(operation, ctx, rollbackPostgresTransaction(ctx, session, tx, release, err))
	}
	if err = tx.Commit(ctx); err != nil {
		*release = false
		return result, storeError(ctx, operation, StoreErrorUnknownOutcome, fmt.Errorf("%w", errors.Join(err, session.Terminate(ctx))))
	}
	return result, nil
}
func (s *PostgresStore) ApplyMedicineBatch(ctx context.Context, owner string, batch medicine.Batch) (medicine.Receipt, error) {
	return medicinePostgresChange(s, ctx, owner, batch.OperationID, OperationMedicineApply, func(before medicine.State) (medicine.State, medicine.Receipt, error) {
		return medicine.ApplyBatch(owner, before, batch, time.Now().UTC())
	})
}
func (s *PostgresStore) ImportMedicines(ctx context.Context, owner string, request medicine.ImportRequest) (medicine.ImportResult, error) {
	return medicinePostgresChange(s, ctx, owner, request.OperationID, OperationMedicineImport, func(before medicine.State) (medicine.State, medicine.ImportResult, error) {
		return medicine.ApplyImport(owner, before, request, time.Now().UTC())
	})
}
