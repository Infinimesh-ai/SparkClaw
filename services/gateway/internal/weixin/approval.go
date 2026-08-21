package weixin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func (d *Dispatcher) handleApprovalReply(ctx context.Context, inbound InboundMessage, chatSession app.ExternalChatSession, externalID, retryID, text string, receivedAt time.Time) (bool, error) {
	approval, ok, err := d.pendingApprovalForChatSession(ctx, chatSession)
	if err != nil {
		return true, fmt.Errorf("load pending approval: %w", err)
	}
	if !ok {
		return false, nil
	}
	// One inbound record per external message: reusing retryID keeps the
	// delivery bookkeeping on a single record, so a failed answer send is
	// retried as a plain resend instead of re-entering this flow (where the
	// approval would no longer be pending and the reply would leak to the
	// agent as an ordinary message).
	record := d.store.SaveExternalChatMessage(app.ExternalChatMessage{
		ID:                retryID,
		ChatSessionID:     chatSession.ID,
		BindingID:         inbound.Binding.ID,
		Direction:         "inbound",
		Role:              "user",
		ExternalMessageID: externalID,
		Content:           text,
		ContextToken:      inbound.ContextToken,
		LinkedRunID:       approval.RunID,
		Status:            "received",
		CreatedAt:         receivedAt,
	})
	decision, ok := parseApprovalReply(text)
	if !ok {
		_, err := d.finishControlReply(ctx, inbound, chatSession, record, weixinApprovalPrompt(approval), approval.RunID, "needs_clear_approval_reply")
		return true, err
	}
	if decision {
		candidate, err := d.store.ResolveApproval(ctx, approval.ID, "approved", "confirmed from vx")
		resolved, err := store.ReconcileApprovalWrite(ctx, d.store, candidate, err)
		if err != nil {
			return true, err
		}
		if _, err := d.runtime.ExecuteApprovedToolCall(ctx, resolved); err != nil {
			_, sendErr := d.finishControlReply(ctx, inbound, chatSession, record, "确认失败："+err.Error(), approval.RunID, "failed")
			return true, sendErr
		}
		if result, resumed, err := d.runtime.ResumeRunAfterApproval(ctx, approval.SessionID, approval.RunID); err != nil {
			return true, err
		} else if resumed {
			if len(result.Approvals) > 0 {
				_, sendErr := d.finishControlReply(ctx, inbound, chatSession, record, weixinApprovalPrompt(result.Approvals[len(result.Approvals)-1]), result.Run.ID, "processed")
				return true, sendErr
			}
			ingress := weixinIngress(inbound, chatSession, "approval:"+approval.ID)
			_, deliveryErr := d.finishWorkflowReply(ctx, record, result, ingress)
			return true, deliveryErr
		}
		if err := d.runtime.CompleteRunIfApprovalsResolved(ctx, approval.RunID); err != nil {
			return true, err
		}
		if call, ok, err := d.store.GetToolCall(ctx, resolved.ToolCallID); err != nil {
			return true, err
		} else if ok {
			if answer := weixinApprovedToolAnswer(call); answer != "" {
				_, sendErr := d.finishControlReply(ctx, inbound, chatSession, record, answer, approval.RunID, "processed")
				return true, sendErr
			}
		}
		_, sendErr := d.finishControlReply(ctx, inbound, chatSession, record, "已确认并执行。", approval.RunID, "processed")
		return true, sendErr
	}
	candidate, err := d.store.ResolveApproval(ctx, approval.ID, "rejected", "rejected from vx")
	resolved, err := store.ReconcileApprovalWrite(ctx, d.store, candidate, err)
	if err != nil {
		return true, err
	}
	if call, ok, err := d.store.GetToolCall(ctx, resolved.ToolCallID); err != nil {
		return true, err
	} else if ok {
		now := time.Now().UTC()
		call.Status = "rejected"
		call.Error = "user rejected approval from vx"
		call.CompletedAt = &now
		candidate, saveErr := d.store.SaveToolCall(ctx, call)
		if _, saveErr = store.ReconcileToolCallWrite(ctx, d.store, candidate, saveErr); saveErr != nil {
			return true, saveErr
		}
	}
	if err := d.runtime.CompleteRunIfApprovalsResolved(ctx, resolved.RunID); err != nil {
		return true, err
	}
	_, sendErr := d.finishControlReply(ctx, inbound, chatSession, record, "已取消，本次需要确认的操作没有执行。", resolved.RunID, "processed")
	return true, sendErr
}

