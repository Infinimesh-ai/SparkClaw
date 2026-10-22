import { useMailWorkspace } from "../desktop/MailWorkspaceContext";
import { useEffect, useId, useRef, useState } from "react";
import { ChevronDown, RefreshCw } from "lucide-react";
import { api } from "../api/client";
import type { EmailSyncStatus, EmailCleanupScope, EmailSyncWarning, EmailMailbox } from "../api/email";
import type { EmailProviderStatus } from "../api/types";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { EmailProgress, emailMailboxLabel } from "./emailCommon";
import { useEmailRefresh } from "../hooks/useEmailRefresh";
import { newWorkbenchRequestID } from "../lib/workbenchRequest";

type CleanupRequest = {
  scope: EmailCleanupScope;
  extra: { mailbox_id?: string; date?: string };
  label: string;
};

function accountID(account: EmailProviderStatus) { return account.account_id || account.account || "default"; }
function accountKey(account: EmailProviderStatus) { return `${account.provider}:${accountID(account)}`; }
function accountMailbox(status: EmailSyncStatus | null, account: EmailProviderStatus) {
  // A historical/default binding must never be mistaken for another account
  // from the same provider. Old servers omit account_id only for default.
  const mailboxes = (status?.mailboxes ?? []).filter((item) => item.provider === account.provider && (item.account_id || "default") === accountID(account));
  return mailboxes.find((item) => item.active_binding) ?? mailboxes.reduce<typeof mailboxes[number] | undefined>(
    (latest, mailbox) => !latest || mailbox.version > latest.version ? mailbox : latest, undefined
  );
}

