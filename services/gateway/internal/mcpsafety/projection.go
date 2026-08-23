package mcpsafety

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
)

const (
	DefaultStateMaxBytes   = 16 << 10
	DefaultArchiveMaxBytes = 16 << 20
	stateBase64Threshold   = 4096
	MaxPersistedBase64     = 4096
)

var (
	obviousSecretPattern = regexp.MustCompile(`(?i)(authorization|password|passwd|secret|token|api[_-]?key|access[_-]?key)\s*[:=]\s*[^\s,;]+`)
	urlPattern           = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type Mode int

const (
	State Mode = iota
	Archive
)

type Limits struct {
	StateMaxBytes   int
	ArchiveMaxBytes int
}

type Projection struct {
	Output        any
	ArchiveOutput any
}

func ProjectToolResult(result mcpclient.ToolResult, provider, remoteName string, limits Limits) Projection {
	archive := map[string]any{
		"provider": provider, "remote_name": remoteName, "content": result.Content,
		"structured_content": result.StructuredContent, "is_error": result.IsError,
		"meta": result.Meta, "untrusted": true,
	}
	return ProjectValue(CanonicalToolResult(result), archive, limits)
}

func ProjectValue(value any, archive any, limits Limits) Projection {
	if archive == nil {
		archive = map[string]any{"result": NormalizeJSONValue(value), "untrusted": true}
	}
	return Projection{
		Output:        BoundedProjection(value, State, limits.StateMaxBytes),
		ArchiveOutput: BoundedProjection(archive, Archive, limits.ArchiveMaxBytes),
	}
}

func CanonicalToolResult(result mcpclient.ToolResult) any {
	if len(result.StructuredContent) > 0 {
		if canonical, ok := result.StructuredContent["result"]; ok {
			return canonical
		}
		return result.StructuredContent
	}
	if len(result.Content) == 1 {
		if text, ok := result.Content[0]["text"].(string); ok {
			var structured any
			if json.Unmarshal([]byte(text), &structured) == nil {
				return structured
			}
			return text
		}
	}
	return map[string]any{"content": result.Content}
}

func BoundedProjection(value any, mode Mode, maxBytes int) any {
	if maxBytes <= 0 {
		if mode == State {
			maxBytes = DefaultStateMaxBytes
		} else {
			maxBytes = DefaultArchiveMaxBytes
		}
	}
	sanitized := sanitizeValue(NormalizeJSONValue(value), mode, maxBytes)
	raw, err := json.Marshal(sanitized)
	if err == nil && len(raw) <= maxBytes {
		return sanitized
	}
	if err != nil {
		return map[string]any{"truncated": true, "reason": "non_json_result", "untrusted": true}
	}
	sum := sha256.Sum256(raw)
	return map[string]any{
		"truncated": true, "original_bytes": len(raw), "sha256": hex.EncodeToString(sum[:]),
		"reason": "external MCP result exceeded the projection limit", "untrusted": true,
	}
}

func UnsafeForPersistence(value any) bool {
	return unsafeForPersistence(value, "")
}

func unsafeForPersistence(value any, key string) bool {
	if SensitiveKey(key) {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if unsafeForPersistence(child, childKey) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if unsafeForPersistence(child, key) {
				return true
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") || obviousSecretPattern.MatchString(trimmed) {
			return true
		}
		for _, candidate := range urlPattern.FindAllString(trimmed, -1) {
			if SignedURL(candidate) {
				return true
			}
		}
		if len(trimmed) > MaxPersistedBase64 && LikelyBase64(trimmed) {
			return true
		}
	}
	return false
}

func SafeErrorText(value string, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 500
	}
	projected := BoundedProjection(value, State, maxBytes)
	if text, ok := projected.(string); ok {
		return TruncateUTF8(text, maxBytes)
	}
	raw, _ := json.Marshal(projected)
	return TruncateUTF8(string(raw), maxBytes)
}

func ToolResultText(result mcpclient.ToolResult) string {
	parts := make([]string, 0, len(result.Content))
	for _, block := range result.Content {
		if blockType, _ := block["type"].(string); blockType != "text" {
			continue
		}
		if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
			parts = append(parts, strings.TrimSpace(text))
		}
	}
	return strings.Join(parts, "\n")
}

func NormalizeJSONValue(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var normalized any
	if json.Unmarshal(raw, &normalized) != nil {
		return value
	}
	return normalized
}

func sanitizeValue(value any, mode Mode, maxBytes int) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if SensitiveKey(key) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = sanitizeValue(child, mode, maxBytes)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = sanitizeValue(child, mode, maxBytes)
		}
		return out
	case string:
		return sanitizeString(typed, mode, maxBytes)
	default:
		return typed
	}
}

func sanitizeString(value string, mode Mode, maxBytes int) any {
	value = obviousSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = urlPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if SignedURL(candidate) {
			return "[REDACTED_SIGNED_URL]"
		}
		return candidate
	})
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") {
		return "[REDACTED]"
	}
	if mode == State && len(value) > stateBase64Threshold && LikelyBase64(value) {
		sum := sha256.Sum256([]byte(value))
		return map[string]any{
			"omitted": "base64", "encoded_bytes": len(value), "sha256": hex.EncodeToString(sum[:]), "untrusted": true,
		}
	}
	if mode == State {
		limit := maxBytes / 2
		if limit < 1024 {
			limit = 1024
		}
		if len(value) > limit {
			return TruncateUTF8(value, limit) + "... [truncated]"
		}
	}
	return value
}

func SensitiveKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	for _, fragment := range []string{"authorization", "password", "passwd", "secret", "token", "apikey", "accesskey", "credential", "privatekey"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func SignedURL(value string) bool {
	parsed, err := url.Parse(strings.TrimRight(value, ".,;)"))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return false
	}
	for key := range parsed.Query() {
		normalized := strings.ToLower(strings.ReplaceAll(key, "-", ""))
		for _, fragment := range []string{"signature", "credential", "securitytoken", "accesskey", "signed", "expires"} {
			if strings.Contains(normalized, fragment) {
				return true
			}
		}
	}
	return false
}

func LikelyBase64(value string) bool {
	if comma := strings.Index(value, ","); strings.HasPrefix(strings.ToLower(value), "data:") && comma >= 0 {
		value = value[comma+1:]
	}
	value = strings.TrimSpace(value)
	if len(value)%4 != 0 {
		return false
	}
	_, err := base64.StdEncoding.DecodeString(value)
	return err == nil
}

// TruncateUTF8 cuts value to at most maxBytes without splitting a rune. It
// is the shared byte-bounded truncation for MCP surfaces; keep one copy.
func TruncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