func (d *Dispatcher) pendingApprovalForChatSession(ctx context.Context, chatSession app.ExternalChatSession) (app.Approval, bool, error) {
	if strings.TrimSpace(chatSession.LinkedSessionID) == "" {
		return app.Approval{}, false, nil
	}
	approvals, err := d.store.ListApprovals(ctx, "pending")
	if err != nil {
		return app.Approval{}, false, err
	}
	for i := len(approvals) - 1; i >= 0; i-- {
		approval := approvals[i]
		if approval.SessionID == chatSession.LinkedSessionID {
			return approval, true, nil
		}
	}
	return app.Approval{}, false, nil
}

func parseApprovalReply(text string) (bool, bool) {
	value := strings.TrimSpace(strings.ToLower(text))
	value = strings.Trim(value, " \t\r\n。.!！,，")
	switch value {
	case "是", "确认", "同意", "可以", "执行", "继续", "好", "好的", "yes", "y", "ok", "approve", "approved", "confirm":
		return true, true
	case "否", "不", "不要", "取消", "拒绝", "不同意", "停止", "no", "n", "reject", "rejected", "cancel":
		return false, true
	default:
		return false, false
	}
}

func weixinApprovalPrompt(approval app.Approval) string {
	lines := []string{"需要你确认后才能执行：", "操作：" + approvalActionText(approval)}
	if path := cleanWeixinString(approval.Arguments["path"]); path != "" {
		lines = append(lines, "文件："+path)
	}
	if outputPath := cleanWeixinString(approval.Arguments["output_path"]); outputPath != "" {
		lines = append(lines, "将生成："+outputPath)
	}
	if paragraphIndex := intLikeWeixinValue(approval.Arguments["paragraph_index"]); paragraphIndex > 0 {
		lines = append(lines, fmt.Sprintf("目标段落：第 %d 段", paragraphIndex))
	}
	if oldText := cleanWeixinString(approval.Arguments["old_text"]); oldText != "" {
		lines = append(lines, "原文："+trimWeixinText(oldText, 160))
	}
	if text := cleanWeixinString(approval.Arguments["text"]); text != "" {
		lines = append(lines, "修改为："+trimWeixinText(text, 220))
	}
	if command := cleanWeixinString(approval.Arguments["command"]); command != "" {
		lines = append(lines, "命令："+command)
	}
	lines = append(lines, "", "请回复“是”确认执行，或回复“否”取消执行。")
	return strings.Join(lines, "\n")
}

func approvalActionText(approval app.Approval) string {
	if summary := strings.TrimSpace(approval.Summary); summary != "" && !strings.HasPrefix(summary, "Approve ") {
		return summary
	}
	switch approval.Tool {
	case "docx.replace_paragraph":
		return "修改 Word 文档中的一段正文"
	case "docx.insert_paragraph":
		return "在 Word 文档中插入一段正文"
	case "docx.delete_paragraph":
		return "删除 Word 文档中的一段正文"
	case "docx.set_text_style":
		return "调整 Word 文档段落样式"
	case "office.replace_text":
		return "替换 Office 文档中的指定文本"
	case "shell.exec_sandboxed":
		return "执行一条沙箱命令"
	case "file.delete":
		return "删除文件到回收区"
	case "memory.write_sensitive":
		return "写入敏感记忆"
	default:
		return "执行一个需要确认的操作"
	}
}

func weixinApprovedToolAnswer(call app.ToolCall) string {
	if !isDocumentMutationToolName(call.Tool) {
		return ""
	}
	result, ok := call.Result.(map[string]any)
	if !ok {
		return ""
	}
	outputPath := cleanWeixinString(result["output_path"])
	if outputPath == "" {
		return ""
	}
	return "修改好的文件：" + outputPath
}

func isDocumentMutationToolName(tool string) bool {
	return strings.HasPrefix(tool, "docx.") ||
		strings.HasPrefix(tool, "pptx.") ||
		strings.HasPrefix(tool, "xlsx.") ||
		tool == "office.replace_text" ||
		tool == "pdf.transform"
}

func cleanWeixinString(value any) string {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return ""
	}
	return text
}

func intLikeWeixinValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		var out int
		_, _ = fmt.Sscanf(fmt.Sprint(value), "%d", &out)
		return out
	}
}

func trimWeixinText(value string, limit int) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	runes := []rune(value)
	if limit <= 0 || len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
