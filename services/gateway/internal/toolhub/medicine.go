package toolhub

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// The service remains backend-owned when an execution receives an ephemeral store.
func (h *ToolHub) WithMedicineRepository(repo medicine.Repository) *ToolHub {
	h.medicine = medicine.New(repo)
	return h
}
func (h *ToolHub) medicineRead(ctx context.Context, _ map[string]any, sessionID, _ string) (Result, error) {
	if h.medicine == nil {
		return Result{}, errors.New("medicine repository unavailable")
	}
	owner, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	box, err := h.medicine.Read(ctx, owner)
	if err != nil {
		return Result{}, err
	}
	return Result{Output: map[string]any{"items": box.Items, "total": len(box.Items), "sequence": box.Sequence}}, nil
}
func (h *ToolHub) medicineWrite(ctx context.Context, args map[string]any, sessionID, runID string) (Result, error) {
	if h.medicine == nil {
		return Result{}, errors.New("medicine repository unavailable")
	}
	owner, err := h.ownerIDForSession(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	var batch medicine.Batch
	raw, err := json.Marshal(args["batch"])
	if err != nil {
		return Result{}, err
	}
	if err = json.Unmarshal(raw, &batch); err != nil {
		return Result{}, err
	}
	// Operation identity is frozen by the Runtime and cannot be chosen by the model.
	if batch.OperationID != "medicine_"+runID {
		return Result{}, errors.New("medicine batch is not bound to its workflow")
	}
	receipt, err := h.medicine.Apply(ctx, owner, batch)
	if store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
		// Read the original receipt once; never retry a batch after an uncertain commit.
		operation, found, lookupErr := h.medicine.Operation(ctx, owner, batch.OperationID)
		if lookupErr != nil {
			return Result{}, errors.Join(err, lookupErr)
		}
		if found && operation.OperationID == batch.OperationID && operation.Batch != nil && operation.Batch.OperationID == batch.OperationID && operation.Batch.Digest == medicine.BatchDigest(owner, batch) {
			receipt, err = *operation.Batch, nil
		}
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Output: receipt}, nil
}
func medicineToolDefinitions() []app.ToolDefinition {
	return []app.ToolDefinition{
		{Name: "medicine.box.read", Description: "Read the authenticated owner's authoritative medicine inventory.", InputSchema: schema("object", nil, map[string]any{}), OutputSchema: objectValueSchema(), Risk: app.RiskRead, Idempotent: true, TimeoutMS: 10000, Sandbox: "forbidden", Audit: "always"},
		{Name: "medicine.box.write", Description: "Commit one complete Runtime-bound medicine batch atomically.", InputSchema: schema("object", []string{"batch"}, map[string]any{"batch": objectValueSchema()}), OutputSchema: objectValueSchema(), Risk: app.RiskReversible, RequiresApproval: false, ArgumentsImmutable: true, Idempotent: true, TimeoutMS: 10000, Sandbox: "forbidden", Audit: "always"},
		{Name: "medicine.drug_info", Description: "Answer one named-medicine question using a bounded source decision and current Info evidence when needed.", InputSchema: schema("object", []string{"query", "today"}, map[string]any{"query": stringSchema(), "today": stringSchema()}), OutputSchema: objectValueSchema(), Risk: app.RiskRead, Idempotent: true, TimeoutMS: 120000, Sandbox: "forbidden", Audit: "always"},
	}
}
