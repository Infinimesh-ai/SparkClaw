package agent

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func emailReadResult(call app.ToolCall) (app.EmailReadResult, bool) {
	var result app.EmailReadResult
	if call.Tool != app.ToolEmailRead || !toolCallCompleted(call) {
		return result, false
	}
	raw, err := json.Marshal(call.Result)
	if err != nil || json.Unmarshal(raw, &result) != nil ||
		!app.KnownEmailProvider(result.Provider) || result.BrowserCredentialGeneration == 0 ||
		result.ScriptRevision <= 0 {
		return result, false
	}
	if result.Status == "empty" {
		return result, result.Capture == nil
	}
	if (result.Status != "collected" && result.Status != "partial") || result.Capture == nil {
		return result, false
	}
	capture := result.Capture
	digest, err := hex.DecodeString(strings.TrimPrefix(capture.ManifestSHA256, "sha256:"))
	if !strings.HasPrefix(capture.ManifestSHA256, "sha256:") || err != nil || len(digest) != 32 || capture.ManifestPath == "" || path.IsAbs(capture.ManifestPath) ||
		path.Clean(capture.ManifestPath) != capture.ManifestPath || strings.HasPrefix(capture.ManifestPath, "../") ||
		capture.MailID == "" || capture.MailboxID == "" || capture.CaptureID == "" ||
		capture.AttachmentsCount < 0 || capture.AttachmentsCount > 20 ||
		(capture.ReadState != "read" && capture.ReadState != "unread" && capture.ReadState != "unknown") {
		return result, false
	}
	return result, true
}

func adaptBrowserEmailReadOutcome(call app.ToolCall, nodeID app.WorkflowNodeID) app.ToolOutcome {
	outcome := adaptGenericWorkflowOutcome(call, nodeID)
	outcome.Retryable = false
	result, ok := emailReadResult(call)
	if !ok {
		return outcome
	}
	if result.Status != "partial" {
		outcome.Signals = []app.OutcomeSignal{app.OutcomeSignalEmailRead}
	}
	outcome.Refs = []app.ResourceRef{{
		Kind: "email_capture_receipt", Ref: call.ID, Provenance: call.ID,
		Attributes: map[string]string{"provider": result.Provider, "status": result.Status, "storage": "script_capture"},
	}}
	return outcome
}

func groundedEmailReadSummary(calls []app.ToolCall) (string, bool) {
	for index := len(calls) - 1; index >= 0; index-- {
		result, ok := emailReadResult(calls[index])
		if !ok {
			continue
		}
		if result.Status == "empty" {
			return "没有找到可采集的未读邮件。", true
		}
		if result.Status == "partial" {
			return fmt.Sprintf("已保存 %s 一封邮件的部分来源资料和 %d 个附件或内嵌资源，采集尚不完整。%s尚未生成邮件内容总结。", app.EmailProviderDisplayName(result.Provider), result.Capture.AttachmentsCount, emailReadStateSummary(result.Capture.ReadState)), true
		}
		return fmt.Sprintf("已将 %s 的一封邮件来源资料和 %d 个附件或内嵌资源保存到工作区。%s尚未生成邮件内容总结。", app.EmailProviderDisplayName(result.Provider), result.Capture.AttachmentsCount, emailReadStateSummary(result.Capture.ReadState)), true
	}
	return "", false
}

func emailReadStateSummary(state string) string {
	switch state {
	case "read":
		return "已确认邮件为已读。"
	case "unread":
		return "邮件仍为未读。"
	default:
		return "未能确认邮件的已读状态。"
	}
}
