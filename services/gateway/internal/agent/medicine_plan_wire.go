package agent

import (
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
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
type medicineModelEntry struct {
	Changes    []medicineModelChange `json:"changes"`
	Target     *medicineModelTarget  `json:"target,omitempty"`
	Unresolved []medicineUnresolved  `json:"unresolved"`
}
type medicineModelPlan struct {
	Operation string               `json:"operation"`
	Items     []medicineModelEntry `json:"items"`
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
	entry := object(map[string]any{"changes": map[string]any{"type": "array", "maxItems": 11, "items": change}, "target": target, "unresolved": map[string]any{"type": "array", "maxItems": 14, "items": unresolved}}, []string{"changes", "unresolved"})
	schema := object(map[string]any{"operation": map[string]any{"type": "string", "enum": []string{"create", "update", "delete"}}, "items": map[string]any{"type": "array", "minItems": 1, "items": entry}}, []string{"operation", "items"})
	return modelrouter.ChatOptions{ForceDisableThinking: true, StrictJSONSchema: &modelrouter.StrictJSONSchema{Name: "medicine_edit_v3", Schema: schema}}
}
func decodeMedicineModelPlan(raw string) (medicineEditPlan, error) {
	var wire medicineModelPlan
	if len(raw) > 64<<10 {
		return medicineEditPlan{}, errors.New("medicine plan too large")
	}
	if err := appstream.DecodeStrict([]byte(raw), &wire); err != nil {
		return medicineEditPlan{}, err
	}
	plan := medicineEditPlan{Operation: wire.Operation}
	for _, item := range wire.Items {
		if item.Changes == nil || item.Unresolved == nil {
			return plan, errors.New("missing medicine draft arrays")
		}
		entry := medicinePlannedEntry{Changes: map[string]any{}, Evidence: map[string]string{}, Unresolved: item.Unresolved}
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
