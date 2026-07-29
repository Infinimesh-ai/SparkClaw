package agent

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var browserPersistenceURLPattern = regexp.MustCompile(`https?://[^\s<>"'\]\)]+`)

func (r Runtime) redactBrowserToolPersistence(runID, tool string, arguments map[string]any, output any) (map[string]any, any) {
	if !strings.HasPrefix(strings.TrimSpace(tool), "browser.") {
		return arguments, output
	}
	target := app.BrowserTargetDescriptor{QueryProvenance: app.BrowserQueryProviderVolatile}
	if run, ok := r.store.GetRun(runID); ok && run.Workflow != nil && run.Workflow.Browser != nil {
		target = run.Workflow.Browser.Target
	}
	return browserPersistenceMap(target, arguments), browserPersistenceValue(target, "", output)
}

func browserPersistenceMap(target app.BrowserTargetDescriptor, value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	redacted := browserPersistenceValue(target, "", value)
	if mapped, ok := redacted.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func browserPersistenceValue(target app.BrowserTargetDescriptor, key string, value any) any {
	normalized := value
	if value != nil {
		raw, err := json.Marshal(value)
		if err == nil {
			var decoded any
			if json.Unmarshal(raw, &decoded) == nil {
				normalized = decoded
			}
		}
	}
	return redactBrowserPersistenceJSON(target, key, normalized)
}

func redactBrowserPersistenceJSON(target app.BrowserTargetDescriptor, key string, value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, child := range typed {
			out[childKey] = redactBrowserPersistenceJSON(target, childKey, child)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, child := range typed {
			out[index] = redactBrowserPersistenceJSON(target, key, child)
		}
		return out
	case string:
		if browserPersistenceURLField(key) {
			return browserSafePersistenceURL(target, typed)
		}
		return redactBrowserPersistenceText(target, typed)
	default:
		return value
	}
}

func browserPersistenceURLField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "url", "final_url", "current_url", "canonical_url", "redacted_url",
		"login_handoff_url", "post_login_url", "expected_url", "target_url",
		"before_url", "after_url":
		return true
	default:
		return false
	}
}

func redactBrowserPersistenceText(target app.BrowserTargetDescriptor, text string) string {
	return browserPersistenceURLPattern.ReplaceAllStringFunc(text, func(raw string) string {
		return browserSafePersistenceURL(target, raw)
	})
}

func browserSafePersistenceURL(target app.BrowserTargetDescriptor, raw string) string {
	live, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (live.Scheme != "http" && live.Scheme != "https") || live.Host == "" {
		return raw
	}
	live.Scheme = strings.ToLower(live.Scheme)
	live.Host = strings.ToLower(live.Host)
	if live.Path == "" {
		live.Path = "/"
	}
	live.RawQuery = ""
	live.ForceQuery = false
	if target.QueryProvenance == app.BrowserQueryOwnerSupplied {
		if frozen, frozenErr := url.Parse(strings.TrimSpace(target.CanonicalURL)); frozenErr == nil &&
			frozen.Scheme != "" && frozen.Host != "" {
			live.RawQuery = frozen.RawQuery
			live.ForceQuery = frozen.ForceQuery
		}
	}
	return live.String()
}
