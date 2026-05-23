package trace

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const redactedValue = "[REDACTED]"

func traceRedactPatterns(cfg config.Config) []string {
	return appendUniqueNonEmpty(cfg.Logging.RedactPatterns, cfg.Memory.RedactPatterns)
}

func redactAny(value any, patterns []string) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return redactString(v, patterns)
	case map[string]any:
		return redactMap(v, patterns)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactAny(item, patterns)
		}
		return out
	case []string:
		return redactStringSlice(v, patterns)
	default:
		if converted, ok := redactComplexValue(value, patterns); ok {
			return converted
		}
		return value
	}
}

func redactMap(in map[string]any, patterns []string) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		if matchesRedactPattern(key, patterns) {
			out[key] = redactedValue
			continue
		}
		out[key] = redactAny(value, patterns)
	}
	return out
}

func redactStringSlice(in []string, patterns []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, value := range in {
		out[i] = redactString(value, patterns)
	}
	return out
}

func redactString(value string, patterns []string) string {
	out := value
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}
		out = redactStringPattern(out, pattern)
	}
	return out
}

func redactStringPattern(value, pattern string) string {
	lower := strings.ToLower(value)
	needle := strings.ToLower(pattern)
	if !strings.Contains(lower, needle) {
		return value
	}
	var builder strings.Builder
	start := 0
	for {
		idx := strings.Index(lower[start:], needle)
		if idx < 0 {
			builder.WriteString(value[start:])
			break
		}
		idx += start
		secretStart, ok := secretLikeStart(value, lower, idx+len(pattern))
		if !ok {
			builder.WriteString(value[start : idx+len(pattern)])
			start = idx + len(pattern)
			continue
		}
		end := secretLikeEnd(value, secretStart)
		builder.WriteString(value[start:idx])
		builder.WriteString(pattern)
		builder.WriteString("=")
		builder.WriteString(redactedValue)
		start = end
	}
	return builder.String()
}

func secretLikeStart(value, lower string, start int) (int, bool) {
	i := start
	skippedWhitespace := false
	for i < len(value) && isSpace(value[i]) {
		i++
		skippedWhitespace = true
	}
	if i >= len(value) {
		return 0, false
	}
	if i+2 <= len(lower) && lower[i:i+2] == "is" && (i+2 == len(lower) || isSpace(value[i+2]) || isSeparator(value[i+2])) {
		i += 2
		for i < len(value) && (isSpace(value[i]) || isSeparator(value[i])) {
			i++
		}
		return i, i < len(value)
	}
	if isSeparator(value[i]) {
		for i < len(value) && (isSpace(value[i]) || isSeparator(value[i])) {
			i++
		}
		return i, i < len(value)
	}
	if skippedWhitespace {
		return i, i < len(value)
	}
	return 0, false
}

func secretLikeEnd(value string, start int) int {
	i := start
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower[start:], "bearer ") {
		i += len("bearer ")
		for i < len(value) && isSpace(value[i]) {
			i++
		}
	}
	for i < len(value) {
		ch := value[i]
		if ch == ',' || ch == ';' || ch == '\n' || ch == '\r' || ch == '\t' || ch == ' ' || ch == '"' || ch == '\'' || ch == '}' || ch == ']' {
			break
		}
		i++
	}
	return i
}

func matchesRedactPattern(value string, patterns []string) bool {
	lower := strings.ToLower(value)
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(lower, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func isSeparator(ch byte) bool {
	return ch == ':' || ch == '=' || ch == '"' || ch == '\'' || ch == '`'
}

func isSpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r'
}

func appendUniqueNonEmpty(values ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range values {
		for _, value := range list {
			clean := strings.TrimSpace(value)
			if clean == "" {
				continue
			}
			key := strings.ToLower(clean)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, clean)
		}
	}
	return out
}

func redactComplexValue(value any, patterns []string) (any, bool) {
	if value == nil {
		return nil, true
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		out := map[string]any{}
		iter := rv.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			if matchesRedactPattern(key, patterns) {
				out[key] = redactedValue
				continue
			}
			out[key] = redactAny(iter.Value().Interface(), patterns)
		}
		return out, true
	case reflect.Slice, reflect.Array:
		out := make([]any, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out[i] = redactAny(rv.Index(i).Interface(), patterns)
		}
		return out, true
	case reflect.Struct:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, false
		}
		return redactAny(decoded, patterns), true
	default:
		return nil, false
	}
}
