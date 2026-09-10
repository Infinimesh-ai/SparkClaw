import { useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import type { EmailComposeCapabilities, EmailDraft, EmailMailbox, EmailMessage } from "../api/email";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { emailErrorLabel, emailSendFailureLabel } from "./emailCommon";

export type EmailComposeTarget = { mode: "compose" | "reply" | "reply_all"; mailId?: string; mailboxId?: string; draftId?: string; instanceId?: string };
const splitAddresses = (value: string) => value.split(/[,;\n]/).map((x) => x.trim()).filter(Boolean);

export function EmailCompose({ target, mailboxes, text, language, onClose, onBeforeClose, onSent }: {
  target: EmailComposeTarget; mailboxes: EmailMailbox[]; text: Copy; language: Language; onClose: () => void;
  onBeforeClose: (handler: (() => Promise<boolean>) | null) => void;
  onSent?: (draft: EmailDraft) => void;
}) {
  const [draft, setDraft] = useState<EmailDraft | null>(null);
  const [capabilities, setCapabilities] = useState<EmailComposeCapabilities | null>(null);
  const [mailbox, setMailbox] = useState(target.mailboxId ?? mailboxes.find((m) => m.active_binding)?.id ?? "");
  const [to, setTo] = useState("");
  const [cc, setCC] = useState("");
  const [subject, setSubject] = useState("");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [selectingSource, setSelectingSource] = useState(false);
  const [saved, setSaved] = useState(false);
  const active = useRef(true);
  const executing = useRef(false);
  const sendKey = useRef("");
  const initialId = useRef(crypto.randomUUID());
  function populate(value: EmailDraft) {
    setDraft(value); setMailbox(value.mailbox_id); setTo((value.to ?? []).join(", ")); setCC((value.cc ?? []).join(", ")); setSubject(value.subject); setBody(value.body);
  }
  useEffect(() => {
    active.current = true;
    let mounted = true;
    async function load() {
      try {
        const caps = await api.emailComposeCapabilities();
        if (mounted) setCapabilities(caps);
        if (target.draftId) {
          const value = await api.emailDraft(target.draftId);
          if (mounted) populate(value);
        } else if (target.mode !== "compose") {
          const value = await api.saveEmailDraft({ id: initialId.current, expected_version: 0, mailbox_id: target.mailboxId ?? "", mode: target.mode, reply_mail_id: target.mailId, to: [], cc: [], subject: "", body: "" }).catch(async (reason) => {
            try { return await api.emailDraft(initialId.current); } catch { throw reason; }
          });
          if (mounted) populate(value);
        }
      } catch (reason) { if (mounted) setError(reason); }
      finally { if (mounted) setBusy(false); }
    }
    void load();
    return () => { mounted = false; active.current = false; };
  }, [target]);
  const mode = draft?.mode ?? target.mode;
  const locked = draft && !["draft", "failed"].includes(draft.state);
  const tooManyRecipients = Boolean(capabilities && splitAddresses(to).length + splitAddresses(cc).length > capabilities.max_to);
  const unsupported = tooManyRecipients || !capabilities || !capabilities[mode] || !capabilities.cc && splitAddresses(cc).length > 0 || splitAddresses(to).length > capabilities.max_to;
  async function save(send: boolean) {
    if (executing.current) return false;
    if (locked) return true;
    executing.current = true; setBusy(true); setError(null); setSaved(false);
    try {
      const value = await api.saveEmailDraft({ id: draft?.id ?? initialId.current, expected_version: draft?.version ?? 0, mailbox_id: mailbox, mode, reply_mail_id: draft?.reply_mail_id ?? target.mailId, to: splitAddresses(to), cc: splitAddresses(cc), subject, body });
      if (!active.current) return false;
      populate(value); setSaved(true);
      if (send) {
        sendKey.current = crypto.randomUUID();
        // Freeze the rendered editor immediately before the HTTP request.
        setDraft({ ...value, state: "sending" });
        try {
          const sent = await api.sendEmailDraft(value.id, value.version, sendKey.current);
          if (active.current) { populate(sent); if (sent.state === "sent") onSent?.(sent); }
        } catch (reason) {
          // Request failure does not establish whether the provider sent the message.
          try { const current = await api.emailDraft(value.id); if (active.current) populate(current); }
          catch { if (active.current) setDraft({ ...value, state: "unknown" }); }
          throw reason;
        }
      }
      return true;
    } catch (reason) {
      if (active.current) {
        setError(reason);
        if (!draft) { try { populate(await api.emailDraft(initialId.current)); } catch { /* Preserve local editor for explicit retry. */ } }
      }
      return false;
    }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  async function reconcile(sentMailId?: string) {
    if (!draft || executing.current) return;
    executing.current = true; setBusy(true); setError(null);
    try {
      const result = sentMailId ? await api.reconcileEmailDraft(draft.id, sentMailId) : await api.reconcileEmailDraft(draft.id);
      if (active.current) { populate(result); setSelectingSource(false); if (result.state === "sent") onSent?.(result); }
    } catch (reason) { if (active.current) setError(reason); }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  const closeHandler = useRef<() => Promise<boolean>>(async () => true);
  closeHandler.current = async () => {
    if (locked) return true;
    if (busy || executing.current) return false;
    if (saved || (!draft && !to && !cc && !subject && !body)) return true;
    return save(false);
  };
  useEffect(() => {
    onBeforeClose(() => closeHandler.current());
    return () => onBeforeClose(null);
  }, [onBeforeClose]);
  async function close() { if (await closeHandler.current()) onClose(); }
  return <section className="emailComposer" aria-label={mode === "compose" ? text.email.compose : mode === "reply_all" ? text.email.replyAll : text.email.reply}>
    <header><h3>{mode === "compose" ? text.email.compose : mode === "reply_all" ? text.email.replyAll : text.email.reply}</h3><button className="emailTextButton" disabled={busy && !locked} onClick={() => void close()}>{text.common.close}</button></header>
    <fieldset disabled={busy || Boolean(locked)}>
      <label>{text.email.sendingAddress}<select value={mailbox} onChange={(e) => { setMailbox(e.target.value); setSaved(false); }}>
        {!mailbox && <option value="">{text.email.noSendingAccount}</option>}
        {mailboxes.filter((m) => m.active_binding || m.id === mailbox).map((m) => <option key={m.id} value={m.id}>{m.address}</option>)}
      </select></label>
      <label>{text.email.to}<input value={to} placeholder={text.email.recipientHint} onChange={(e) => { setTo(e.target.value); setSaved(false); }} /></label>
      <label>{text.email.cc}<input value={cc} placeholder={text.email.recipientHint} onChange={(e) => { setCC(e.target.value); setSaved(false); }} /></label>
      <label>{text.email.subject}<input value={subject} required placeholder={text.email.subjectRequired} onChange={(e) => { setSubject(e.target.value); setSaved(false); }} /></label>
      <label>{text.email.body}<textarea rows={8} value={body} placeholder={text.email.bodyPlaceholder} onChange={(e) => { setBody(e.target.value); setSaved(false); }} /></label>
    </fieldset>
    {error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
    {unsupported && !busy && <p className="emailWarning">{tooManyRecipients ? text.email.recipientLimit : text.email.sendUnsupported}</p>}
    {draft?.state === "sent" && <div role="status"><p>{draft.confirmation_source === "owner_confirmed_capture" ? text.email.sendOwnerConfirmed : text.email.sendSucceeded}</p><p>{draft.sent_mail_id ? text.email.sendEvidenceLinked : text.email.sendEvidencePending}</p>
      {draft.conversation_id && onSent && <button className="emailTextButton" onClick={() => onSent(draft)}>{text.email.openSentConversation}</button>}
    </div>}
    {draft && ["sent", "sending", "unknown"].includes(draft.state) && !draft.sent_mail_id && <button className="emailTextButton" disabled={busy} onClick={() => void reconcile()}>{text.email.reconcileSend}</button>}
    {draft && ["sent", "sending", "unknown"].includes(draft.state) && !draft.sent_mail_id && <button className="emailTextButton" disabled={busy} onClick={() => setSelectingSource(!selectingSource)}>{text.email.selectSentSource}</button>}
    {selectingSource && draft && <EmailSentSources mailboxId={draft.mailbox_id} text={text} language={language} disabled={busy} onConfirm={(id) => void reconcile(id)} />}
    {draft && ["sending", "unknown"].includes(draft.state) && <p role="status">{busy ? text.email.sending : text.email.sendUnknown}</p>}
    {draft?.state === "failed" && <p role="alert">{emailSendFailureLabel(draft.error_code, text)}</p>}
    {saved && draft?.state === "draft" && <p role="status">{text.email.draftSaved}</p>}
    <footer><button className="emailTextButton" disabled={busy || Boolean(locked) || !mailbox} onClick={() => void save(false)}>{text.email.saveDraft}</button><button className="emailTextButton" disabled={busy || Boolean(locked) || unsupported || !mailbox || !splitAddresses(to).length || !subject.trim() || !body.trim()} onClick={() => void save(true)}>{text.email.send}</button></footer>
  </section>;
}

export function EmailDraftList({ text, onSelect }: { text: Copy; onSelect: (target: EmailComposeTarget) => void }) {
  const [items, setItems] = useState<EmailDraft[]>([]);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const controller = new AbortController();
    api.emailDrafts({}, controller.signal).then((r) => { if (!controller.signal.aborted) { setItems(r.items ?? []); setCursor(r.next_cursor ?? ""); } }).catch((e) => { if (!controller.signal.aborted) setError(e); }).finally(() => { if (!controller.signal.aborted) setBusy(false); });
    return () => controller.abort();
  }, []);
  async function more() {
    if (busy) return;
    setBusy(true); setError(null);
    try { const r = await api.emailDrafts({ cursor }); setItems((old) => [...old, ...(r.items ?? []).filter((item) => !old.some((previous) => previous.id === item.id))]); setCursor(r.next_cursor ?? ""); }
    catch (e) { setError(e); } finally { setBusy(false); }
  }
  return <section className="emailRules"><h3>{text.email.drafts}</h3>{error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
    {!busy && !items.length && <p>{text.email.emptyDrafts}</p>}
    {items.map((item) => <button className="emailTextButton" key={item.id} onClick={() => onSelect({ mode: item.mode, draftId: item.id })}>{item.subject || text.email.untitled}<small aria-label={text.email.draftState}>{item.state === "sent" ? item.confirmation_source === "owner_confirmed_capture" ? text.email.sendOwnerConfirmed : text.email.sendSucceeded : item.state === "unknown" || item.state === "sending" ? text.email.sendUnknown : item.state === "failed" ? text.email.sendFailed : text.email.draftSaved}</small></button>)}
    {cursor && <button className="emailTextButton" disabled={busy} onClick={() => void more()}>{text.email.moreDrafts}</button>}
  </section>;
}

function EmailSentSources({ mailboxId, text, language, disabled, onConfirm }: { mailboxId: string; text: Copy; language: Language; disabled: boolean; onConfirm: (id: string) => void }) {
  const [items, setItems] = useState<EmailMessage[]>([]);
  const [selected, setSelected] = useState<EmailMessage | null>(null);
  const [cursor, setCursor] = useState("");
  const [query, setQuery] = useState("");
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const controller = new AbortController(); setBusy(true); setError(null); setSelected(null); setItems([]); setCursor("");
    api.emailSentSources({ mailbox_id: mailboxId, q: query, limit: 20 }, controller.signal).then((r) => {
      if (!controller.signal.aborted) { setItems(r.messages ?? []); setCursor(r.next_cursor ?? ""); }
    }).catch((e) => { if (!controller.signal.aborted) setError(e); }).finally(() => { if (!controller.signal.aborted) setBusy(false); });
    return () => controller.abort();
  }, [mailboxId, query]);
  async function more() {
    setBusy(true); setError(null);
    try { const r = await api.emailSentSources({ mailbox_id: mailboxId, q: query, cursor, limit: 20 }); setItems((old) => [...old, ...(r.messages ?? [])]); setCursor(r.next_cursor ?? ""); }
    catch (e) { setError(e); } finally { setBusy(false); }
  }
  return <section className="emailRules" aria-label={text.email.selectSentSource}>
    <p>{text.email.sentSourceHelp}</p>
    <input aria-label={text.email.search} value={query} disabled={busy || disabled} onChange={(e) => setQuery(e.target.value)} />
    {error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
    {!busy && !items.length && <p>{text.email.noSentSources}</p>}
    {items.map((item) => <button className="emailTextButton" disabled={disabled} key={item.id} aria-pressed={selected?.id === item.id} onClick={() => setSelected(item)}>{item.subject || text.email.untitled}<small>{item.to.join(", ")} · {formatDateTime(item.sent_at || item.arrived_at, language)}</small></button>)}
    {cursor && <button className="emailTextButton" disabled={busy || disabled} onClick={() => void more()}>{text.email.moreSentSources}</button>}
    {selected && <div><small>{text.email.originalSubject}</small><h4>{selected.subject}</h4><p>{text.email.to}: {selected.to.join(", ")}</p><p>{text.email.cc}: {selected.cc.join(", ")}</p><pre className="emailOriginalBody">{selected.body_text}</pre><button className="emailTextButton" disabled={busy || disabled} onClick={() => onConfirm(selected.id)}>{text.email.confirmSentSource}</button></div>}
  </section>;
}
