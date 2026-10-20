import { ComposerApproval } from "../components/composerApproval";
import type { ApprovalChoice } from "../components/composerApproval";
import type { LocalApproval, LocalTask } from "./clientStore";
import type { Copy } from "../i18n";
import { stripSystemArgs } from "../lib/format";

export function pendingLocalApprovals(tasks: LocalTask[]) {
  return tasks.filter(task => !task.termination_reason).flatMap(task => (task.approvals ?? [])
    .filter(approval => ["pending", "decision_pending", "decision_unknown"].includes(approval.state))
    .map(approval => ({ task, approval })));
}

export function LocalComposerApprovals({ tasks, text, enabled, onDecide }: {
  tasks: LocalTask[];
  text: Copy;
  enabled: boolean;
  onDecide: (task: LocalTask, approval: LocalApproval, decision: ApprovalChoice) => Promise<void>;
}) {
  const copy = text.approval.composer;
  const rows = pendingLocalApprovals(tasks);
  if (!rows.length) return null;
  return <div className="composerApprovals">{rows.map(({ task, approval }) => {
    const unavailable = approval.state === "decision_unknown" ? copy.uncertain
      : !enabled ? copy.offline
      : !approval.actionable ? copy.unverified
      : undefined;
    return <ComposerApproval key={`${task.request_id}:${approval.approval_id}:${approval.digest}`} text={text} lockOnAttempt
      summary={approval.summary || approval.tool} expiresAt={approval.expires_at} decision={approval.decision} unavailable={unavailable}
      details={<><strong>{approval.tool}</strong><pre>{JSON.stringify(stripSystemArgs(approval.arguments), null, 2)}</pre></>}
      onDecide={decision => onDecide(task, approval, decision)} />;
  })}</div>;
}
