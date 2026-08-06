package agent

import (
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

const maxPersistableApprovalBase64Bytes = 4096

func validateApprovalArgumentPersistence(def app.ToolDefinition, args map[string]any) error {
	if !isLocalMindToolDefinition(def) {
		return nil
	}
	if unsafeApprovalArgument(args, "") {
		return &app.CodedToolError{
			Code: app.ToolErrorMCPPersistenceUnsafe,
			Err:  errors.New("LocalMind tool arguments contain secret, signed URL, or large base64 data that cannot be persisted for approval"),
		}
	}
	return nil
}

func isLocalMindToolDefinition(def app.ToolDefinition) bool {
	for _, capability := range def.Capabilities {
		if capability.Name == app.ToolCapabilityExternalMCPWorkspace &&
			capability.Qualifiers[app.CapabilityQualifierProvider] == app.CapabilityProviderLocalMind {
			return true
		}
	}
	return false
}

func unsafeApprovalArgument(value any, key string) bool {
	if sensitivePersistenceKey(key) {
		return true
	}
	switch typed := value.(type) {
	case map[string]any:
		for childKey, child := range typed {
			if unsafeApprovalArgument(child, childKey) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if unsafeApprovalArgument(child, key) {
				return true
			}
		}
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.HasPrefix(strings.ToLower(trimmed), "bearer ") || signedURL(trimmed) {
			return true
		}
		if len(trimmed) > maxPersistableApprovalBase64Bytes && likelyBase64(trimmed) {
			return true
		}
	}
	return false
}

func sensitivePersistenceKey(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "").Replace(strings.TrimSpace(key)))
	for _, fragment := range []string{"authorization", "password", "passwd", "secret", "token", "apikey", "accesskey", "credential", "privatekey"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func signedURL(value string) bool {
	parsed, err := url.Parse(value)
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

func likelyBase64(value string) bool {
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

func redactedRejectedApprovalArguments(map[string]any) map[string]any {
	return map[string]any{"persistence_rejected": true}
}
