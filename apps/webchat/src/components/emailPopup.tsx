import { useCallback, useEffect, useRef, useState } from "react";
import { ArrowLeft, Mail, Search, Trash2, X } from "lucide-react";
import { api } from "../api/client";
import type { EmailEntry, EmailMessage } from "../api/email";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { useEmailPages } from "../hooks/useEmailPages";
import { useEmailLoginAlert } from "../hooks/useEmailLoginAlert";
import { useEmailStatus } from "../hooks/useEmailStatus";
import { useEmailPresentations } from "../hooks/useEmailPresentations";
import { useEmailViewing } from "../hooks/useEmailViewing";
import { EmailEventAssignment, EmailEventRename } from "./emailEventAssignment";
import { EmailMessageCard } from "./emailMessage";
import { EmailSync } from "./emailSync";
import { EmailProgress, emailErrorLabel, emailEventTitle } from "./emailCommon";
import { EmailCompose, EmailDraftList, type EmailComposeTarget } from "./emailCompose";
import { EmailSenderRules } from "./emailSenderRules";

type Entry = EmailEntry | "pending";
function uniqueAddresses(values: string[]) {
  const seen = new Set<string>();
  return values.map((value) => value.trim()).filter((value) => {
    const key = value.toLocaleLowerCase();
    if (!key || seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
function initialEntry(): Entry {
  return window.localStorage.getItem("sparkclaw.email.entry") === "notification" ? "notification" : "interaction";
}
export function EmailPopupEntry({ text, language }: { text: Copy; language: Language }) {
  const loginRequired = useEmailLoginAlert();
  const [open, setOpen] = useState(false);
  const [selection, setSelection] = useState("");
  return <>
    <button className="iconButton emailEntryButton" title={loginRequired ? text.email.loginExpired : text.email.title} aria-label={text.email.title} aria-describedby={loginRequired ? "email-login-alert" : undefined} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}><Mail size={18} />{loginRequired && <span className="emailLoginDot" aria-hidden="true" />}</button>
    {loginRequired && <span id="email-login-alert" className="emailAlertAccessible" role="status">{text.email.loginExpired}</span>}
    {open && <EmailPopup text={text} language={language} selection={selection} onSelect={setSelection} onClose={() => setOpen(false)} />}
  </>;
}

export function EmailPopup({ text, language, selection, onSelect, onClose }: {
  text: Copy; language: Language; selection: string; onSelect: (id: string) => void; onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const [mailboxId, setMailboxId] = useState("");
  const [query, setQuery] = useState("");
  const [searchDraft, setSearchDraft] = useState("");
  const [entry, setEntry] = useState<Entry>(initialEntry);
  const [rulesOpen, setRulesOpen] = useState(false);
  const [rulesRevision, setRulesRevision] = useState(0);
  const [composeTarget, setComposeTarget] = useState<EmailComposeTarget | null>(null);
  const [draftsOpen, setDraftsOpen] = useState(false);
  const beforeClose = useRef<(() => Promise<boolean>) | null>(null);
  const registerBeforeClose = useCallback((handler: (() => Promise<boolean>) | null) => { beforeClose.current = handler; }, []);
  async function closePopup() { if (!beforeClose.current || await beforeClose.current()) onClose(); }
  async function openComposer(target: EmailComposeTarget) {
    if (beforeClose.current && !await beforeClose.current()) return;
    setComposeTarget({ ...target, instanceId: crypto.randomUUID() });
  }
  const lastSelection = useRef<Partial<Record<Entry, string>>>({ [entry]: selection });
  const [detailVisible, setDetailVisible] = useState(Boolean(selection));
  const [viewport, setViewport] = useState<HTMLDivElement | null>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [busyMail, setBusyMail] = useState("");
  const [reanalyzeQueued, setReanalyzeQueued] = useState(false);
  const [deleteConfirm, setDeleteConfirm] = useState(false);
  const [deletingConversation, setDeletingConversation] = useState(false);
  const filters = `${mailboxId}\n${query}\n${entry}`;
  const pending = entry === "pending";
  const singleId = selection.startsWith("mail:") ? selection.slice(5) : "";
  const conversationId = !pending && !singleId ? selection : "";
  const mailViewKey = pending ? `pending:${filters}` : selection;
  const loadConversations = useCallback(async (cursor: string, signal: AbortSignal) => {
    const result = await api.emailConversations({ mailbox_id: mailboxId, q: query, entry: pending ? undefined : entry, cursor, limit: 30 }, signal);
    return { ...result, items: result.conversations ?? [] };
  }, [mailboxId, query, entry, pending]);
  const conversations = useEmailPages(filters, loadConversations, !pending);
  const loadLoose = useCallback(async (cursor: string, signal: AbortSignal) => {
    const fn = entry === "notification" ? api.emailNotifications : api.emailInteractionMails;
    const result = await fn({ mailbox_id: mailboxId, q: query, cursor, limit: 30, ...(entry === "notification" ? { unassigned_only: true } : {}) }, signal);
    return { ...result, items: result.messages ?? [] };
  }, [mailboxId, query, entry]);
  const loose = useEmailPages(filters, loadLoose, !pending);
  const loadMessages = useCallback(async (cursor: string, signal: AbortSignal) => {
    if (singleId) {
      const mail = await api.emailMessage(singleId, signal);
      return { version: mail.version, items: [mail] };
    }
    const result = pending
      ? await api.emailPending({ mailbox_id: mailboxId, q: query, cursor, limit: 30 }, signal)
      : await api.emailMessages(conversationId, { cursor, limit: 30 }, signal);
    return { ...result, items: result.messages ?? [] };
  }, [mailboxId, query, pending, singleId, conversationId]);
  const mails = useEmailPages<EmailMessage>(mailViewKey, loadMessages, pending || Boolean(selection));
  const overview = useEmailStatus(conversationId);
  const refresh = useCallback(async () => { await Promise.all([conversations.refresh(), loose.refresh(), mails.refresh(), overview.refresh()]); }, [conversations.refresh, loose.refresh, mails.refresh, overview.refresh]);
  const viewed = useEmailViewing(viewport, mails.items.map((mail) => mail.id).join("\n"), () => { void refresh(); }, setActionError);
  const error = actionError || conversations.error || loose.error || mails.error || overview.error;
  const selected = overview.detail;
  const title = emailEventTitle(selected, text);
  const conversationMode = Boolean(selected && !pending && !singleId);
  const displayMails = conversationMode
    ? [...mails.items].sort((left, right) => Date.parse(left.sent_at || left.arrived_at) - Date.parse(right.sent_at || right.arrived_at))
    : mails.items;
  const receivingAddresses = conversationMode ? uniqueAddresses(displayMails.map((mail) => mail.receiving_address)) : [];
  const senderAddresses = conversationMode ? uniqueAddresses(displayMails.map((mail) => mail.from)) : [];
  const addressFallback = mails.loading ? text.email.loading : text.common.notSet;
  const presentations = useEmailPresentations("mail", conversationMode ? displayMails.map((mail) => mail.id) : [], language);
  const replyTarget = conversationMode
    ? [...mails.items].filter((mail) => ["inbound", "received"].includes(mail.direction)).sort((left, right) => Date.parse(right.sent_at || right.arrived_at) - Date.parse(left.sent_at || left.arrived_at))[0]
    : undefined;
  const historyState = conversationMode
    ? (displayMails.some((mail) => mail.history_state === "failed") ? "failed"
      : displayMails.some((mail) => mail.history_state === "pending") ? "pending"
      : displayMails.some((mail) => mail.history_state && mail.history_state !== "complete") ? "partial" : "complete")
    : "complete";

  useEffect(() => {
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const element = dialog.current;
    if (element?.showModal) element.showModal(); else element?.setAttribute("open", "");
    return () => { if (element?.close) element.close(); previous?.focus(); };
  }, []);
  useEffect(() => { if (viewport) viewport.scrollTop = 0; }, [viewport, mailViewKey]);
  useEffect(() => { setDeleteConfirm(false); }, [selection]);
  useEffect(() => {
    if (detailVisible && (pending || selected || singleId)) dialog.current?.querySelector<HTMLElement>(".emailDetailHeader h2")?.focus({ preventScroll: true });
  }, [detailVisible, pending, selected?.id, singleId]);
  function select(id: string) { onSelect(id); lastSelection.current[entry] = id; setDetailVisible(true); setActionError(null); }
  function switchEntry(next: Entry) {
    lastSelection.current[entry] = selection;
    setEntry(next); onSelect(lastSelection.current[next] ?? ""); setDetailVisible(next === "pending"); setActionError(null);
    if (next !== "pending") window.localStorage.setItem("sparkclaw.email.entry", next);
  }
  async function openEvent(id: string, fallback: EmailEntry = "interaction") {
    const result = await api.emailConversation(id);
    const next = result.conversation.effective_entry ?? fallback;
    setEntry(next); lastSelection.current[next] = id; onSelect(id); setDetailVisible(true);
    window.localStorage.setItem("sparkclaw.email.entry", next);
  }
  async function reanalyze(id: string) {
    setBusyMail(id); setActionError(null); setReanalyzeQueued(false);
    try { const result = await api.reanalyzeEmail(id); setReanalyzeQueued(result.scheduled); await refresh(); }
    catch (reason) { setActionError(reason); }
    finally { setBusyMail(""); }
  }
  async function deleteConversation() {
    if (!selected || !conversationMode || deletingConversation) return;
    if (beforeClose.current && !await beforeClose.current()) return;
    setDeletingConversation(true); setActionError(null);
    try {
      await api.deleteEmailConversation(selected.id, { expected_version: selected.version, command_key: crypto.randomUUID() });
      lastSelection.current[entry] = ""; onSelect(""); setDetailVisible(false); setDeleteConfirm(false);
      await Promise.all([conversations.refresh(), loose.refresh()]);
    } catch (reason) { setActionError(reason); }
    finally { setDeletingConversation(false); }
  }
  async function classify(mail: EmailMessage, target: EmailEntry, remember: boolean) {
    setBusyMail(mail.id); setActionError(null);
    try {
      await api.classifyEmail(mail.id, { entry: target, expected_version: mail.classification?.revision ?? 0, remember_sender: remember, expected_rule_version: mail.current_sender_rule_revision ?? 0, command_key: crypto.randomUUID() });
      await refresh();
      setRulesRevision((revision) => revision + 1);
      if (mail.conversation_id) await openEvent(mail.conversation_id, entry === "notification" ? "notification" : "interaction");
      else {
        const nextId = `mail:${mail.id}`;
        setEntry(target); lastSelection.current[target] = nextId; onSelect(nextId); setDetailVisible(true);
        window.localStorage.setItem("sparkclaw.email.entry", target);
      }
    } catch (reason) { setActionError(reason); throw reason; }
    finally { setBusyMail(""); }
  }
  const entryCounts = !pending ? conversations.counts : undefined;
  const sectionLabel = entry === "notification" ? text.email.information : pending ? text.email.pending : text.email.interactions;
  return (
    <dialog ref={dialog} className={`emailPopup ${detailVisible ? "detailVisible" : ""}`} aria-labelledby="email-popup-title" onCancel={(event) => { event.preventDefault(); void closePopup(); }} onKeyDown={(event) => { if (event.key === "Escape") { event.preventDefault(); void closePopup(); } }}>
      <header className="emailPopupHeader"><div><Mail size={22} /><h2 id="email-popup-title">{text.email.title}</h2></div><button className="iconButton" onClick={() => void closePopup()} aria-label={text.common.close} title={text.common.close}><X size={19} /></button></header>
      <form className="emailFilters" onSubmit={(event) => { event.preventDefault(); setQuery(searchDraft.trim()); }}>
        <select aria-label={text.email.receivingAddress} value={mailboxId} onChange={(event) => setMailboxId(event.target.value)}>
          <option value="">{text.email.allAddresses}</option>
          {(overview.status?.mailboxes ?? []).map((mailbox) => <option value={mailbox.id} key={mailbox.id}>{mailbox.address}</option>)}
        </select>
        <label className="emailSearch"><Search size={16} /><input aria-label={text.email.search} placeholder={text.email.search} value={searchDraft} onChange={(event) => setSearchDraft(event.target.value)} /></label>
        <button className="emailTextButton" type="submit">{text.email.searchAction}</button>
        <button className="emailTextButton" type="button" aria-expanded={rulesOpen} onClick={() => setRulesOpen(!rulesOpen)}>{text.email.senderRules}</button>
        <button className="emailTextButton" type="button" onClick={() => void openComposer({ mode: "compose", mailboxId: mailboxId || undefined })}>{text.email.compose}</button>
        <button className="emailTextButton" type="button" aria-expanded={draftsOpen} onClick={() => setDraftsOpen(!draftsOpen)}>{text.email.drafts}</button>
      </form>
      {composeTarget && <EmailCompose key={composeTarget.instanceId ?? composeTarget.draftId ?? `${composeTarget.mode}:${composeTarget.mailId ?? "new"}`} target={composeTarget} language={language} mailboxes={overview.status?.mailboxes ?? []} text={text} onClose={() => setComposeTarget(null)} onBeforeClose={registerBeforeClose} onSent={(draft) => {
        void refresh();
        if (draft.conversation_id) { setEntry("interaction"); onSelect(draft.conversation_id); lastSelection.current.interaction = draft.conversation_id; setDetailVisible(true); }
      }} />}
      {draftsOpen && <EmailDraftList text={text} onSelect={(target) => { void openComposer(target); setDraftsOpen(false); }} />}
      {rulesOpen && <EmailSenderRules key={rulesRevision} text={text} />}
      <EmailSync status={overview.status} providers={overview.providers} mailboxId={mailboxId} text={text} language={language} onRefresh={refresh} onError={setActionError} />
      {error ? <div className="emailError" role="alert">{actionError ? emailErrorLabel(error, text) : text.email.loadFailed}<button className="emailTextButton" onClick={() => { setActionError(null); void refresh(); }}>{text.common.refresh}</button></div> : null}
      {reanalyzeQueued && <div className="emailNotice" role="status">{text.email.reanalysisQueued}</div>}
      <div className={`emailPanes ${detailVisible ? "detailVisible" : ""}`}>
        <nav className="emailCategoryPane" aria-label={text.email.title}>
          <div className="emailCategoryTabs">
            <button className={entry === "interaction" ? "selected" : ""} aria-pressed={entry === "interaction"} onClick={() => switchEntry("interaction")}>{text.email.interactions}</button>
            <button className={entry === "notification" ? "selected" : ""} aria-pressed={entry === "notification"} onClick={() => switchEntry("notification")}>{text.email.information}</button>
            <button className={pending ? "selected" : ""} aria-pressed={pending} onClick={() => switchEntry("pending")}>{text.email.pending} <span>{overview.status?.pending_count ?? 0}</span></button>
          </div>
        </nav>
        <section className="emailConversationPane" aria-label={text.email.conversations}>
          <h3 className="emailListTitle">{sectionLabel}</h3>
          {entryCounts && <p className="emailResultCount">{text.email.matchingMessages}: {entryCounts.total.toLocaleString(language)} · {text.email.unseenMessages}: {entryCounts.unseen.toLocaleString(language)}</p>}
          <div className="emailConversationList" onKeyDown={(event) => {
            if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
            const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>(".emailConversationRow")];
            if (!buttons.length) return;
            const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
            const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1 : (index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
            buttons[next]?.focus(); event.preventDefault();
          }}>
            {!pending && conversations.items.length === 0 && loose.items.length === 0 && <p className="emailEmpty">{conversations.loading || loose.loading ? text.email.loading : text.email.emptyConversations}</p>}
            {!pending && conversations.items.map((conversation) => {
              return <button key={conversation.id} className={`emailConversationRow ${selection === conversation.id ? "selected" : ""}`} aria-current={selection === conversation.id ? "true" : undefined} onClick={() => select(conversation.id)}>
                <span className="emailRowHeading"><strong>{emailEventTitle(conversation, text)}</strong>{conversation.unseen_count > 0 && <span className="emailUnseen">{conversation.unseen_count}</span>}</span>
                <span>{(conversation.participants ?? []).join(", ")}</span>
                <span className="emailRowFooter">{conversation.last_activity_at && <time dateTime={conversation.last_activity_at}>{formatDateTime(conversation.last_activity_at, language)}</time>}{(conversation.concerns ?? []).length > 0 && <span className="emailWarning">{text.email.concern}</span>}</span>
              </button>;
            })}
            {!pending && conversations.nextCursor && <button className="emailLoadMore" disabled={conversations.loading} onClick={() => void conversations.loadMore()}>{text.email.moreConversations}</button>}
            {!pending && loose.items.map((mail) => <button key={mail.id} className={`emailConversationRow ${singleId === mail.id ? "selected" : ""}`} onClick={() => select(`mail:${mail.id}`)}>
              <span className="emailRowHeading"><strong>{mail.subject || text.email.untitled}</strong>{!mail.viewed && <span className="emailUnseen">{text.email.unseen}</span>}</span>
              <span>{mail.from}</span><small>{text.email.originalSubject}: {mail.subject || text.email.untitled}</small><p>{text.email.unassignedInteraction}</p>
              <span>{formatDateTime(mail.sent_at || mail.arrived_at, language)}</span>
            </button>)}
            {!pending && loose.nextCursor && <button className="emailLoadMore" disabled={loose.loading} onClick={() => void loose.loadMore()}>{text.email.moreMessages}</button>}
          </div>
        </section>
        <section className="emailDetailPane" aria-label={selected ? text.email.conversationDetail : sectionLabel}>
          <button className="emailMobileBack emailTextButton" onClick={() => setDetailVisible(false)}><ArrowLeft size={16} />{sectionLabel}</button>
          <div className="emailTimeline" ref={setViewport}>
            {pending ? <header className="emailDetailHeader"><h2 tabIndex={-1}>{text.email.pending}</h2><p>{text.email.pendingHelp}</p></header> : selected ? <header className="emailDetailHeader">
              <h2 tabIndex={-1}>{title}</h2>
              <dl className="emailConversationAddresses">
                <div><dt>{text.email.conversationReceivingEmail}</dt><dd>{receivingAddresses.length ? receivingAddresses.map((address) => <span key={address}>{address}</span>) : addressFallback}</dd></div>
                <div><dt>{text.email.conversationSenderEmail}</dt><dd>{senderAddresses.length ? senderAddresses.map((address) => <span key={address}>{address}</span>) : addressFallback}</dd></div>
              </dl>
              <EmailEventRename key={selected.id} conversation={selected} text={text} onSaved={refresh} onError={setActionError} />
              <div className="emailConversationDelete">
                {!deleteConfirm ? <button className="emailTextButton emailDangerText" type="button" disabled={deletingConversation} onClick={() => setDeleteConfirm(true)}><Trash2 size={14} />{text.email.deleteConversation}</button>
                  : <div className="emailDeleteConfirm" role="alert"><span>{text.email.deleteConversationConfirm}</span><button className="emailDangerButton" type="button" disabled={deletingConversation} onClick={() => void deleteConversation()}>{text.email.deleteConversation}</button><button className="emailTextButton" type="button" disabled={deletingConversation} onClick={() => setDeleteConfirm(false)}>{text.common.cancel}</button></div>}
              </div>
              <EmailProgress state={selected.processing_state} text={text} />
              {historyState !== "complete" && <p className="emailWarning" role="status">{historyState === "pending" ? text.email.historyPending : historyState === "failed" ? text.email.historyFailed : text.email.historyMissing}</p>}
              {selected.historical_mixed && <p className="emailWarning">{text.email.historicalMixed} · {text.email.membershipFixed}</p>}
              {(selected.concerns ?? []).map((concern) => <aside className="emailConcern" key={concern.id}><strong>{concern.kind === "suspected_duplicate" ? text.email.suspectedDuplicate : text.email.pendingCorrection}</strong><p>{text.email.eventCorrectionHelp}</p><small>{text.email.membershipFixed}</small><div>{(concern.related_conversation_ids ?? []).map((id, index) => <button className="emailTextButton" key={id} onClick={() => void openEvent(id).catch(setActionError)}>{text.email.relatedConversation} {index + 1}</button>)}</div></aside>)}
            </header> : singleId ? <header className="emailDetailHeader"><h2 tabIndex={-1}>{sectionLabel}</h2></header> : <p className="emailEmpty">{selection ? text.email.loading : text.email.selectConversation}</p>}
            {displayMails.map((mail) => <div key={mail.id}><EmailMessageCard mail={mail} viewed={mail.viewed || viewed(mail.id)} text={text} language={language} busy={busyMail === mail.id} onReanalyze={(id) => void reanalyze(id)} onError={setActionError} presentation={conversationMode ? presentations.items[mail.id] : undefined} onRetryPresentation={presentations.retry} onClassify={classify} onOpenMail={(id) => select(`mail:${id}`)} onOpenConversation={singleId ? (id) => void openEvent(id).catch(setActionError) : undefined} onReply={conversationMode ? undefined : (mail, all) => void openComposer({ mode: all ? "reply_all" : "reply", mailId: mail.id, mailboxId: mail.mailbox_id })} conversationMode={conversationMode} />{!conversationMode && <EmailEventAssignment mail={mail} text={text} onError={setActionError} onSaved={async (id) => { await refresh(); await openEvent(id); }} />}</div>)}
            {pending && mails.items.length === 0 && <p className="emailEmpty">{mails.loading ? text.email.loading : text.email.emptyPending}</p>}
            {mails.nextCursor && <button className="emailLoadMore" disabled={mails.loading} onClick={() => void mails.loadMore()}>{text.email.moreMessages}</button>}
          </div>
          {replyTarget && !composeTarget && <EmailCompose key={`${selection}:${replyTarget.id}`} target={{ mode: "reply", mailId: replyTarget.id, mailboxId: replyTarget.mailbox_id }} variant="conversation" language={language} mailboxes={overview.status?.mailboxes ?? []} text={text} onClose={() => {}} onBeforeClose={registerBeforeClose} onSent={() => { void refresh(); }} />}
        </section>
      </div>
    </dialog>
  );
}
