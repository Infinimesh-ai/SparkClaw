package toolhub

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func objectSchema(required []string, properties map[string]any) map[string]any {
	return schema("object", required, properties)
}

func strictObjectSchema(required []string, properties map[string]any) map[string]any {
	out := schema("object", required, properties)
	out["additionalProperties"] = false
	return out
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func integerSchema() map[string]any {
	return map[string]any{"type": "integer"}
}

func booleanSchema() map[string]any {
	return map[string]any{"type": "boolean"}
}

func objectValueSchema() map[string]any {
	return map[string]any{"type": "object"}
}

func arraySchema(item map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": item}
}

func stringArraySchema() map[string]any {
	return arraySchema(stringSchema())
}

func scalarValueSchema() map[string]any {
	return map[string]any{"type": []any{"string", "number", "integer", "boolean", "null"}}
}

func browserAutomationDefinition(name, description string, risk app.RiskLevel, approval bool, required []string, extraInput []string, outputRequired []string) app.ToolDefinition {
	return app.ToolDefinition{
		Name:        name,
		Description: description,
		InputSchema: schema("object", required, browserAutomationInputProperties(required, extraInput)),
		OutputSchema: objectSchema(outputRequired, map[string]any{
			"tool":            stringSchema(),
			"raw_tool":        stringSchema(),
			"arguments":       objectValueSchema(),
			"output":          objectValueSchema(),
			"text":            stringSchema(),
			"pages":           arraySchema(objectValueSchema()),
			"browser_mode":    stringSchema(),
			"presentation":    stringSchema(),
			"surface_visible": booleanSchema(),
			"untrusted":       booleanSchema(),
			"provider":        stringSchema(),
			"duration_ms":     integerSchema(),
		}),
		Risk:             risk,
		RequiresApproval: approval,
		Idempotent:       risk == app.RiskRead,
		TimeoutMS:        30000,
		Sandbox:          "forbidden",
		Audit:            "always",
	}
}

func browserAutomationInputProperties(required []string, extra []string) map[string]any {
	all := slices.Clone(required)
	all = append(all, extra...)
	all = append(all, "mode", "target_kind", "focused", "current_focus", "rich_text", "timeout_ms", "reason", "browser_mode", "presentation", "surface_visible", "disable_hidden_browser", "visible_browser")
	out := map[string]any{}
	for _, field := range all {
		switch field {
		case "page_id":
			out[field] = map[string]any{"type": []any{"string", "number"}}
		case "uid", "url", "text", "value", "mode", "target_kind", "reason", "browser_mode", "presentation", "browser_page_ref", "filePath", "snapshot_id", "expected_effect", "interaction_goal":
			out[field] = stringSchema()
		case "focused", "current_focus", "rich_text", "surface_visible", "disable_hidden_browser", "visible_browser", "verbose":
			out[field] = booleanSchema()
		case "session_generation", "page_generation":
			out[field] = map[string]any{"type": []any{"string", "number"}}
		case "timeout_ms":
			out[field] = map[string]any{"type": "number"}
		}
	}
	return out
}

func schema(kind string, required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 kind,
		"required":             required,
		"properties":           properties,
		"additionalProperties": true,
	}
}

func validateInput(def app.ToolDefinition, args map[string]any) error {
	if args == nil {
		args = map[string]any{}
	}
	schemaArgs := args
	if _, ok := args["_verifier"]; ok {
		schemaArgs = make(map[string]any, len(args)-1)
		for key, value := range args {
			if key != "_verifier" {
				schemaArgs[key] = value
			}
		}
	}
	if err := validateSchemaValue(schemaArgs, def.InputSchema, "arguments"); err != nil {
		return fmt.Errorf("%s %w", def.Name, err)
	}
	if def.Name == "pdf.transform" {
		if err := validatePDFTransformArguments(schemaArgs); err != nil {
			return err
		}
	}
	return nil
}

