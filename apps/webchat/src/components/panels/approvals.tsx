import { useState } from "react";
import { Check, Inbox, Pencil, RefreshCw, ShieldCheck, X } from "lucide-react";
import type { Approval, ApprovalPresentation } from "../../api/types";
import type { Copy as CopyText } from "../../i18n";
import { formatState, stripSystemArgs } from "../../lib/format";
import { JsonBlock, RiskPill, SectionHeader } from "./primitives";

export function ApprovalPanel({
  approvals,
  text,
  resolvingId,
  onResolve,
  onModify,
  onModifyPlan
}: {
  approvals: Approval[];
  text: CopyText;
  resolvingId?: string;
  onResolve: (id: string, accepted: boolean) => void;
  onModify: (id: string, args: Record<string, unknown>) => void;
  onModifyPlan: (id: string, plan: string) => void;
}) {
  const [editing, setEditing] = useState("");
  const [draft, setDraft] = useState("");
  const [parseError, setParseError] = useState("");

  function startEdit(approval: Approval) {
    setEditing(approval.id);
    setDraft(approval.source === "happy_team_plan" ? approval.external_context?.plan ?? "" : JSON.stringify(stripSystemArgs(approval.arguments), null, 2));
    setParseError("");
  }

  function saveEdit(approval: Approval) {
    if (approval.source === "happy_team_plan") {
      onModifyPlan(approval.id, draft);
      setEditing("");
      setParseError("");
      return;
    }
    try {
      const parsed = JSON.parse(draft) as Record<string, unknown>;
      onModify(approval.id, parsed);
      setEditing("");
      setParseError("");
    } catch {
      setParseError(text.approval.invalidJson);
    }
  }

  return (
    <div className="panelStack">
      <SectionHeader icon={<Inbox size={17} />} title={text.approval.title} />
      {approvals.length === 0 ? (
        <span className="muted">{text.approval.empty}</span>
      ) : (
        approvals.map((approval) => {
          const { context: happyPlan, available: planAvailable } = happyPlanState(approval);
          const headline = approvalHeadline(approval, text);
          const contextBound = Boolean(approval.policy_context);
          const argumentsLocked = contextBound || Boolean(approval.arguments_immutable);
          const resolving = resolvingId === approval.id;
          return (
          <article className={`approvalItem ${approval.risk}`} key={approval.id}>
            <div className="approvalTop">
              <strong>{headline.title}</strong>
              <RiskPill risk={approval.risk} text={text} />
            </div>
            <p>{headline.reason}</p>
            {headline.showResources && approval.resources.length > 0 && (
              <div className="evalCases">
                {approval.resources.map((resource) => (
                  <span key={resource}>{resource}</span>
                ))}
              </div>
            )}
            <ApprovalDetails approval={approval} text={text} />
            {approval.status === "pending" ? (
              <>
                {!argumentsLocked && editing === approval.id && (
                  <div className="approvalEdit">
                    <textarea value={draft} onChange={(event) => setDraft(event.target.value)} />
                    {parseError && <span className="compactError">{parseError}</span>}
                  </div>
                )}
                <div className="buttonRow">
                  <button className="approve" onClick={() => onResolve(approval.id, true)} title={planAvailable || !happyPlan ? text.common.approve : text.approval.planUnavailable} disabled={resolving || Boolean(happyPlan && !planAvailable)}>
                    {resolving ? <RefreshCw className="spin" size={16} /> : <Check size={16} />}
                  </button>
                  {!argumentsLocked && (
                    <button className="edit" onClick={() => (editing === approval.id ? saveEdit(approval) : startEdit(approval))} title={happyPlan ? text.approval.editPlan : text.approval.editArguments} disabled={resolving || Boolean(happyPlan && !planAvailable)}>
                      <Pencil size={15} />
                    </button>
                  )}
                  <button className="reject" onClick={() => onResolve(approval.id, false)} title={text.common.reject} disabled={resolving}>
                    <X size={16} />
                  </button>
                </div>
              </>
            ) : (
              <span className="resolved">{formatState(approval.status, text)}</span>
            )}
          </article>
          );
        })
      )}
    </div>
  );
}

