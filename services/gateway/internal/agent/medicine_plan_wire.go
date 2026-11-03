package agent

import (
	"encoding/json"
	"errors"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/medicine"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

// Field arrays avoid optional-property-order constraints in structured-output
// grammars: a late expiry must never disappear after the model emits quantity.
// Runtime immediately normalizes this model-only wire shape into a frozen map.
type medicineModelChange struct {
	Field    string `json:"field"`
	Value    any    `json:"value"`
	Evidence string `json:"evidence"`
}
type medicineModelTarget struct {
	Name      string `json:"name"`
	ExpiresOn string `json:"expires_on"`
	Location  string `json:"location"`
	Evidence  string `json:"evidence"`
}

// Each entry carries its own action. A batch-level choice that included
// "mixed" drew the real model to label plain updates as mixed; per-entry
// actions let Runtime detect mixed batches deterministically instead.
type medicineModelEntry struct {
	Action     string                `json:"action"`
	Changes    []medicineModelChange `json:"changes"`
	Target     *medicineModelTarget  `json:"target,omitempty"`
	Unresolved []medicineUnresolved  `json:"unresolved"`
}
type medicineModelPlan struct {
	Items []medicineModelEntry `json:"items"`
}

func medicinePlanOptions() modelrouter.ChatOptions {
	object := func(props map[string]any, required []string) map[string]any {
		return map[string]any{"type": "object", "additionalProperties": false, "properties": props, "required": required}
	}
	str := map[string]any{"type": "string"}
	change := object(map[string]any{
		"field":    map[string]any{"type": "string", "enum": medicineChangeFields},
		"value":    map[string]any{"anyOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}, map[string]any{"type": "boolean"}}},
		"evidence": str,
	}, []string{"field", "value", "evidence"})
	target := object(map[string]any{"name": str, "expires_on": str, "location": str, "evidence": str}, []string{"name", "expires_on", "location", "evidence"})
	unresolved := object(map[string]any{"field": map[string]any{"type": "string", "enum": append(append([]string{}, medicineChangeFields...), "expiry", "target")}, "reason": map[string]any{"type": "string", "enum": []string{"missing", "ambiguous"}}}, []string{"field", "reason"})
	action := map[string]any{"type": "string", "enum": []string{medicine.OperationCreate, medicine.OperationUpdate, medicine.OperationDelete}}
	entry := object(map[string]any{"action": action, "changes": map[string]any{"type": "array", "maxItems": 11, "items": change}, "target": target, "unresolved": map[string]any{"type": "array", "maxItems": 14, "items": unresolved}}, []string{"action", "changes", "unresolved"})
	schema := object(map[string]any{"items": map[string]any{"type": "array", "minItems": 1, "items": entry}}, []string{"items"})
	return modelrouter.ChatOptions{ForceDisableThinking: true, StrictJSONSchema: &modelrouter.StrictJSONSchema{Name: "medicine_edit_v4", Schema: schema}}
}
func decodeMedicineModelPlan(raw string) (medicineEditPlan, error) {
	var wire medicineModelPlan
	if len(raw) > 64<<10 {
		return medicineEditPlan{}, errors.New("medicine plan too large")
	}
	if err := appstream.DecodeStrict([]byte(raw), &wire); err != nil {
		return medicineEditPlan{}, err
	}
	plan := medicineEditPlan{}
	for _, item := range wire.Items {
		if item.Changes == nil || item.Unresolved == nil {
			return plan, errors.New("missing medicine draft arrays")
		}
		if !slices.Contains([]string{medicine.OperationCreate, medicine.OperationUpdate, medicine.OperationDelete}, item.Action) {
			return plan, errors.New("invalid medicine action")
		}
		if plan.Operation == "" {
			plan.Operation = item.Action
		} else if item.Action != plan.Operation {
			return plan, errMedicineMixedOperations
		}
		entry := medicinePlannedEntry{Changes: map[string]any{}, Evidence: map[string]string{}, Unresolved: item.Unresolved}
		if item.Action == medicine.OperationDelete {
			// A delete writes no fields; only the target can need the owner.
			item.Changes = nil
			entry.Unresolved = []medicineUnresolved{}
			for _, u := range item.Unresolved {
				if u.Field == "target" {
					entry.Unresolved = append(entry.Unresolved, u)
				}
			}
		}
		for _, change := range item.Changes {
			if _, ok := entry.Changes[change.Field]; ok {
				return plan, errors.New("duplicate medicine field")
			}
			entry.Changes[change.Field] = change.Value
			entry.Evidence[change.Field] = change.Evidence
		}
		if item.Target != nil {
			entry.Target = &medicineTarget{Name: item.Target.Name, ExpiresOn: item.Target.ExpiresOn, Location: item.Target.Location}
			entry.Evidence["target"] = item.Target.Evidence
		}
		plan.Items = append(plan.Items, entry)
	}
	normalized, err := json.Marshal(plan)
	if err != nil {
		return plan, err
	}
	return decodeMedicinePlan(string(normalized))
}
