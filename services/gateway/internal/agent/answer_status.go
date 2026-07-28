package agent

import "strings"

// These fragments are the single source of truth shared by every builder of a
// blocked/failed final answer and by isBlockedFinalAnswer below. The runtime
// classifies run completion by matching the final-answer text, so a builder
// must never restate one of these fragments as a fresh literal: editing the
// copy in one place but not the other would silently reclassify a failed run
// as a successful answer.
const (
	blockedAnswerTaskIncomplete   = "任务没有完成"
	blockedAnswerCannotComplete   = "无法完成"
	blockedAnswerCouldNotContinue = "I could not continue"
	blockedAnswerStepLimit        = "Reached the workflow step limit"
	blockedAnswerWaitingApproval  = "waiting for approval"
	blockedAnswerPendingApproval  = "pending approval"
)

// retiredLegacyRunMessage closes persisted runs that predate the workflow
// runtime; the generic ReAct loop they would resume into no longer exists.
const retiredLegacyRunMessage = blockedAnswerCannotComplete +
	"继续这次旧的运行:它由已下线的通用执行循环创建。请重新发送你的请求,让它通过当前的 workflow 运行时执行。"

func isBlockedFinalAnswer(answer string) bool {
	answer = strings.TrimSpace(answer)
	lower := strings.ToLower(answer)
	return strings.HasPrefix(answer, blockedAnswerCouldNotContinue) ||
		strings.HasPrefix(answer, blockedAnswerStepLimit) ||
		strings.HasPrefix(answer, blockedAnswerTaskIncomplete) ||
		strings.HasPrefix(answer, blockedAnswerCannotComplete) ||
		strings.Contains(lower, blockedAnswerWaitingApproval) ||
		strings.Contains(lower, blockedAnswerPendingApproval)
}
