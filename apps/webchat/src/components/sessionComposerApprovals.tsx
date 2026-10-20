import type { Approval } from "../api/types";
import type { Copy, Language } from "../i18n";
import { ComposerApproval, type ApprovalChoice } from "./composerApproval";
import { ApprovalDetails } from "./panels/approvals";

export function pendingSessionApprovals(approvals: Approval[], sessionID: string) {
  return approvals.filter(approval => sessionID && approval.session_id === sessionID && approval.status === "pending");
}

export function SessionComposerApprovals({ approvals, sessionID, text, language, enabled, onDecide }: {
  approvals: Approval[];
  sessionID: string;
  text: Copy;
  language: Language;
  enabled: boolean;
  onDecide: (approval: Approval, choice: ApprovalChoice) => Promise<void>;
}) {
  const rows = pendingSessionApprovals(approvals, sessionID);
  if (!rows.length) return null;
  return <div className="composerApprovals">{rows.map(approval => {
    const planUnavailable = approval.source === "happy_team_plan" && approval.external_context?.plan_availability !== "available";
    return <ComposerApproval key={`${approval.id}:${JSON.stringify([approval.arguments, approval.external_context, approval.policy_context])}`}
      language={language} summary={approval.summary || approval.tool}
      unavailable={!enabled ? (language === "zh" ? "审批暂时不可用，连接恢复后可继续。" : "Approval is unavailable. Continue when the connection recovers.") : undefined}
      approveUnavailable={planUnavailable ? text.approval.planUnavailable : undefined}
      details={<><strong>{approval.tool}</strong><p>{approval.reason}</p>
        {approval.resources.length > 0 && <ul>{approval.resources.map(resource => <li key={resource}>{resource}</li>)}</ul>}
        <ApprovalDetails approval={approval} text={text} /></>}
      onDecide={choice => onDecide(approval, choice)} />;
  })}</div>;
}
