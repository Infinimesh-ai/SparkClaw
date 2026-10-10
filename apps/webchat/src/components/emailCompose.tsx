import { useEffect, useRef, useState } from "react";
import { Send, Sparkles } from "lucide-react";
import { api, APIError } from "../api/client";
import type { EmailComposeCapabilities, EmailDraft, EmailMailbox, EmailMessage } from "../api/email";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { emailErrorLabel, emailSendFailureLabel } from "./emailCommon";
import { MailAttachmentManifest, WorkspaceMailAttachments } from "../desktop/MailDraftAttachments";
import { useMailWorkspace } from "../desktop/MailWorkspaceContext";
import type { LocalFile } from "../desktop/clientStore";

export type EmailComposeTarget = { mode: "compose" | "reply" | "reply_all"; mailId?: string; mailboxId?: string; draftId?: string; instanceId?: string };
const splitAddresses = (value: string) => value.split(/[,;\n]/).map((x) => x.trim()).filter(Boolean);

export function EmailCompose({ target, mailboxes, text, language, onClose, onBeforeClose, onSent, variant = "full" }: {
  target: EmailComposeTarget; mailboxes: EmailMailbox[]; text: Copy; language: Language; onClose: () => void;
  onBeforeClose: (handler: (() => Promise<boolean>) | null) => void;
  onSent?: (draft: EmailDraft) => void;
  variant?: "full" | "conversation";
}) {
  const workspace = useMailWorkspace();
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
  const [instruction, setInstruction] = useState("");
  const [attachmentIDs, setAttachmentIDs] = useState<string[]>([]);
  const [localFiles, setLocalFiles] = useState<LocalFile[]>([]);
  const [pendingFile, setPendingFile] = useState("");
  const [fileRevision, setFileRevision] = useState(0);
  const [review, setReview] = useState<EmailDraft | null>(null);
  const [requiresReview, setRequiresReview] = useState(false);
  const [reloadRequired, setReloadRequired] = useState(false);
  const active = useRef(true);
  const executing = useRef(false);
  const sendKey = useRef("");
  const initialId = useRef(crypto.randomUUID());
  const actionsEnabled = !workspace || workspace.enabled && workspace.sendEnabled;
  const actionsAllowed = useRef(actionsEnabled);
  actionsAllowed.current = actionsEnabled;
  const previousConnectionEnabled = useRef(workspace?.enabled);
  const attachmentsEnabled = Boolean(workspace?.enabled && workspace.attachmentsEnabled && capabilities?.workspace_attachments);
  function populate(value: EmailDraft) {
    setDraft(value); setMailbox(value.mailbox_id); setTo((value.to ?? []).join(", ")); setCC((value.cc ?? []).join(", ")); setSubject(value.subject); setBody(value.body);
    setAttachmentIDs((value.attachments ?? []).map(item => item.local_file_id || `legacy:${item.path || item.name}`));
    if (value.attachments?.length) setRequiresReview(true);
    setPendingFile(""); setReloadRequired(false);
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
        } else if (target.mode !== "compose" && variant !== "conversation") {
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
  }, [target, variant]);
  useEffect(() => {
    const previous = previousConnectionEnabled.current;
    previousConnectionEnabled.current = workspace?.enabled;
    if (previous === workspace?.enabled || !workspace?.enabled) return;
    const controller = new AbortController();
    void api.emailComposeCapabilities().then(value => {
      if (!controller.signal.aborted) setCapabilities(value);
    }).catch(reason => { if (!controller.signal.aborted) setError(reason); });
    return () => controller.abort();
  }, [workspace?.enabled]);
  const listFiles = workspace?.listFiles;
  useEffect(() => {
    if (!attachmentsEnabled || !listFiles) return;
    let alive = true;
    void listFiles().then(files => { if (alive) setLocalFiles(files); }).catch(reason => { if (alive) setError(reason); });
    return () => { alive = false; };
  }, [attachmentsEnabled, listFiles, fileRevision]);
  const mode = draft?.mode ?? target.mode;
  const hasWorkspaceAttachments = !!draft?.attachments?.length;
  const locked = Boolean(draft && !["draft", "failed"].includes(draft.state));
  const canSave = actionsEnabled && !busy && !locked && !review && !reloadRequired && !pendingFile && !attachmentIDs.some(id => id.startsWith("legacy:")) && (attachmentsEnabled || !attachmentIDs.length);
  const tooManyRecipients = Boolean(capabilities && splitAddresses(to).length + splitAddresses(cc).length > capabilities.max_to);
  const unsupported = tooManyRecipients || !capabilities || !capabilities[mode] || !capabilities.cc && splitAddresses(cc).length > 0 || splitAddresses(to).length > capabilities.max_to;
  function edited() { setSaved(false); setReview(null); }
  function addAttachment() {
    const file = localFiles.find(item => item.id === pendingFile);
    if (!attachmentsEnabled || !file || attachmentIDs.includes(file.id) || attachmentIDs.length >= 5) return;
    setAttachmentIDs(ids => [...ids, file.id]); setPendingFile(""); setRequiresReview(true); edited();
  }
  async function polish() {
    if (!actionsAllowed.current || executing.current || !target.mailId || !instruction.trim()) return;
    executing.current = true; setBusy(true); setError(null); setSaved(false);
    try {
      const value = await api.polishEmailReply({ id: initialId.current, mail_id: target.mailId, instruction: instruction.trim(), language });
      if (active.current) { populate(value); setSaved(true); }
    } catch (reason) { if (active.current) setError(reason); }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  async function save(send: boolean) {
    if (executing.current) return false;
    if (locked) return true;
    if (!canSave || send && unsupported) return false;
    executing.current = true; setBusy(true); setError(null); setSaved(false);
    let persisted = false;
    try {
      const value = await api.saveEmailDraft({ id: draft?.id ?? initialId.current, expected_version: draft?.version ?? 0, mailbox_id: mailbox, mode, reply_mail_id: draft?.reply_mail_id ?? target.mailId, to: splitAddresses(to), cc: splitAddresses(cc), subject, body, ...(workspace || requiresReview ? { attachments: attachmentIDs.map(local_file_id => ({ local_file_id })) } : {}) });
      if (!active.current) return false;
      persisted = true;
      populate(value); setSaved(true);
      if (send) {
        if (requiresReview || value.attachments?.length) setReview(structuredClone(value));
        else if (actionsAllowed.current) await deliver(value);
      }
      return true;
    } catch (reason) {
      if (active.current) {
        setError(reason);
        if (reason instanceof APIError && reason.code === "email_conflict") { setReview(null); setReloadRequired(true); }
        else if (!draft && !persisted) { try { populate(await api.emailDraft(initialId.current)); } catch { /* Preserve local editor for explicit retry. */ } }
      }
      return false;
    }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  async function deliver(value: EmailDraft) {
    sendKey.current = crypto.randomUUID(); setReview(null);
    setDraft({ ...value, state: "sending" });
    try {
      const sent = await api.sendEmailDraft(value.id, value.version, sendKey.current);
      if (active.current) { populate(sent); if (sent.state === "sent") onSent?.(sent); }
    } catch (reason) {
      if (active.current) {
        if (reason instanceof APIError && ["email_conflict", "email_attachment_changed", "email_attachment_invalid"].includes(reason.code)) {
          // These errors precede send intent. Keep local content and require an
          // explicit reload before reviewing another authoritative version.
          setDraft(value); setReloadRequired(true);
        } else {
          // A stale draft read cannot disprove an uncertain send. Only explicit
          // reconciliation of the original send can unlock this message.
          setDraft({ ...value, state: "unknown" });
        }
      }
      throw reason;
    }
  }
  async function confirmSend() {
    const snapshot = review;
    if (!actionsAllowed.current || executing.current || !snapshot || snapshot.id !== draft?.id || snapshot.version !== draft.version || locked || unsupported || snapshot.attachments?.length && !attachmentsEnabled) return;
    executing.current = true; setBusy(true); setError(null);
    try { await deliver(snapshot); }
    catch (reason) { if (active.current) setError(reason); }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  async function reload() {
    if (!actionsAllowed.current || executing.current) return;
    executing.current = true; setBusy(true); setError(null);
    try { const value = await api.emailDraft(draft?.id ?? initialId.current); if (active.current) { populate(value); setReview(null); setSaved(true); } }
    catch (reason) { if (active.current) setError(reason); }
    finally { executing.current = false; if (active.current) setBusy(false); }
  }
  async function reconcile(sentMailId?: string) {
    if (!actionsAllowed.current || !draft || executing.current) return;
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
    if (saved || (!draft && !to && !cc && !subject && !body && !attachmentIDs.length && !pendingFile)) return true;
    return save(false);
  };
  useEffect(() => {
    onBeforeClose(() => closeHandler.current());
    return () => onBeforeClose(null);
  }, [onBeforeClose]);
  async function close() { if (await closeHandler.current()) onClose(); }
  const compact = variant === "conversation";
  if (compact && !draft) {
    return <section className="emailReplyComposer" aria-label={text.email.reply}>
      <header><span className="emailReplyIcon"><Sparkles size={16} /></span><div><h3>{text.email.tellHowToReply}</h3><p>{text.email.replyPolishHelp}</p></div></header>
      <textarea aria-label={text.email.replyIntent} rows={3} value={instruction} placeholder={text.email.replyIntentPlaceholder} disabled={busy} onChange={(event) => setInstruction(event.target.value)} />
      {error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
      {!busy && capabilities && !capabilities.reply && <p className="emailWarning">{text.email.sendUnsupported}</p>}
      <footer><span>{text.email.replyDraftEditable}</span><button className="emailReplyPrimary" disabled={busy || !actionsEnabled || !capabilities?.reply || !instruction.trim()} onClick={() => void polish()}><Sparkles size={15} />{busy ? text.common.running : text.email.polishReply}</button></footer>
    </section>;
  }
  const canSend = canSave && !unsupported && Boolean(mailbox) && splitAddresses(to).length > 0 && Boolean(subject.trim()) && Boolean(body.trim());
  return <section className={compact ? "emailReplyComposer emailReplyDraft" : "emailComposer"} aria-label={mode === "compose" ? text.email.compose : mode === "reply_all" ? text.email.replyAll : text.email.reply}>
    <header><h3>{compact ? text.email.polishedReply : mode === "compose" ? text.email.compose : mode === "reply_all" ? text.email.replyAll : text.email.reply}</h3>{!compact && <button className="emailTextButton" disabled={busy && !locked} onClick={() => void close()}>{text.common.close}</button>}</header>
    <fieldset disabled={busy || locked || !!review}>
      {!compact && <label>{text.email.sendingAddress}<select value={mailbox} onChange={(e) => { setMailbox(e.target.value); edited(); }}>
        {!mailbox && <option value="">{text.email.noSendingAccount}</option>}
        {mailboxes.filter((m) => m.active_binding || m.id === mailbox).map((m) => <option key={m.id} value={m.id}>{m.address}</option>)}
      </select></label>}
      {!compact && <label>{text.email.to}<input value={to} placeholder={text.email.recipientHint} onChange={(e) => { setTo(e.target.value); edited(); }} /></label>}
      {!compact && <label>{text.email.cc}<input value={cc} placeholder={text.email.recipientHint} onChange={(e) => { setCC(e.target.value); edited(); }} /></label>}
      {!compact && <label>{text.email.subject}<input value={subject} required placeholder={text.email.subjectRequired} onChange={(e) => { setSubject(e.target.value); edited(); }} /></label>}
      {compact && <p className="emailReplyRecipient">{text.email.replyRecipient}: {to}</p>}
      <label className={compact ? "emailReplyBody" : undefined}>{compact ? text.email.editPolishedReply : text.email.body}<textarea rows={compact ? 7 : 8} value={body} placeholder={text.email.bodyPlaceholder} onChange={(e) => { setBody(e.target.value); edited(); }} /></label>
      {!locked && (attachmentsEnabled || attachmentIDs.length > 0) && <WorkspaceMailAttachments language={language} ids={attachmentIDs} files={localFiles} saved={draft?.attachments ?? []} pendingFile={pendingFile} onPendingFile={value => { setPendingFile(value); edited(); }} onRefresh={() => setFileRevision(value => value + 1)} onAdd={addAttachment} onRemove={id => { setAttachmentIDs(ids => ids.filter(item => item !== id)); setRequiresReview(true); edited(); }} enabled={attachmentsEnabled}/>}
    </fieldset>
    {review && <section role="region" aria-label={language === "zh" ? "发送确认" : "Send confirmation"}>
      <p>{language === "zh" ? `确认发送版本 ${review.version}` : `Confirm sending version ${review.version}`}</p>
      <p>{review.to.join(", ")}{review.cc.length ? ` · CC: ${review.cc.join(", ")}` : ""}</p>
      <strong>{review.subject}</strong><pre className="emailOriginalBody">{review.body}</pre>
      <MailAttachmentManifest language={language} attachments={review.attachments ?? []}/>
      <button className="emailTextButton" type="button" disabled={busy} onClick={() => setReview(null)}>{language === "zh" ? "返回修改" : "Edit message"}</button>
      <button className="emailTextButton" type="button" disabled={busy || !actionsEnabled || unsupported || (!!review.attachments?.length && !attachmentsEnabled)} onClick={() => void confirmSend()}>{language === "zh" ? "确认发送此版本" : "Confirm sending this version"}</button>
    </section>}
    {!review && hasWorkspaceAttachments && <MailAttachmentManifest language={language} attachments={draft?.attachments ?? []}/>}
    {!actionsEnabled && <p role="status">{language === "zh" ? "邮件操作暂不可用。未保存的编辑保留在本窗口中；连接与授权恢复后可继续。" : "Mail actions are unavailable. Unsaved edits remain in this window until connection and authorization recover."}</p>}
    {reloadRequired && <p role="status">{language === "zh" ? "草稿或附件已变化，此次未发送。请重新读取草稿，再保存并确认当前文件。重新读取会替换未保存的编辑。" : "The draft or its attachments changed; this attempt was not sent. Reload the draft, then save and review the current files. Reloading replaces unsaved edits."} <button className="emailTextButton" type="button" disabled={busy || !actionsEnabled} onClick={() => void reload()}>{language === "zh" ? "重新读取草稿" : "Reload draft"}</button></p>}
    {error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
    {unsupported && !busy && <p className="emailWarning">{tooManyRecipients ? text.email.recipientLimit : text.email.sendUnsupported}</p>}
    {draft?.state === "sent" && <div role="status"><p>{draft.confirmation_source === "owner_confirmed_capture" ? text.email.sendOwnerConfirmed : text.email.sendSucceeded}</p><p>{draft.sent_mail_id ? text.email.sendEvidenceLinked : text.email.sendEvidencePending}</p>
      {draft.conversation_id && onSent && <button className="emailTextButton" onClick={() => onSent(draft)}>{text.email.openSentConversation}</button>}
    </div>}
    {draft && ["sent", "sending", "unknown"].includes(draft.state) && !draft.sent_mail_id && <button className="emailTextButton" disabled={busy || !actionsEnabled} onClick={() => void reconcile()}>{text.email.reconcileSend}</button>}
    {draft && ["sent", "sending", "unknown"].includes(draft.state) && !draft.sent_mail_id && <button className="emailTextButton" disabled={busy || !actionsEnabled} onClick={() => setSelectingSource(!selectingSource)}>{text.email.selectSentSource}</button>}
    {selectingSource && draft && <EmailSentSources mailboxId={draft.mailbox_id} text={text} language={language} disabled={busy || !actionsEnabled} onConfirm={(id) => void reconcile(id)} />}
    {draft && ["sending", "unknown"].includes(draft.state) && <p role="status">{busy ? text.email.sending : text.email.sendUnknown}</p>}
    {draft?.state === "failed" && <p role="alert">{emailSendFailureLabel(draft.error_code, text)}</p>}
    {saved && draft?.state === "draft" && <p role="status">{text.email.draftSaved}</p>}
    {!review && <footer><button className="emailTextButton" disabled={!canSave || !mailbox} onClick={() => void save(false)}>{text.email.saveDraft}</button>{compact ? <button className="emailReplyPrimary" disabled={!canSend} onClick={() => void save(true)}><Send size={15} />{text.email.sendReply}</button> : <button className="emailTextButton" disabled={!canSend} onClick={() => void save(true)}>{text.email.send}</button>}</footer>}
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
