package gateway

import (
	"encoding/json"
	"strings"
	"sync"

	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

// This recorder observes only requests in the disposable native lab. Its
// allowlist excludes credentials, authorization, history and local schedule
// contents. Observing successful ACKs does not manufacture display evidence:
// the native test also reads the original execution's persisted status.
type scheduleNativeEvidence struct {
	mu       sync.Mutex
	inputs   []map[string]any
	receipts []map[string]any
}

func newScheduleNativeEvidence(mode string) *scheduleNativeEvidence {
	if mode != "schedule" && mode != "schedule-real-model" {
		return nil
	}
	return &scheduleNativeEvidence{inputs: []map[string]any{}, receipts: []map[string]any{}}
}

func (e *scheduleNativeEvidence) observe(request wb.Request, response wb.Response) {
	if e == nil {
		return
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(request.Body, &body) != nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	switch request.Operation {
	case wb.OperationSubmit:
		var messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(body["messages"], &messages) != nil || len(messages) == 0 {
			return
		}
		var conversation string
		_ = json.Unmarshal(body["local_conversation_id"], &conversation)
		e.inputs = append(e.inputs, map[string]any{"request_id": request.RequestID, "local_conversation_id": conversation,
			"input": messages[len(messages)-1].Content, "client_presentation": body["client_presentation"], "http_status": response.Status,
			"local_schedule_content_present": strings.Contains(string(request.Body), "local-only") || strings.Contains(string(request.Body), "other-owner schedule")})
	case wb.OperationAck:
		if receipt, ok := body["client_action_receipt"]; ok && response.Status >= 200 && response.Status < 300 {
			e.receipts = append(e.receipts, map[string]any{"request_id": request.RequestID, "receipt": receipt})
		}
	}
}

func (e *scheduleNativeEvidence) snapshot() any {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return map[string]any{"inputs": append([]map[string]any{}, e.inputs...), "receipts": append([]map[string]any{}, e.receipts...)}
}
