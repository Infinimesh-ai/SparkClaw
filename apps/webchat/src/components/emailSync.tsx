import { useState } from "react";
import { ChevronDown, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import type { EmailSyncStatus } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { EmailProgress } from "./emailCommon";

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
  async function sync() {
    setBusy("sync");
    setScheduled(false);
    try { const result = await api.syncEmail(mailboxId); setScheduled(result.scheduled); await onRefresh(); }
    catch (reason) { onError(reason); }
    finally { setBusy(""); }
  }
  async function recover(provider: EmailProviderStatus["provider"], verify: boolean) {
    const mailbox = providerMailbox(status, provider);
    setBusy(provider);
    try {
      if (verify && mailbox) {
        const checked = await api.checkEmailProvider(provider);
        if (checked.state !== "ready") throw new Error(text.email.loginExpired);
        await api.updateEmailIntake(provider, true, mailbox.version);
        const result = await api.syncEmail(mailbox.id);
        setScheduled(result.scheduled);
      } else {
        await api.openEmailLoginBrowser(provider);
      }
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
  return (
    <div className="emailSync">
      <div className="emailSyncBar">
        <button className="emailTextButton" aria-expanded={open} onClick={() => setOpen(!open)}><ChevronDown size={15} />{text.email.receivingSettings}</button>
        <span>{text.email.backlog}: {status?.backlog ?? "—"}</span>
        {scheduled && <span role="status">{text.email.syncScheduled}</span>}
        <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void sync()}><RefreshCw size={14} className={busy === "sync" ? "spin" : ""} />{text.email.sync}</button>
      </div>
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
        </div>)}
      </div>}
    </div>
  );
}
