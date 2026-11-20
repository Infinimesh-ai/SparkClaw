package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

const maxMedicineInventoryBody = 63488

func readMedicineJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if len(r.URL.Query()) != 0 {
		return medicine.ErrInvalid
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMedicineInventoryBody))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil || d.Decode(new(any)) != io.EOF {
		return medicine.ErrInvalid
	}
	return nil
}
func decodeMedicineFields(raw json.RawMessage) (medicine.Fields, error) {
	var fields medicine.Fields
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&fields) != nil {
		return fields, medicine.ErrInvalid
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return fields, medicine.ErrInvalid
	}
	for _, name := range []string{"name", "category", "quantity", "unit", "expires_on", "no_expiry"} {
		value, ok := keys[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fields, medicine.ErrInvalid
		}
	}
	return fields, nil
}
func (s *Server) syncMedicine(w http.ResponseWriter, r *http.Request) {
	var input medicineSyncInput
	if readMedicineJSON(w, r, &input) != nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	out, err := s.medicineSync(r.Context(), principalForRequest(r).OwnerID, input)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, out)
}
func (s *Server) getMedicineOperation(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()) != 0 {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	out, err := s.medicineOperation(r.Context(), principalForRequest(r).OwnerID, r.PathValue("operation"))
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, out)
}
func (s *Server) createMedicine(w http.ResponseWriter, r *http.Request) {
	s.writeMedicineItem(w, r, medicine.OperationCreate)
}
func (s *Server) updateMedicine(w http.ResponseWriter, r *http.Request) {
	s.writeMedicineItem(w, r, medicine.OperationUpdate)
}
func (s *Server) deleteMedicine(w http.ResponseWriter, r *http.Request) {
	s.writeMedicineItem(w, r, medicine.OperationDelete)
}
func (s *Server) writeMedicineItem(w http.ResponseWriter, r *http.Request, operation string) {
	var input medicineItemInput
	if readMedicineJSON(w, r, &input) != nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	input.ItemID = r.PathValue("item")
	out, err := s.medicineWriteItem(r.Context(), principalForRequest(r).OwnerID, operation, input)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) batchMedicine(w http.ResponseWriter, r *http.Request) {
	var input medicineBatchInput
	if readMedicineJSON(w, r, &input) != nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	out, err := s.medicineBatch(r.Context(), principalForRequest(r).OwnerID, input)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) importMedicine(w http.ResponseWriter, r *http.Request) {
	var input medicineImportInput
	if readMedicineJSON(w, r, &input) != nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	out, err := s.medicineImport(r.Context(), principalForRequest(r).OwnerID, input)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// The medicine box's business functions, shared by the HTTP handlers and
// the link domain methods.

type medicineSyncInput struct {
	Cursor *string `json:"cursor"`
	Limit  *int    `json:"limit"`
}

func (s *Server) medicineSync(ctx context.Context, owner string, input medicineSyncInput) (any, error) {
	if input.Cursor == nil || input.Limit == nil {
		return nil, medicine.ErrInvalid
	}
	return medicine.New(s.store).Sync(ctx, owner, *input.Cursor, *input.Limit)
}

func (s *Server) medicineOperation(ctx context.Context, owner, operationID string) (any, error) {
	out, ok, err := s.store.GetMedicineOperation(ctx, owner, operationID)
	if err == nil && !ok {
		err = medicine.ErrNotFound
	}
	return out, err
}

type medicineItemInput struct {
	OperationID string          `json:"operation_id"`
	BaseVersion *int64          `json:"base_version,omitempty"`
	Item        json.RawMessage `json:"item,omitempty"`
	// ItemID comes from the route (HTTP) or the method arguments (link).
	ItemID string `json:"-"`
}

func (s *Server) medicineWriteItem(ctx context.Context, owner, operation string, input medicineItemInput) (any, error) {
	entry := medicine.Entry{EntryID: "item", ID: input.ItemID}
	if operation == medicine.OperationCreate {
		if input.BaseVersion != nil || entry.ID != "" {
			return nil, medicine.ErrInvalid
		}
	} else {
		if input.BaseVersion == nil || entry.ID == "" {
			return nil, medicine.ErrInvalid
		}
		entry.BaseVersion = *input.BaseVersion
	}
	if operation == medicine.OperationDelete {
		if len(input.Item) > 0 {
			return nil, medicine.ErrInvalid
		}
	} else {
		var err error
		if entry.Fields, err = decodeMedicineFields(input.Item); err != nil {
			return nil, err
		}
	}
	receipt, err := s.store.ApplyMedicineBatch(ctx, owner, medicine.Batch{OperationID: input.OperationID, Operation: operation, Items: []medicine.Entry{entry}})
	if err != nil {
		return nil, err
	}
	if operation == medicine.OperationDelete {
		return map[string]any{"deleted": true}, nil
	}
	return receipt.Items[0].After, nil
}

type medicineBatchInput struct {
	OperationID string `json:"operation_id"`
	Operation   string `json:"operation"`
	Items       []struct {
		EntryID     string          `json:"entry_id"`
		ID          string          `json:"id,omitempty"`
		BaseVersion int64           `json:"base_version,omitempty"`
		Fields      json.RawMessage `json:"fields"`
	} `json:"items"`
}

func (s *Server) medicineBatch(ctx context.Context, owner string, input medicineBatchInput) (any, error) {
	if len(input.Items) > medicine.MaxBatch || input.Operation != medicine.OperationCreate && input.Operation != medicine.OperationUpdate {
		return nil, medicine.ErrInvalid
	}
	batch := medicine.Batch{OperationID: input.OperationID, Operation: input.Operation}
	for _, entry := range input.Items {
		fields, err := decodeMedicineFields(entry.Fields)
		if err != nil {
			return nil, err
		}
		batch.Items = append(batch.Items, medicine.Entry{EntryID: entry.EntryID, ID: entry.ID, BaseVersion: entry.BaseVersion, Fields: fields})
	}
	return s.store.ApplyMedicineBatch(ctx, owner, batch)
}

type medicineImportInput struct {
	OperationID    string            `json:"operation_id"`
	InstallationID string            `json:"installation_id"`
	Items          []json.RawMessage `json:"items"`
}

func (s *Server) medicineImport(ctx context.Context, owner string, input medicineImportInput) (any, error) {
	if len(input.Items) > medicine.MaxImport {
		return nil, medicine.ErrInvalid
	}
	request := medicine.ImportRequest{OperationID: input.OperationID, InstallationID: input.InstallationID}
	for _, raw := range input.Items {
		var item medicine.Item
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&item) != nil {
			return nil, medicine.ErrInvalid
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) != nil {
			return nil, medicine.ErrInvalid
		}
		for _, name := range []string{"name", "category", "quantity", "unit", "expires_on", "no_expiry"} {
			if value, ok := keys[name]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return nil, medicine.ErrInvalid
			}
		}
		request.Items = append(request.Items, item)
	}
	return s.store.ImportMedicines(ctx, owner, request)
}

// medicineInventoryFailure classifies a medicine box failure: HTTP status,
// the domain's reason code, and the link error code.
func medicineInventoryFailure(err error) (int, string, string) {
	switch {
	case errors.Is(err, medicine.ErrInvalid):
		return 400, "medicine_invalid", contract.CodeInvalidRequest
	case errors.Is(err, medicine.ErrConflict):
		return 409, "medicine_conflict", contract.CodeConflict
	case errors.Is(err, medicine.ErrNotFound):
		return 404, "medicine_not_found", contract.CodeNotFound
	case errors.Is(err, medicine.ErrCapacity):
		return 507, "medicine_capacity", contract.CodeQuotaExceeded
	case errors.Is(err, medicine.ErrReset):
		return 409, "medicine_sync_reset_required", contract.CodeConflict
	case store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome:
		return http.StatusServiceUnavailable, "medicine_unknown_outcome", contract.CodeOutcomeUnknown
	}
	return http.StatusServiceUnavailable, "medicine_unavailable", contract.CodeBackendUnavailable
}
func writeMedicineInventoryError(w http.ResponseWriter, err error) {
	status, code, _ := medicineInventoryFailure(err)
	writeJSON(w, status, map[string]any{"code": code, "error": code, "retryable": false})
}
