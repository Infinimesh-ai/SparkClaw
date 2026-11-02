package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
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
	var input struct {
		Cursor *string `json:"cursor"`
		Limit  *int    `json:"limit"`
	}
	if readMedicineJSON(w, r, &input) != nil || input.Cursor == nil || input.Limit == nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	out, err := medicine.New(s.store).Sync(r.Context(), principalForRequest(r).OwnerID, *input.Cursor, *input.Limit)
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
	out, ok, err := s.store.GetMedicineOperation(r.Context(), principalForRequest(r).OwnerID, r.PathValue("operation"))
	if err == nil && !ok {
		err = medicine.ErrNotFound
	}
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
	var input struct {
		OperationID string          `json:"operation_id"`
		BaseVersion *int64          `json:"base_version,omitempty"`
		Item        json.RawMessage `json:"item,omitempty"`
	}
	if readMedicineJSON(w, r, &input) != nil {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	entry := medicine.Entry{EntryID: "item", ID: r.PathValue("item")}
	if operation == medicine.OperationCreate {
		if input.BaseVersion != nil || entry.ID != "" {
			writeMedicineInventoryError(w, medicine.ErrInvalid)
			return
		}
	} else {
		if input.BaseVersion == nil {
			writeMedicineInventoryError(w, medicine.ErrInvalid)
			return
		}
		entry.BaseVersion = *input.BaseVersion
	}
	if operation == medicine.OperationDelete {
		if len(input.Item) > 0 {
			writeMedicineInventoryError(w, medicine.ErrInvalid)
			return
		}
	} else {
		var err error
		entry.Fields, err = decodeMedicineFields(input.Item)
		if err != nil {
			writeMedicineInventoryError(w, err)
			return
		}
	}
	receipt, err := s.store.ApplyMedicineBatch(r.Context(), principalForRequest(r).OwnerID, medicine.Batch{OperationID: input.OperationID, Operation: operation, Items: []medicine.Entry{entry}})
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	if operation == medicine.OperationDelete {
		writeJSON(w, 200, map[string]any{"deleted": true})
		return
	}
	writeJSON(w, 200, receipt.Items[0].After)
}
func (s *Server) batchMedicine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OperationID string `json:"operation_id"`
		Operation   string `json:"operation"`
		Items       []struct {
			EntryID     string          `json:"entry_id"`
			ID          string          `json:"id,omitempty"`
			BaseVersion int64           `json:"base_version,omitempty"`
			Fields      json.RawMessage `json:"fields"`
		} `json:"items"`
	}
	if readMedicineJSON(w, r, &input) != nil || len(input.Items) > medicine.MaxBatch {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	if input.Operation != medicine.OperationCreate && input.Operation != medicine.OperationUpdate {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	batch := medicine.Batch{OperationID: input.OperationID, Operation: input.Operation}
	for _, entry := range input.Items {
		fields, err := decodeMedicineFields(entry.Fields)
		if err != nil {
			writeMedicineInventoryError(w, err)
			return
		}
		batch.Items = append(batch.Items, medicine.Entry{EntryID: entry.EntryID, ID: entry.ID, BaseVersion: entry.BaseVersion, Fields: fields})
	}
	out, err := s.store.ApplyMedicineBatch(r.Context(), principalForRequest(r).OwnerID, batch)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func (s *Server) importMedicine(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OperationID    string            `json:"operation_id"`
		InstallationID string            `json:"installation_id"`
		Items          []json.RawMessage `json:"items"`
	}
	if readMedicineJSON(w, r, &input) != nil || len(input.Items) > medicine.MaxImport {
		writeMedicineInventoryError(w, medicine.ErrInvalid)
		return
	}
	request := medicine.ImportRequest{OperationID: input.OperationID, InstallationID: input.InstallationID}
	for _, raw := range input.Items {
		var item medicine.Item
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if d.Decode(&item) != nil {
			writeMedicineInventoryError(w, medicine.ErrInvalid)
			return
		}
		var keys map[string]json.RawMessage
		if json.Unmarshal(raw, &keys) != nil {
			writeMedicineInventoryError(w, medicine.ErrInvalid)
			return
		}
		for _, name := range []string{"name", "category", "quantity", "unit", "expires_on", "no_expiry"} {
			if value, ok := keys[name]; !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				writeMedicineInventoryError(w, medicine.ErrInvalid)
				return
			}
		}
		request.Items = append(request.Items, item)
	}
	out, err := s.store.ImportMedicines(r.Context(), principalForRequest(r).OwnerID, request)
	if err != nil {
		writeMedicineInventoryError(w, err)
		return
	}
	writeJSON(w, 200, out)
}
func writeMedicineInventoryError(w http.ResponseWriter, err error) {
	status, code := http.StatusServiceUnavailable, "medicine_unavailable"
	switch {
	case errors.Is(err, medicine.ErrInvalid):
		status, code = 400, "medicine_invalid"
	case errors.Is(err, medicine.ErrConflict):
		status, code = 409, "medicine_conflict"
	case errors.Is(err, medicine.ErrNotFound):
		status, code = 404, "medicine_not_found"
	case errors.Is(err, medicine.ErrCapacity):
		status, code = 507, "medicine_capacity"
	case errors.Is(err, medicine.ErrReset):
		status, code = 409, "medicine_sync_reset_required"
	case store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome:
		code = "medicine_unknown_outcome"
	}
	writeJSON(w, status, map[string]any{"code": code, "error": code, "retryable": false})
}
