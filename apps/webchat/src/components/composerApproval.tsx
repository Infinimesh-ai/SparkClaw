import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import { Check, ShieldCheck } from "lucide-react";
import type { Copy } from "../i18n";

export type ApprovalChoice = "approve" | "reject";

// The caller keys this component by the original approval and its immutable
// content identity. Choosing an answer never submits a message or grants a task.
// lockOnAttempt keeps a failed answer fixed for digest-bound approvals, where
// only the original decision may be retried.
export function ComposerApproval({ text, summary, details, unavailable, approveUnavailable, expiresAt, decision, lockOnAttempt = false, onDecide }: {
  text: Copy;
  summary: string;
  details: ReactNode;
  unavailable?: string;
  approveUnavailable?: string;
  expiresAt?: string;
  decision?: ApprovalChoice | null;
  lockOnAttempt?: boolean;
  onDecide: (choice: ApprovalChoice) => Promise<void>;
}) {
  const copy = text.approval.composer;
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
  const lockedChoice = decision || (lockOnAttempt ? attempted : null);
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
    catch (reason) { setError(reason instanceof Error ? reason.message : copy.submitFailed); }
    finally { inFlight.current = false; setSending(false); }
  }

  const status = expired ? copy.expired : unavailable || (received ? copy.received : "");
  return <section className="composerApproval" aria-labelledby={`${id}-title`}>
    <header><ShieldCheck size={16} /><strong id={`${id}-title`}>{copy.title}</strong></header>
    <p className="composerApprovalQuestion">{summary}</p>
    <details className="composerApprovalDetails"><summary>{copy.viewDetails}</summary>{details}</details>
    <fieldset disabled={disabled}>
      <legend>{copy.question}</legend>
      {(["approve", "reject"] as const).map((value, index) => <label key={value} className={`composerApprovalOption${selected === value ? " selected" : ""}`}>
        <input type="radio" name={`${id}-choice`} value={value} checked={selected === value}
          disabled={Boolean((lockedChoice && lockedChoice !== value) || (value === "approve" && approveUnavailable))}
          aria-describedby={value === "approve" && approveUnavailable ? `${id}-approve-unavailable` : undefined} onChange={() => setChoice(value)} />
        <span className="composerApprovalNumber" aria-hidden="true">{index + 1}</span>
        <span><strong>{value === "approve" ? copy.yes : copy.no}</strong>
          <small>{value === "approve" ? copy.allow : copy.reject}</small></span>
        {selected === value && <Check size={16} aria-hidden="true" />}
      </label>)}
    </fieldset>
    {/* Keep the live region mounted so later status changes are announced. */}
    <p className="composerApprovalStatus" role="status">{status}</p>
    {approveUnavailable && <p className="composerApprovalStatus" id={`${id}-approve-unavailable`}>{approveUnavailable}</p>}
    {error && <p className="compactError" role="alert">{error}</p>}
    <footer><button type="button" disabled={disabled || !selected || (selected === "approve" && Boolean(approveUnavailable))} onClick={() => void submit()}>
      {sending ? copy.submitting : lockedChoice ? copy.retry : copy.submit}
    </button></footer>
  </section>;
}
