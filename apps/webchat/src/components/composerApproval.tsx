import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Check, ShieldCheck } from "lucide-react";
import type { Language } from "../i18n";

export type ApprovalChoice = "approve" | "reject";

// The caller keys this component by the original approval and its immutable
// content identity. Choosing an answer never submits a message or grants a task.
export function ComposerApproval({ language, summary, details, unavailable, approveUnavailable, expiresAt, decision, onDecide }: {
  language: Language;
  summary: string;
  details: ReactNode;
  unavailable?: string;
  approveUnavailable?: string;
  expiresAt?: string;
  decision?: ApprovalChoice | null;
  onDecide: (choice: ApprovalChoice) => Promise<void>;
}) {
  const zh = language === "zh";
  const id = useId();
  const [choice, setChoice] = useState<ApprovalChoice | null>(null);
  const [attempted, setAttempted] = useState<ApprovalChoice | null>(null);
  const [sending, setSending] = useState(false);
  const [received, setReceived] = useState(false);
  const [error, setError] = useState("");
  const inFlight = useRef(false);
  const [now, setNow] = useState(Date.now);
  const deadline = expiresAt === undefined ? undefined : Date.parse(expiresAt);
  const expired = deadline !== undefined && (!Number.isFinite(deadline) || deadline <= Math.max(now, Date.now()));
  const lockedChoice = decision || attempted;
  const selected = lockedChoice || choice;
  const disabled = Boolean(unavailable || expired || sending || received);
  useEffect(() => {
    if (deadline === undefined || !Number.isFinite(deadline)) return;
    if (deadline <= Date.now()) { if (now < deadline) setNow(Date.now()); return; }
    const timer = window.setTimeout(() => setNow(Date.now()), Math.min(deadline - Date.now() + 1, 2_147_483_647));
    return () => window.clearTimeout(timer);
  }, [deadline, now]);

  async function submit() {
    if (!selected || disabled || (selected === "approve" && approveUnavailable) || inFlight.current) return;
    // Also check at the event boundary in case the tab's timer was suspended.
    if (deadline !== undefined && (!Number.isFinite(deadline) || deadline <= Date.now())) {
      setNow(Date.now()); return;
    }
    inFlight.current = true; setSending(true); setAttempted(selected); setError("");
    try { await onDecide(selected); setReceived(true); }
    catch (reason) { setError(reason instanceof Error ? reason.message : (zh ? "提交失败，请重试原决定。" : "Could not submit. Retry the same decision.")); }
    finally { inFlight.current = false; setSending(false); }
  }

  const status = expired ? (zh ? "审批已过期，无法继续操作。" : "Approval expired. It can no longer be acted on.")
    : unavailable || (received ? (zh ? "决定已接收。" : "Decision received.") : "");
  return <section className="composerApproval" aria-labelledby={`${id}-title`}>
    <header><ShieldCheck size={16} /><strong id={`${id}-title`}>{zh ? "需要你的确认" : "Your approval is needed"}</strong></header>
    <p className="composerApprovalQuestion">{summary}</p>
    <details className="composerApprovalDetails"><summary>{zh ? "查看操作详情" : "View action details"}</summary>{details}</details>
    <fieldset disabled={disabled}>
      <legend>{zh ? "是否允许执行此操作？" : "Allow this action?"}</legend>
      {(["approve", "reject"] as const).map((value, index) => <label key={value} className={`composerApprovalOption${selected === value ? " selected" : ""}`}>
        <input type="radio" name={`${id}-choice`} value={value} checked={selected === value}
          disabled={Boolean((lockedChoice && lockedChoice !== value) || (value === "approve" && approveUnavailable))} onChange={() => setChoice(value)} />
        <span className="composerApprovalNumber" aria-hidden="true">{index + 1}</span>
        <span><strong>{value === "approve" ? (zh ? "是" : "Yes") : (zh ? "否" : "No")}</strong>
          <small>{value === "approve" ? (zh ? "允许本次操作" : "Allow this action") : (zh ? "拒绝本次操作" : "Reject this action")}</small></span>
        {selected === value && <Check size={16} aria-hidden="true" />}
      </label>)}
    </fieldset>
    {status && <p className="composerApprovalStatus" role="status">{status}</p>}
    {approveUnavailable && <p className="composerApprovalStatus">{approveUnavailable}</p>}
    {error && <p className="compactError" role="alert">{error}</p>}
    <footer><button type="button" disabled={disabled || !selected || (selected === "approve" && Boolean(approveUnavailable))} onClick={() => void submit()}>
      {sending ? (zh ? "正在提交…" : "Submitting…") : lockedChoice ? (zh ? "重试原决定" : "Retry same decision") : (zh ? "提交" : "Submit")}
    </button></footer>
  </section>;
}
