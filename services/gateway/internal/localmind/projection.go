package localmind

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

const stateBase64Threshold = 4096

var (
	obviousSecretPattern = regexp.MustCompile(`(?i)(authorization|password|passwd|secret|token|api[_-]?key|access[_-]?key)\s*[:=]\s*[^\s,;]+`)
	urlPattern           = regexp.MustCompile(`https?://[^\s"'<>]+`)
)

type projectionMode int

const (
	projectionState projectionMode = iota
	projectionArchive
)

func (m *Manager) projectToolResult(result mcpclient.ToolResult, remoteName string) toolhub.Result {
	canonical, ok := result.StructuredContent["result"]
	if !ok {
		canonical = toolResultText(result)
	}
	archive := map[string]any{
		"provider": "localmind", "remote_name": remoteName, "content": result.Content,
		"structured_content": result.StructuredContent, "is_error": result.IsError, "meta": result.Meta,
		"untrusted": true,
	}
	return toolhub.Result{
		Output:        boundedProjection(canonical, projectionState, m.cfg.StateOutputMaxBytes),
		ArchiveOutput: boundedProjection(archive, projectionArchive, m.cfg.ArchiveOutputMaxBytes),
	}
}

func (m *Manager) projectValue(value any, method string) toolhub.Result {
	normalized := normalizeJSONValue(value)
	archive := map[string]any{
		"provider": "localmind", "method": method, "result": normalized, "untrusted": true,
	}
	return toolhub.Result{
		Output:        boundedProjection(normalized, projectionState, m.cfg.StateOutputMaxBytes),
		ArchiveOutput: boundedProjection(archive, projectionArchive, m.cfg.ArchiveOutputMaxBytes),
	}
}

func boundedProjection(value any, mode projectionMode, maxBytes int) any {
	if maxBytes <= 0 {
		if mode == projectionState {
			maxBytes = 16 << 10
		} else {
			maxBytes = 16 << 20
		}
	}
	sanitized := sanitizeValue(normalizeJSONValue(value), mode, maxBytes)
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
		"reason": "LocalMind result exceeded the configured projection limit", "untrusted": true,
	}
}

func sanitizeValue(value any, mode projectionMode, maxBytes int) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, child := range typed {
			if sensitiveResultKey(key) {
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

func sanitizeString(value string, mode projectionMode, maxBytes int) any {
	value = obviousSecretPattern.ReplaceAllString(value, "$1=[REDACTED]")
	value = urlPattern.ReplaceAllStringFunc(value, func(candidate string) string {
		if signedResultURL(candidate) {
			return "[REDACTED_SIGNED_URL]"
		}
		return candidate
	})
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") {
		return "[REDACTED]"
	}
	if mode == projectionState && len(value) > stateBase64Threshold && likelyBase64Result(value) {
		sum := sha256.Sum256([]byte(value))
		return map[string]any{
			"omitted": "base64", "encoded_bytes": len(value), "sha256": hex.EncodeToString(sum[:]), "untrusted": true,
		}
	}
	if mode == projectionState {
		limit := maxBytes / 2
		if limit < 1024 {
			limit = 1024
		}
		if len(value) > limit {
			return value[:limit] + "... [truncated]"
		}
	}
	return value
}

func sensitiveResultKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	for _, fragment := range []string{"authorization", "password", "passwd", "secret", "token", "apikey", "accesskey", "credential", "privatekey"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func signedResultURL(value string) bool {
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

func likelyBase64Result(value string) bool {
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

func normalizeJSONValue(value any) any {
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

func toolResultText(result mcpclient.ToolResult) string {
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

func safeToolErrorText(value string) string {
	projected := boundedProjection(value, projectionState, 500)
	if text, ok := projected.(string); ok {
		return boundedText(text, 500)
	}
	raw, _ := json.Marshal(projected)
	return boundedText(string(raw), 500)
}
