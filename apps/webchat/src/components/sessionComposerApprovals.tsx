import type { Approval } from "../api/types";
import type { Copy } from "../i18n";
import { ComposerApproval, type ApprovalChoice } from "./composerApproval";
import { ApprovalDetails, approvalHeadline, happyPlanState } from "./panels/approvals";

export function pendingSessionApprovals(approvals: Approval[], sessionID: string) {
  return approvals.filter(approval => sessionID && approval.session_id === sessionID && approval.status === "pending");
}

export function SessionComposerApprovals({ approvals, sessionID, text, enabled, onDecide }: {
  approvals: Approval[];
  sessionID: string;
  text: Copy;
  enabled: boolean;
  onDecide: (approval: Approval, choice: ApprovalChoice) => Promise<void>;
}) {
  const rows = pendingSessionApprovals(approvals, sessionID);
  if (!rows.length) return null;
  return <div className="composerApprovals">{rows.map(approval => {
    const plan = happyPlanState(approval);
    const headline = approvalHeadline(approval, text);
    return <ComposerApproval key={`${approval.id}:${JSON.stringify([approval.arguments, approval.external_context, approval.policy_context])}`}
      text={text} summary={headline.title || approval.tool}
      unavailable={!enabled ? text.approval.composer.offline : undefined}
      approveUnavailable={plan.context && !plan.available ? text.approval.planUnavailable : undefined}
      details={<><strong>{approval.tool}</strong><p>{headline.reason}</p>
        {headline.showResources && approval.resources.length > 0 && <ul>{approval.resources.map(resource => <li key={resource}>{resource}</li>)}</ul>}
        <ApprovalDetails approval={approval} text={text} /></>}
      onDecide={choice => onDecide(approval, choice)} />;
  })}</div>;
}
