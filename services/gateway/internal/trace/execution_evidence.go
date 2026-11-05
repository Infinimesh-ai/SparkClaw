package trace

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/executionevidence"
)

// NewExecutionEvidenceRecorder is opt-in internal evaluation. Transient desktop
// executions never borrow the resident host's content store.
func NewExecutionEvidenceRecorder(root string, cfg config.Config) *executionevidence.Recorder {
	if !cfg.Storage.ExecutionEvidence.Enabled || strings.TrimSpace(root) == "" {
		return nil
	}
	options := cfg.Storage.ExecutionEvidence.Options()
	patterns := appendUniqueNonEmpty(traceRedactPatterns(cfg), []string{"authorization", "cookie"})
	options.Redact = func(raw []byte) []byte {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			if safe, err := json.Marshal(redactEvidenceValue(value, patterns)); err == nil {
				original, _ := json.Marshal(value)
				if bytes.Equal(original, safe) {
					return raw
				}
				return safe
			}
		}
		return []byte(redactString(string(raw), patterns))
	}
	return executionevidence.New(filepath.Join(root, "document-evidence"), options)
}

// Numeric token usage/limits are public telemetry, not credential tokens. Keep
// their measured values while applying the existing secret rules recursively.
func redactEvidenceValue(value any, patterns []string) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			_, number := item.(json.Number)
			if number && strings.HasSuffix(key, "_tokens") {
				out[key] = item
			} else if matchesRedactPattern(key, patterns) {
				out[key] = redactedValue
			} else {
				out[key] = redactEvidenceValue(item, patterns)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = redactEvidenceValue(item, patterns)
		}
		return out
	default:
		return redactAny(value, patterns)
	}
}

func (w *Writer) ExecutionEvidence() *executionevidence.Recorder {
	if w == nil {
		return nil
	}
	return w.evidence
}
