import { useCallback, useState } from "react";
import { api } from "../api/client";
import type { EmailConversation, EmailMessage } from "../api/email";
import type { Copy } from "../i18n";
import { emailEventTitle } from "./emailCommon";
import { useEmailPages } from "../hooks/useEmailPages";

export function EmailEventAssignment({ mail, text, onSaved, onError }: {
  mail: EmailMessage; text: Copy; onSaved: (id: string) => Promise<void>; onError: (reason: unknown) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [target, setTarget] = useState("");
  const [create, setCreate] = useState(false);
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const load = useCallback(async (cursor: string, signal: AbortSignal) => {
    const result = await api.emailConversations({ q: query, cursor, limit: 30 }, signal);
    return { ...result, items: result.conversations ?? [] };
  }, [query]);
  const choices = useEmailPages(query, load, open && !create);
  async function save() {
    setBusy(true);
    try {
      const result = await api.assignEmailEvent(mail.id, { conversation_id: create ? undefined : target, title: create ? title.trim() : undefined, expected_version: mail.version, command_key: crypto.randomUUID() });
      await onSaved(result.conversation_id); setOpen(false);
    } catch (reason) { onError(reason); } finally { setBusy(false); }
  }
  return <section className="emailEventEdit">
    <button className="emailTextButton" aria-expanded={open} onClick={() => setOpen(!open)}>{text.email.changeEvent}</button>
    {open && <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <p>{text.email.eventCorrectionHelp}</p>
      <label><input type="checkbox" checked={create} disabled={busy} onChange={(event) => setCreate(event.target.checked)} />{text.email.newEvent}</label>
      {create ? <label>{text.email.eventTitle}<input required maxLength={160} value={title} disabled={busy} onChange={(event) => setTitle(event.target.value)} /></label> : <>
        <label>{text.email.search}<input value={query} disabled={busy} onChange={(event) => { setQuery(event.target.value); setTarget(""); }} /></label>
        <label>{text.email.targetEvent}<select required value={target} disabled={busy} onChange={(event) => setTarget(event.target.value)}>
          <option value="">{text.email.selectConversation}</option>
          {choices.items.filter((item) => item.id !== mail.conversation_id).map((item) => <option key={item.id} value={item.id}>{emailEventTitle(item, text)}</option>)}
        </select></label>
        {choices.loading && <p role="status">{text.email.loading}</p>}
        {choices.error ? <p role="alert">{text.email.loadFailed}<button type="button" className="emailTextButton" onClick={() => void choices.refresh()}>{text.common.refresh}</button></p> : null}
        {choices.nextCursor && <button type="button" className="emailTextButton" disabled={choices.loading} onClick={() => void choices.loadMore()}>{text.email.moreConversations}</button>}
      </>}
      <button className="emailTextButton" disabled={busy || (create ? !title.trim() : !target)}>{text.email.saveEvent}</button>
      <button type="button" className="emailTextButton" disabled={busy} onClick={() => setOpen(false)}>{text.common.cancel}</button>
    </form>}
  </section>;
}

export function EmailEventRename({ conversation, text, onSaved, onError }: {
  conversation: EmailConversation; text: Copy; onSaved: () => Promise<void>; onError: (reason: unknown) => void;
}) {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState(conversation.title);
  const [busy, setBusy] = useState(false);
  async function save() {
    setBusy(true);
    try {
      await api.renameEmailEvent(conversation.id, { title: title.trim(), expected_version: conversation.version, command_key: crypto.randomUUID() });
      await onSaved(); setOpen(false);
    } catch (reason) { onError(reason); } finally { setBusy(false); }
  }
  return <div className="emailEventEdit">
    {!open ? <button className="emailTextButton" onClick={() => { setTitle(conversation.title); setOpen(true); }}>{text.email.renameEvent}</button> : <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <label>{text.email.eventTitle}<input value={title} required maxLength={160} disabled={busy} onChange={(event) => setTitle(event.target.value)} /></label>
      <button className="emailTextButton" disabled={busy || !title.trim()}>{text.email.saveEvent}</button>
      <button className="emailTextButton" type="button" disabled={busy} onClick={() => setOpen(false)}>{text.common.cancel}</button>
    </form>}
  </div>;
}
