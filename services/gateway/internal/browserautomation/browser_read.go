package browserautomation

import "strings"

func sameBrowserReadURL(left, right string) bool {
	left = strings.TrimSuffix(strings.TrimSpace(left), "/")
	right = strings.TrimSuffix(strings.TrimSpace(right), "/")
	return left != "" && strings.EqualFold(left, right)
}

func applyPageReadAuth(result *PageReadResult, metadata map[string]any) {
	if result == nil || metadata == nil {
		return
	}
	result.AuthState = firstStringValue(metadata, "browser_page_auth_state", "authState", "auth_state")
	result.AuthConfidence = firstStringValue(metadata, "browser_page_auth_confidence", "authConfidence", "auth_confidence")
	result.AuthSignals = firstStringSliceValue(metadata["browser_page_auth_signals"], metadata["authSignals"], metadata["auth_signals"])
	result.AuthChallengeDetected = boolValue(metadata["auth_challenge_detected"]) || boolValue(metadata["authChallengeDetected"])
}

func firstNonEmptyBrowserString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
