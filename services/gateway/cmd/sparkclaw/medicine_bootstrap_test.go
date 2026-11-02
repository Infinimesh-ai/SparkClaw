package main

import (
	"context"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"path/filepath"
	"testing"
)

func TestBackendRuntimeWiresMedicineRepository(t *testing.T) {
	for _, kind := range []store.BackendKind{store.BackendMemory, store.BackendFile} {
		t.Run(string(kind), func(t *testing.T) {
			runtime, err := store.NewRuntime(t.Context(), store.RuntimeOptions{Backend: kind, File: store.FileStoreOptions{Path: filepath.Join(t.TempDir(), "state.json")}})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = runtime.Close(context.Background()) })
			st := backendFromRuntime(runtime)
			if st.MedicineRepository == nil {
				t.Fatal("mandatory medicine repository missing")
			}
			receipt, err := st.ApplyMedicineBatch(t.Context(), "owner", medicine.Batch{OperationID: "bootstrap", Operation: medicine.OperationCreate, Items: []medicine.Entry{{EntryID: "one", Fields: medicine.Fields{Name: "medicine", Category: "other", Quantity: 1, Unit: "box", NoExpiry: true}}}})
			if err != nil {
				t.Fatal(err)
			}
			box, err := runtime.MedicineRepository().ReadMedicineBox(t.Context(), "owner")
			if err != nil || len(box.Items) != 1 || box.Items[0].ID != receipt.Items[0].After.ID {
				t.Fatal("bootstrap did not use authoritative store", box, err)
			}
		})
	}
}