func validateOutput(def app.ToolDefinition, output any) error {
	if len(def.OutputSchema) == 0 {
		return nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("%s output schema violation: output is not JSON serializable: %w", def.Name, err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return fmt.Errorf("%s output schema violation: output is not JSON decodable: %w", def.Name, err)
	}
	if err := validateSchemaValue(normalized, def.OutputSchema, "output"); err != nil {
		return fmt.Errorf("%s output schema violation: %w", def.Name, err)
	}
	return nil
}

func validateSchemaValue(value any, schema map[string]any, path string) error {
	if len(schema) == 0 {
		return nil
	}
	types := schemaTypes(schema["type"])
	if len(types) > 0 && !matchesAnyType(value, types) {
		return fmt.Errorf("%s must be %s", path, strings.Join(types, " or "))
	}
	if rawEnum, ok := schema["enum"]; ok && !matchesEnum(value, rawEnum) {
		return fmt.Errorf("%s must be one of %s", path, enumValues(rawEnum))
	}
	if object, ok := value.(map[string]any); ok {
		if err := validateObject(object, schema, path); err != nil {
			return err
		}
	}
	if items, ok := arrayItems(value); ok {
		if err := validateArray(items, schema, path); err != nil {
			return err
		}
	}
	if text, ok := value.(string); ok {
		if err := validateString(text, schema, path); err != nil {
			return err
		}
	}
	if number, ok := numberValue(value); ok {
		if err := validateNumber(number, schema, path); err != nil {
			return err
		}
	}
	return nil
}

func validateObject(object map[string]any, schema map[string]any, path string) error {
	props := schemaMap(schema["properties"])
	for _, name := range stringList(schema["required"]) {
		if value, ok := object[name]; !ok || value == nil {
			return fmt.Errorf("%s requires %q", path, name)
		}
	}
	additional := schema["additionalProperties"]
	for key, value := range object {
		propSchema, ok := props[key]
		if ok {
			if err := validateSchemaValue(value, propSchema, path+"."+key); err != nil {
				return err
			}
			continue
		}
		if allowed, ok := additional.(bool); ok && !allowed {
			return fmt.Errorf("%s.%s is not allowed", path, key)
		}
		if additionalSchema, ok := additional.(map[string]any); ok {
			if err := validateSchemaValue(value, additionalSchema, path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateArray(items []any, schema map[string]any, path string) error {
	if min, ok := intConstraint(schema["minItems"]); ok && len(items) < min {
		return fmt.Errorf("%s must have at least %d item(s)", path, min)
	}
	if max, ok := intConstraint(schema["maxItems"]); ok && len(items) > max {
		return fmt.Errorf("%s must have at most %d item(s)", path, max)
	}
	itemSchema, ok := schema["items"].(map[string]any)
	if !ok {
		return nil
	}
	for i, item := range items {
		if err := validateSchemaValue(item, itemSchema, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}

func validateString(value string, schema map[string]any, path string) error {
	length := len([]rune(value))
	if min, ok := intConstraint(schema["minLength"]); ok && length < min {
		return fmt.Errorf("%s must be at least %d character(s)", path, min)
	}
	if max, ok := intConstraint(schema["maxLength"]); ok && length > max {
		return fmt.Errorf("%s must be at most %d character(s)", path, max)
	}
	return nil
}

func validateNumber(value float64, schema map[string]any, path string) error {
	if min, ok := numberConstraint(schema["minimum"]); ok && value < min {
		return fmt.Errorf("%s must be >= %s", path, formatNumber(min))
	}
	if max, ok := numberConstraint(schema["maximum"]); ok && value > max {
		return fmt.Errorf("%s must be <= %s", path, formatNumber(max))
	}
	return nil
}

func schemaTypes(raw any) []string {
	switch v := raw.(type) {
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	default:
		return nil
	}
}

func matchesAnyType(v any, types []string) bool {
	for _, typ := range types {
		if matchesType(v, typ) {
			return true
		}
	}
	return false
}

func matchesType(v any, typ string) bool {
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := numberValue(v)
		return ok
	case "integer":
		return isInteger(v)
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := arrayItems(v)
		return ok
	case "null":
		return v == nil
	default:
		return true
	}
}

func schemaMap(raw any) map[string]map[string]any {
	out := map[string]map[string]any{}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	for key, value := range rawMap {
		if nested, ok := value.(map[string]any); ok {
			out[key] = nested
		}
	}
	return out
}

func stringList(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := []string{}
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func arrayItems(raw any) ([]any, bool) {
	if raw == nil {
		return nil, false
	}
	value := reflect.ValueOf(raw)
	if value.Kind() != reflect.Slice && value.Kind() != reflect.Array {
		return nil, false
	}
	items := make([]any, 0, value.Len())
	for i := 0; i < value.Len(); i++ {
		items = append(items, value.Index(i).Interface())
	}
	return items, true
}

func numberValue(raw any) (float64, bool) {
	switch v := raw.(type) {
	case int:
		return float64(v), true
	case int8:
		return float64(v), true
	case int16:
		return float64(v), true
	case int32:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint8:
		return float64(v), true
	case uint16:
		return float64(v), true
	case uint32:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		n, err := v.Float64()
		return n, err == nil
	default:
		return 0, false
	}
}

func isInteger(raw any) bool {
	switch v := raw.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case float32:
		return float64(v) == float64(int64(v))
	case float64:
		return v == float64(int64(v))
	case json.Number:
		_, err := v.Int64()
		return err == nil
	default:
		return false
	}
}

func numberConstraint(raw any) (float64, bool) {
	return numberValue(raw)
}

func intConstraint(raw any) (int, bool) {
	n, ok := numberValue(raw)
	if !ok {
		return 0, false
	}
	return int(n), true
}

func matchesEnum(value, rawEnum any) bool {
	items, ok := arrayItems(rawEnum)
	if !ok {
		return true
	}
	for _, item := range items {
		if valuesEqual(value, item) {
			return true
		}
	}
	return false
}

func valuesEqual(a, b any) bool {
	if an, ok := numberValue(a); ok {
		if bn, ok := numberValue(b); ok {
			return an == bn
		}
	}
	return reflect.DeepEqual(a, b)
}

func enumValues(rawEnum any) string {
	items, ok := arrayItems(rawEnum)
	if !ok {
		return "[]"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprint(item))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
