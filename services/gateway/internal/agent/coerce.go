package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func stringSliceValue(v any) []string {
	switch values := v.(type) {
	case []string:
		return values
	case []any:
		out := []string{}
		for _, value := range values {
			if s, ok := value.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if text := strings.TrimSpace(fmt.Sprint(v)); text != "" && text != "<nil>" {
			return []string{text}
		}
		return nil
	}
}

func anyMap(v any) (map[string]any, bool) {
	switch value := v.(type) {
	case map[string]any:
		return value, true
	case nil:
		return nil, false
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, false
		}
		return out, true
	}
}

func anySlice(v any) []any {
	switch value := v.(type) {
	case []any:
		return value
	case nil:
		return nil
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		var out []any
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil
		}
		return out
	}
}

func firstPresent(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok && strings.TrimSpace(stringValue(value)) != "" && stringValue(value) != "<nil>" {
			return value
		}
	}
	return nil
}

func intLikeValue(v any) int {
	switch value := v.(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case uint:
		return int(value)
	case uint8:
		return int(value)
	case uint16:
		return int(value)
	case uint32:
		return int(value)
	case uint64:
		return int(value)
	case float32:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func boolLikeValue(v any) bool {
	switch value := v.(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func toolArgsSummary(args map[string]any) string {
	if len(args) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return "{unserializable}"
	}
	return trimForEpisode(string(raw), 600)
}
