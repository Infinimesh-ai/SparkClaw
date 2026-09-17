import { useState } from "react";
import { ChevronDown, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import type { EmailSyncStatus, EmailCleanupScope, EmailSyncWarning } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { EmailProgress } from "./emailCommon";
import { useEmailRefresh } from "../hooks/useEmailRefresh";

function providerMailbox(status: EmailSyncStatus | null, provider: EmailProviderStatus["provider"]) {
  const mailboxes = (status?.mailboxes ?? []).filter((item) => item.provider === provider);
  return mailboxes.find((item) => item.active_binding) ?? mailboxes.reduce<typeof mailboxes[number] | undefined>(
    (latest, mailbox) => !latest || mailbox.version > latest.version ? mailbox : latest, undefined
  );
}

export function EmailSync({ status, providers, mailboxId, text, language, onRefresh, onError }: {
  status: EmailSyncStatus | null; providers: EmailProviderStatus[]; mailboxId: string;
  text: Copy; language: Language; onRefresh: () => Promise<void>; onError: (reason: unknown) => void;
}) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState("");
  const [scheduled, setScheduled] = useState(false);
  const [cleanupDate, setCleanupDate] = useState("");
  const [cleaned, setCleaned] = useState<number | null>(null);
  const [warningMailbox, setWarningMailbox] = useState("");
  const [warnings, setWarnings] = useState<EmailSyncWarning[]>([]);
  const manualRefresh = useEmailRefresh(status, mailboxId);
  async function sync() {
    setScheduled(false);
    try { const result = await manualRefresh.refresh(); if (result) { setScheduled(result.scheduled); await onRefresh(); } }
    catch (reason) { onError(reason); }
  }
  async function recover(provider: EmailProviderStatus["provider"], verify: boolean) {
    const mailbox = providerMailbox(status, provider);
    setBusy(provider);
    try {
      if (verify && mailbox) {
        const checked = await api.checkEmailProvider(provider);
        if (checked.state !== "ready") throw new Error(text.email.loginExpired);
        await api.updateEmailIntake(provider, true, mailbox.version);
        const result = await manualRefresh.refresh(mailbox.id);
        if (result) setScheduled(result.scheduled);
      } else {
        await api.openEmailLoginBrowser(provider);
      }
      await onRefresh();
    } catch (reason) { onError(reason); await onRefresh(); }
    finally { setBusy(""); }
  }
  async function cleanup(scope: EmailCleanupScope, extra: { mail_id?: string; mailbox_id?: string; date?: string } = {}) {
    setBusy("cleanup"); setCleaned(null);
    try {
      const result = await api.cleanupEmailSource({ scope, command_key: crypto.randomUUID(), ...extra });
      setCleaned(result.purged);
      await onRefresh();
    } catch (reason) { onError(reason); await onRefresh(); }
    finally { setBusy(""); }
  }
  async function intake(provider: EmailProviderStatus) {
    const mailbox = providerMailbox(status, provider.provider);
    setBusy(provider.provider);
    try { await api.updateEmailIntake(provider.provider, !(mailbox?.active_binding && mailbox.intake_enabled), mailbox?.version ?? 0); await onRefresh(); }
    catch (reason) { onError(reason); await onRefresh(); }
    finally { setBusy(""); }
  }
  async function loadWarnings(id: string) {
    setBusy("warnings"); setWarningMailbox(id);
    try { setWarnings((await api.emailSyncWarnings(id)).items); }
    catch (reason) { onError(reason); }
    finally { setBusy(""); }
  }
  async function acknowledge(warning: EmailSyncWarning) {
    setBusy(`warning:${warning.id}`);
    try {
      await api.acknowledgeEmailSyncWarning(warning.mailbox_id, warning.id);
      setWarnings((current) => current.map((item) => item.id === warning.id ? { ...item, acknowledged_at: new Date().toISOString() } : item));
      await onRefresh();
    } catch (reason) { onError(reason); }
    finally { setBusy(""); }
  }
  const unacknowledged = (status?.mailboxes ?? []).reduce((count, mailbox) => count + (mailbox.unacknowledged_warning_count ?? 0), 0);
  return (
    <div className="emailSync">
      <div className="emailSyncBar">
        <button className="emailTextButton" aria-expanded={open} onClick={() => setOpen(!open)}><ChevronDown size={15} />{text.email.receivingSettings}</button>
        <span>{text.email.backlog}: {status?.backlog ?? "—"}</span>
        {scheduled && manualRefresh.pending && <span role="status">{text.email.syncScheduled}</span>}
        <button className="emailTextButton" disabled={Boolean(busy) || !status || manualRefresh.pending} aria-busy={manualRefresh.pending} onClick={() => void sync()}><RefreshCw size={14} className={manualRefresh.pending ? "spin" : ""} />{text.email.sync}</button>
      </div>
      {unacknowledged > 0 && <div className="emailCapacityWarning" role="alert">
        <strong>{text.email.syncWarnings}: {unacknowledged}</strong>
        <span>{text.email.suppressedWarning}</span>
      </div>}
      {status?.capacity && status.capacity.state !== "ok" && status.capacity.state !== "unknown" && <div className="emailCapacityWarning" role="status">
        <span>{status.capacity.state === "critical" ? text.email.capacityCritical : text.email.capacityWarning}</span>
        <small>{text.email.capacityFreeSpace}: {Math.round(status.capacity.free_bytes / (1 << 30))} GB ({status.capacity.used_percent}%)</small>
        {/* Originals are kept permanently and never deleted automatically, so
            the only remedy offered here is a manual cleanup the user chooses. */}
        <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void cleanup("all")}>{text.email.cleanupAll}</button>
      </div>}
      {(status?.mailboxes ?? []).filter((mailbox) => mailbox.intake_enabled && mailbox.state === "login_required").map((mailbox) => <div className="emailLoginRecovery" role="status" key={mailbox.id}>
        <strong>{mailbox.address}</strong><span>{text.email.loginExpired}</span>
        <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void recover(mailbox.provider, false)}>{text.email.signInAgain}</button>
        <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void recover(mailbox.provider, true)}>{text.email.resumeAfterLogin}</button>
        <small>{text.email.loginRecoveryHelp}</small>
      </div>)}
      {open && <div className="emailSyncDetails">
        <p>{text.email.intakeHelp}</p>
        <p>{text.email.oneAccount}</p>
        {providers.map((provider) => {
          const mailbox = providerMailbox(status, provider.provider);
          return <div className="emailIntakeRow" key={provider.provider}>
            <div><strong>{provider.display_name}</strong><small>{mailbox?.address || provider.account_hint || text.email.verifyAccount}</small>
              {!provider.enabled && <small>{text.email.providerSetup}</small>}
              {mailbox && <EmailProgress state={mailbox.state} text={text} />}
            </div>
            <label className="connectorToggle" title={text.email.receive}>
              <input type="checkbox" checked={Boolean(mailbox?.active_binding && mailbox.intake_enabled)} disabled={Boolean(busy) || (!provider.enabled && !(mailbox?.active_binding && mailbox.intake_enabled))} onChange={() => void intake(provider)} aria-label={`${provider.display_name}: ${text.email.receive}`} />
              <span aria-hidden="true" />
            </label>
            {busy === provider.provider && <span role="status">{text.email.verifying}</span>}
          </div>;
        })}
        {(status?.mailboxes ?? []).map((mailbox) => <div className="emailCoverage" key={mailbox.id}>
          <strong>{mailbox.address}</strong> <EmailProgress state={mailbox.state} text={text} />
          {mailbox.last_sync_at && <span>{text.email.lastSync}: {formatDateTime(mailbox.last_sync_at, language)}</span>}
          {(mailbox.coverage_start || mailbox.coverage_end) && <span>{text.email.coverage}: {mailbox.coverage_start ? formatDateTime(mailbox.coverage_start, language) : "—"} → {mailbox.coverage_end ? formatDateTime(mailbox.coverage_end, language) : "—"}</span>}
          {mailbox.gap && <p className="emailWarning">{text.email.coverageGap}: {text.email.needsAttention}</p>}
          {mailbox.error && <p className="emailWarning">{text.email.needsAttention}</p>}
          {mailbox.provider_mode === "unqualified" && <p className="emailWarning">{text.email.incrementalUnqualified}</p>}
          {((mailbox.suppressed_mail_count ?? 0) > 0 || (mailbox.coverage_gap_count ?? 0) > 0) && <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void loadWarnings(mailbox.id)}>{text.email.viewWarnings}</button>}
          <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void cleanup("mailbox", { mailbox_id: mailbox.id })}>{text.email.cleanupMailbox}</button>
        </div>)}
        {warningMailbox && warnings.length > 0 && <div className="emailCleanup" aria-label={text.email.syncWarnings}>
          <strong>{text.email.syncWarnings}</strong>
          {warnings.map((warning) => <div className="emailCoverage" key={warning.id}>
            <span>{warning.state === "coverage_gap" ? text.email.coverageGapWarning : text.email.suppressedWarning}</span>
            <small>{warning.warning_ref} · {warning.error_code} · {formatDateTime(warning.last_attempt_at, language)}</small>
            {warning.acknowledged_at ? <small>{text.email.warningAcknowledged}</small> : <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void acknowledge(warning)}>{text.email.acknowledgeWarning}</button>}
          </div>)}
        </div>}
        {/* Local originals are kept permanently; these are the manual scopes.
            Cleanup removes only the stored copy — mail, body and metadata stay. */}
        <div className="emailCleanup">
          <label>{text.email.cleanupDate}
            <input type="date" value={cleanupDate} disabled={Boolean(busy)} onChange={(event) => setCleanupDate(event.target.value)} />
          </label>
          <button className="emailTextButton" disabled={Boolean(busy) || !cleanupDate} onClick={() => void cleanup("date", { date: cleanupDate })}>{text.email.cleanupDate}</button>
          <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void cleanup("all")}>{text.email.cleanupAll}</button>
          {cleaned !== null && <span role="status">{text.email.cleanupDone}: {cleaned}</span>}
        </div>
      </div>}
    </div>
  );
}
