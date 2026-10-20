import { ComposerApproval } from "../components/composerApproval";
import type { ApprovalChoice } from "../components/composerApproval";
import type { LocalApproval, LocalTask } from "./clientStore";
import type { Language } from "../i18n";

export function pendingLocalApprovals(tasks: LocalTask[]) {
  return tasks.filter(task => !task.termination_reason).flatMap(task => (task.approvals ?? [])
    .filter(approval => ["pending", "decision_pending", "decision_unknown"].includes(approval.state))
    .map(approval => ({ task, approval })));
}

export function LocalComposerApprovals({ tasks, language, enabled, onDecide }: {
  tasks: LocalTask[];
  language: Language;
  enabled: boolean;
  onDecide: (task: LocalTask, approval: LocalApproval, decision: ApprovalChoice) => Promise<void>;
}) {
  const zh = language === "zh";
  const rows = pendingLocalApprovals(tasks);
  if (!rows.length) return null;
  return <div className="composerApprovals">{rows.map(({ task, approval }) => {
    const unavailable = approval.state === "decision_unknown"
      ? (zh ? "决定接收结果不确定，当前不能继续操作。" : "Decision receipt is uncertain. No further action is currently available.")
      : !enabled ? (zh ? "审批暂时不可用，连接恢复后可继续。" : "Approval is unavailable. Continue when the connection recovers.")
      : !approval.actionable ? (zh ? "请等待当前操作核实后再决定。" : "Wait for the current operation to be verified before deciding.")
      : undefined;
    return <ComposerApproval key={`${task.request_id}:${approval.approval_id}:${approval.digest}`} language={language}
      summary={approval.summary || approval.tool} expiresAt={approval.expires_at} decision={approval.decision} unavailable={unavailable}
      details={<><strong>{approval.tool}</strong><pre>{JSON.stringify(approval.arguments, null, 2)}</pre></>}
      onDecide={decision => onDecide(task, approval, decision)} />;
  })}</div>;
}