export function happyPlanState(approval: Approval) {
  const context = approval.source === "happy_team_plan" ? approval.external_context : undefined;
  return { context, available: context?.plan_availability === "available" };
}

// Shared by the approval inbox and the composer so both describe the request
// identically; workspace-data requests replace the raw tool summary.
export function approvalHeadline(approval: Approval, text: CopyText) {
  const workspaceAccess = approval.presentation?.kind === "external_mcp_workspace_data_access";
  return workspaceAccess
    ? { title: text.approval.workspaceDataTitle, reason: text.approval.workspaceDataReason, showResources: false }
    : { title: approval.summary, reason: approval.reason, showResources: true };
}

export function ApprovalDetails({ approval, text }: { approval: Approval; text: CopyText }) {
  const { context: happyPlan, available: planAvailable } = happyPlanState(approval);
  const workspaceAccess = approval.presentation?.kind === "external_mcp_workspace_data_access" ? approval.presentation : undefined;
  if (workspaceAccess) return <WorkspaceApprovalDetails presentation={workspaceAccess} argumentsValue={approval.arguments} text={text} />;
  if (happyPlan) return <div className="happyPlanDetails">
    <span className="approvalSource">{text.approval.happyTeam}</span>
    <div><small>{text.approval.taskTitle}</small><p>{happyPlan.title}</p></div>
    <div><small>{text.approval.taskGoal}</small><p>{happyPlan.goal_prompt}</p></div>
    <div><small>{text.approval.taskPlan}</small>
      {planAvailable ? <pre>{happyPlan.plan ?? ""}</pre> : <p className="compactError">{text.approval.planUnavailable}</p>}
    </div>
  </div>;
  return <JsonBlock value={stripSystemArgs(approval.arguments)} />;
}

function WorkspaceApprovalDetails({
  presentation,
  argumentsValue,
  text
}: {
  presentation: ApprovalPresentation;
  argumentsValue: Record<string, unknown>;
  text: CopyText;
}) {
  return (
    <div className="workspaceApprovalDetails">
      <div className="workspaceApprovalIdentity">
        <ShieldCheck size={16} />
        <span>
          <small>{text.approval.requester}</small>
          <strong>{presentation.requester}</strong>
        </span>
      </div>
      <div>
        <small>{text.approval.requestedData}</small>
        <div className="workspaceApprovalLocators">
          {(presentation.locators ?? []).map((locator, index) => (
            <div key={`${locator.path || locator.name || locator.query || "locator"}-${index}`}>
              <span>{locator.caption || locator.path || locator.name || locator.query}</span>
              {locator.caption && <code>{locator.path || locator.name || locator.query}</code>}
              <em>{text.approval.unverified}</em>
            </div>
          ))}
        </div>
      </div>
      <dl>
        <dt>{text.approval.access}</dt>
        <dd>{workspaceAccessLabel(presentation.access_class, text)}</dd>
        <dt>{text.approval.output}</dt>
        <dd>{workspaceOutputLabel(presentation.output_class, text)}</dd>
        <dt>{text.approval.returnTo}</dt>
        <dd>{workspaceReturnTarget(presentation, text)}</dd>
        <dt>{text.approval.scope}</dt>
        <dd>{text.approval.singleOperation}</dd>
      </dl>
      <details>
        <summary>{text.approval.technicalDetails}</summary>
        <JsonBlock value={stripSystemArgs(argumentsValue)} />
      </details>
    </div>
  );
}

function workspaceAccessLabel(accessClass: string | undefined, text: CopyText) {
  if (accessClass === "workspace_derivative_disclosure") return text.approval.derivativeDisclosure;
  return text.approval.workspaceRead;
}

function workspaceOutputLabel(outputClass: string | undefined, text: CopyText) {
  if (outputClass === "response_media") return text.approval.responseMedia;
  if (outputClass === "document_derivative") return text.approval.documentDerivative;
  if (outputClass === "document_content") return text.approval.documentContent;
  return outputClass || text.common.notSet;
}

function workspaceReturnTarget(presentation: ApprovalPresentation, text: CopyText) {
  if (presentation.return_route.mode === "source") return text.approval.originalMCPConversation;
  if (presentation.return_route.mode === "endpoint") return presentation.return_route.endpoint_id || text.approval.approvedDestination;
  return text.approval.noReturn;
}