export function EmailSync({ status, providers, mailboxId, text, language, onRefresh, onError }: {
  status: EmailSyncStatus | null; providers: EmailProviderStatus[]; mailboxId: string;
  text: Copy; language: Language; onRefresh: () => Promise<void>; onError: (reason: unknown) => void;
}) {
  const workspace = useMailWorkspace();
  const loginEnabled = !workspace || workspace.enabled && workspace.loginEnabled === true;
  const resumeEnabled = !workspace || workspace.enabled && workspace.resumeEnabled === true;
  const checkEnabled = loginEnabled || resumeEnabled;
  const accountManagementEnabled = !workspace || workspace.enabled && workspace.accountsEnabled === true;
  const [open, setOpen] = useState(false);
  const [accounts, setAccounts] = useState<EmailProviderStatus[] | null>(null);
  const [accountsLoading, setAccountsLoading] = useState(false);
  const accountActionBusy = useRef(false);
  const [busy, setBusy] = useState("");
  const [scheduledScope, setScheduledScope] = useState<string | null>(null);
  const [cleanupDate, setCleanupDate] = useState("");
  const [cleaned, setCleaned] = useState<number | null>(null);
  const [cleanupRequest, setCleanupRequest] = useState<CleanupRequest | null>(null);
  const cleanupRequestRef = useRef<CleanupRequest | null>(null);
  const cleanupBusy = useRef(false);
  const cleanupTrigger = useRef<HTMLButtonElement | null>(null);
  const restoreCleanupFocus = useRef(false);
  const cleanupTitleId = useId();
  const cleanupDescriptionId = useId();
  const [warningMailbox, setWarningMailbox] = useState("");
  const [warnings, setWarnings] = useState<EmailSyncWarning[]>([]);
  const manualRefresh = useEmailRefresh(status, mailboxId);
  useEffect(() => {
    if (!open) return;
    let active = true;
    setAccountsLoading(true);
    api.emailAccounts().then((result) => { if (active) setAccounts(result.accounts); })
      .catch(() => { if (active) setAccounts(null); })
      .finally(() => { if (active) setAccountsLoading(false); });
    return () => { active = false; };
  }, [open, workspace?.identity]);
  function rememberAccount(next: EmailProviderStatus) {
    setAccounts((current) => {
      if (!current) return null;
      return current.some((item) => accountKey(item) === accountKey(next))
        ? current.map((item) => accountKey(item) === accountKey(next) ? next : item) : [...current, next];
    });
  }
  const listedAccounts = accounts ?? providers;
  const providerGroups = [...new Map([...providers, ...listedAccounts].map((item) => [item.provider, item])).values()];
  const multiAccountAvailable = (provider: EmailProviderStatus["provider"]) =>
    accountManagementEnabled && listedAccounts.some((item) => item.provider === provider && item.multi_account_available === true);
  function accountOperationsAvailable(account: EmailProviderStatus) {
    return accountID(account) === "default" || multiAccountAvailable(account.provider);
  }
  async function addAccount(provider: EmailProviderStatus) {
    if (busy || accountActionBusy.current || cleanupBusy.current || cleanupRequestRef.current || accountsLoading || !accounts || !loginEnabled || !multiAccountAvailable(provider.provider)) return;
    accountActionBusy.current = true; setBusy(`add:${provider.provider}`);
    try { rememberAccount(await api.addEmailAccount(provider.provider)); await onRefresh(); }
    catch (reason) { onError(reason); }
    finally { accountActionBusy.current = false; setBusy(""); }
  }
  async function accountLogin(account: EmailProviderStatus, check: boolean) {
    if (busy || accountActionBusy.current || cleanupBusy.current || cleanupRequestRef.current || accountsLoading || (check ? !checkEnabled : !loginEnabled) || !accountOperationsAvailable(account) || check && !account.enabled) return;
    accountActionBusy.current = true; setBusy(accountKey(account));
    try {
      let next = account;
      const legacy = accountID(account) === "default";
      // Opening sign-in explicitly enables this account, but never starts
      // receiving. The user verifies its identity before using the switch.
      if (!next.enabled) {
        next = legacy ? await api.updateEmailProvider(next.provider, next.version, { enabled: true })
          : await api.updateEmailAccount(accountID(next), next.version, { enabled: true });
        rememberAccount(next);
      }
      next = legacy ? await (check ? api.checkEmailProvider(next.provider) : api.openEmailLoginBrowser(next.provider))
        : await (check ? api.checkEmailAccount(accountID(next)) : api.openEmailAccountLogin(accountID(next)));
      rememberAccount(next); await onRefresh();
    } catch (reason) { onError(reason); await onRefresh(); }
    finally { accountActionBusy.current = false; setBusy(""); }
  }
  useEffect(() => {
    if (!cleanupRequest && !busy && restoreCleanupFocus.current) {
      restoreCleanupFocus.current = false;
      cleanupTrigger.current?.focus();
    }
  }, [cleanupRequest, busy]);
  async function sync() {
    setScheduledScope(null);
    try { const result = await manualRefresh.refresh(); if (result) { if (result.scheduled) setScheduledScope(mailboxId); await onRefresh(); } }
    catch (reason) { onError(reason); }
  }
  async function recover(mailbox: EmailMailbox, verify: boolean) {
    const id = mailbox.account_id || "default";
    if ((verify ? !resumeEnabled : !loginEnabled) || busy || accountActionBusy.current || id !== "default" && !multiAccountAvailable(mailbox.provider)) return;
    accountActionBusy.current = true;
    setBusy(`${mailbox.provider}:${id}`);
    try {
      if (verify) {
        const checked = id === "default" ? await api.checkEmailProvider(mailbox.provider) : await api.checkEmailAccount(id);
        rememberAccount(checked);
        if (checked.state !== "ready") throw new Error(text.email.loginExpired);
        if (id === "default") await api.updateEmailIntake(mailbox.provider, true, status?.provider_versions?.[mailbox.provider] ?? mailbox.version);
        else await api.updateEmailAccountIntake(id, true, mailbox.version);
        const result = await manualRefresh.refresh(mailbox.id);
        if (result?.scheduled) setScheduledScope(mailbox.id);
      } else {
        rememberAccount(id === "default" ? await api.openEmailLoginBrowser(mailbox.provider) : await api.openEmailAccountLogin(id));
      }
      await onRefresh();
    } catch (reason) { onError(reason); await onRefresh(); }
    finally { accountActionBusy.current = false; setBusy(""); }
  }
  function requestCleanup(trigger: HTMLButtonElement, request: CleanupRequest) {
    if (busy || cleanupBusy.current || cleanupRequestRef.current) return;
    cleanupTrigger.current = trigger;
    cleanupRequestRef.current = request;
    setCleanupRequest(request);
  }
  function cancelCleanup() {
    if (cleanupBusy.current) return;
    cleanupRequestRef.current = null;
    restoreCleanupFocus.current = true;
    setCleanupRequest(null);
  }
  async function cleanup() {
    const request = cleanupRequestRef.current;
    if (!request || cleanupBusy.current || busy) return;
    // Guard synchronously: two clicks can arrive before disabled is rendered.
    cleanupBusy.current = true;
    setBusy("cleanup"); setCleaned(null);
    try {
      const result = await api.cleanupEmailSource({ scope: request.scope, command_key: newWorkbenchRequestID(), ...request.extra });
      setCleaned(result.purged);
      await onRefresh();
    } catch (reason) { onError(reason); await onRefresh(); }
    finally {
      cleanupBusy.current = false;
      cleanupRequestRef.current = null;
      restoreCleanupFocus.current = true;
      setCleanupRequest(null);
      setBusy("");
    }
  }
  async function intake(provider: EmailProviderStatus) {
    if (busy || accountActionBusy.current || cleanupBusy.current || cleanupRequestRef.current || accountsLoading) return;
    const mailbox = accountMailbox(status, provider);
    if (accountID(provider) !== "default" && !accountManagementEnabled) return;
    if (!accountOperationsAvailable(provider) && !(mailbox?.active_binding && mailbox.intake_enabled)) return;
    if (accountID(provider) !== "default" && provider.state !== "ready" && !(mailbox?.active_binding && mailbox.intake_enabled)) return;
    accountActionBusy.current = true; setBusy(accountKey(provider));
    try {
      const enabled = !(mailbox?.active_binding && mailbox.intake_enabled);
      if (accountID(provider) === "default") await api.updateEmailIntake(provider.provider, enabled, status?.provider_versions?.[provider.provider] ?? mailbox?.version ?? 0);
      else await api.updateEmailAccountIntake(accountID(provider), enabled, mailbox?.version ?? 0);
      await onRefresh();
    }
    catch (reason) { onError(reason); await onRefresh(); }
    finally { accountActionBusy.current = false; setBusy(""); }
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
  const selectedMailboxes = (status?.mailboxes ?? []).filter((mailbox) => !mailboxId || mailbox.id === mailboxId);
  const unacknowledged = selectedMailboxes.reduce((count, mailbox) => count + (mailbox.unacknowledged_warning_count ?? 0), 0);
  const intakeActive = selectedMailboxes.some((mailbox) => mailbox.active_binding && mailbox.intake_enabled);
  const latestSync = selectedMailboxes.map((mailbox) => mailbox.last_sync_at).filter((value): value is string => Boolean(value)).sort().at(-1);
  const backlog = mailboxId ? selectedMailboxes[0]?.backlog : status?.backlog;
  const syncScope = mailboxId ? selectedMailboxes[0]?.address ?? text.common.notSet : text.email.allAddresses;
  const cleanupDisabled = Boolean(busy) || Boolean(cleanupRequest);
  return (
    <div className="emailSync" onKeyDownCapture={(event) => {
      if (event.key === "Escape" && cleanupRequestRef.current) {
        event.preventDefault(); event.stopPropagation(); cancelCleanup();
      }
    }}>
      <div className="emailSyncBar">
        <span className={`emailSyncState ${intakeActive ? "active" : ""}`}><i aria-hidden="true" /><button className="emailTextButton" aria-expanded={open} onClick={() => setOpen(!open)}>{text.email.receivingSettings}<ChevronDown size={13} /></button><small>{status ? intakeActive ? text.email.syncConnected : text.email.syncPaused : text.email.syncUnknown}</small></span>
        <span className="emailSyncQueue">{text.email.backlog} <strong>{backlog ?? "—"}</strong></span>
        {scheduledScope === mailboxId && <span role="status">{text.email.syncScheduled}</span>}
        <span className="emailSyncLast">{text.email.lastSync}: {latestSync ? formatDateTime(latestSync, language) : "—"}</span>
        <button className="emailTextButton emailSyncNow" title={`${text.email.syncScope}: ${syncScope}`} disabled={Boolean(busy) || !status || manualRefresh.pending} aria-busy={manualRefresh.pending} onClick={() => void sync()}><RefreshCw size={14} className={manualRefresh.pending ? "spin" : ""} />{text.email.sync}</button>
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
        <button className="emailTextButton emailDangerText" disabled={cleanupDisabled} onClick={(event) => requestCleanup(event.currentTarget, { scope: "all", extra: {}, label: text.email.cleanupAllScope })}>{text.email.cleanupAll}</button>
      </div>}
      {cleanupRequest && <div className="emailDeleteConfirm" role="alertdialog" aria-labelledby={cleanupTitleId} aria-describedby={cleanupDescriptionId}>
        <div>
          <strong id={cleanupTitleId}>{text.email.cleanupConfirmTitle}</strong>
          <p>{cleanupRequest.label}</p>
          <p id={cleanupDescriptionId}>{text.email.cleanupConsequences}</p>
        </div>
        <button className="emailTextButton" type="button" autoFocus disabled={Boolean(busy)} onClick={cancelCleanup}>{text.common.cancel}</button>
        <button className="emailDangerButton" type="button" disabled={Boolean(busy)} aria-busy={busy === "cleanup"} onClick={() => void cleanup()}>{text.email.cleanupConfirmAction}</button>
      </div>}
      {(status?.mailboxes ?? []).filter((mailbox) => mailbox.intake_enabled && mailbox.state === "login_required").map((mailbox) => <div className="emailLoginRecovery" role="status" key={mailbox.id}>
        <strong>{emailMailboxLabel(mailbox)}</strong><span>{text.email.loginExpired}</span>
        {loginEnabled && <button className="emailTextButton" disabled={cleanupDisabled || (mailbox.account_id && mailbox.account_id !== "default" ? !multiAccountAvailable(mailbox.provider) : false)} onClick={() => void recover(mailbox, false)}>{text.email.signInAgain}</button>}
        <button className="emailTextButton" disabled={cleanupDisabled || !resumeEnabled || (mailbox.account_id && mailbox.account_id !== "default" ? !multiAccountAvailable(mailbox.provider) : false)} onClick={() => void recover(mailbox, true)}>{text.email.resumeAfterLogin}</button>
        <small>{workspace?.loginNotice || text.email.loginRecoveryHelp}</small>
      </div>)}
      {open && <div className="emailSyncDetails">
        <p>{text.email.intakeHelp}</p>
        <p>{!providerGroups.length || listedAccounts.some((account) => account.multi_account_available === true) ? text.email.multipleAccountsHelp : text.email.oneAccount}</p>
        {providerGroups.map((provider) => <section className="emailAccountGroup" key={provider.provider} aria-label={provider.display_name}>
          <div className="emailIntakeRow">
            <div><strong>{provider.display_name}</strong>{!multiAccountAvailable(provider.provider) && <small>{accountManagementEnabled ? text.email.additionalAccountUnavailable : text.email.accountManagementUnavailable}</small>}</div>
            <button className="emailTextButton" disabled={cleanupDisabled || accountsLoading || !accounts || !loginEnabled || !multiAccountAvailable(provider.provider)} onClick={() => void addAccount(provider)} aria-label={`${provider.display_name}: ${text.email.addAccount}`}>{text.email.addAccount}</button>
          </div>
          {listedAccounts.filter((account) => account.provider === provider.provider).map((account, index) => {
            const mailbox = accountMailbox(status, account);
            // Outlook sign-in identity is not its verified delivery address.
            const hint = account.provider === "outlook" ? "" : account.account_hint;
            const label = mailbox ? emailMailboxLabel(mailbox) : hint || (accountID(account) === "default" ? account.provider === "outlook" ? provider.display_name : text.email.accountDefault : `${provider.display_name} ${index + 1}`);
            const receiving = Boolean(mailbox?.active_binding && mailbox.intake_enabled);
            const available = accountOperationsAvailable(account);
            return <div className="emailIntakeRow emailAccountRow" key={accountKey(account)}>
              <div><strong>{label}</strong>
                {!(mailbox || hint) && <small>{text.email.accountNotSignedIn}</small>}
                {!account.enabled && accountID(account) === "default" && <small>{text.email.providerSetup}</small>}
                <EmailProgress state={mailbox?.state || (account.state !== "not_configured" ? account.state : undefined)} text={text} />
              </div>
              {loginEnabled && <button className="emailTextButton" disabled={cleanupDisabled || accountsLoading || !available} onClick={() => void accountLogin(account, false)} aria-label={`${label}: ${text.email.accountOpenLogin}`}>{text.email.accountOpenLogin}</button>}
              <button className="emailTextButton" disabled={cleanupDisabled || accountsLoading || !checkEnabled || !available || !account.enabled} onClick={() => void accountLogin(account, true)} aria-label={`${label}: ${text.email.accountCheck}`}>{text.email.accountCheck}</button>
              <label className="connectorToggle" title={mailbox?.address || account.account_hint ? text.email.receive : text.email.verifyAccount}>
                <input type="checkbox" checked={receiving} disabled={cleanupDisabled || accountsLoading || (accountID(account) !== "default" && !accountManagementEnabled) || (!available && !receiving) || (!account.enabled && !receiving) || (accountID(account) !== "default" && account.state !== "ready" && !receiving)} onChange={() => void intake(account)} aria-label={`${label}: ${text.email.accountReceiveLabel}`} />
                <span aria-hidden="true" />
              </label>
              {busy === accountKey(account) && <span role="status">{text.email.verifying}</span>}
            </div>;
          })}
        </section>)}
        {(status?.mailboxes ?? []).map((mailbox) => <div className="emailCoverage" key={mailbox.id}>
          <strong>{emailMailboxLabel(mailbox)}</strong> <EmailProgress state={mailbox.state} text={text} />
          {mailbox.last_sync_at && <span>{text.email.lastSync}: {formatDateTime(mailbox.last_sync_at, language)}</span>}
          {(mailbox.coverage_start || mailbox.coverage_end) && <span>{text.email.coverage}: {mailbox.coverage_start ? formatDateTime(mailbox.coverage_start, language) : "—"} → {mailbox.coverage_end ? formatDateTime(mailbox.coverage_end, language) : "—"}</span>}
          {mailbox.gap && <p className="emailWarning">{text.email.coverageGap}: {text.email.needsAttention}</p>}
          {mailbox.error && <p className="emailWarning">{text.email.needsAttention}</p>}
          {mailbox.provider_mode === "unqualified" && <p className="emailWarning">{text.email.incrementalUnqualified}</p>}
          {((mailbox.suppressed_mail_count ?? 0) > 0 || (mailbox.coverage_gap_count ?? 0) > 0) && <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void loadWarnings(mailbox.id)}>{text.email.viewWarnings}</button>}
          <button className="emailTextButton emailDangerText" disabled={cleanupDisabled} aria-label={`${text.email.cleanupMailbox}: ${mailbox.address}`} onClick={(event) => requestCleanup(event.currentTarget, { scope: "mailbox", extra: { mailbox_id: mailbox.id }, label: `${text.email.cleanupMailboxScope} ${mailbox.address}` })}>{text.email.cleanupMailbox}</button>
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
          <small>{text.email.cleanupAllScopeHint}</small>
          <label>{text.email.cleanupDate}
            <input type="date" value={cleanupDate} disabled={Boolean(busy)} onChange={(event) => setCleanupDate(event.target.value)} />
          </label>
          <button className="emailTextButton emailDangerText" disabled={cleanupDisabled || !cleanupDate} onClick={(event) => requestCleanup(event.currentTarget, { scope: "date", extra: { date: cleanupDate }, label: `${text.email.cleanupDateScope} ${cleanupDate}` })}>{text.email.cleanupDate}</button>
          <button className="emailTextButton emailDangerText" disabled={cleanupDisabled} onClick={(event) => requestCleanup(event.currentTarget, { scope: "all", extra: {}, label: text.email.cleanupAllScope })}>{text.email.cleanupAll}</button>
          {cleaned !== null && <span role="status">{text.email.cleanupDone}: {cleaned}</span>}
        </div>
      </div>}
    </div>
  );
}
