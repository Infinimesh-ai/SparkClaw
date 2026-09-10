import { useState } from "react";
import type { EmailEntry, EmailMessage, EmailPresentation } from "../api/email";
import type { Copy } from "../i18n";

export function EmailClassificationControls({ mail, presentation, text, busy, onChange }: {
  mail: EmailMessage; presentation?: EmailPresentation; text: Copy; busy: boolean;
  onChange: (mail: EmailMessage, entry: EmailEntry, remember: boolean) => Promise<void>;
}) {
  const [editing, setEditing] = useState<EmailEntry | null>(null);
  const classification = mail.classification;
  const sender = mail.current_sender_address ?? classification?.sender_address;
  const source = classification?.source;
  const origin = source === "manual" ? text.email.classifiedManually : source === "rule" ? text.email.classifiedByRule : source === "model" ? text.email.classifiedByModel : text.email.classificationUncertain;
  const entry = classification?.effective_entry ?? "interaction";
  async function save() {
    if (!editing) return;
    await onChange(mail, editing, Boolean(sender));
    setEditing(null);
  }
  return <section className="emailClassification">
    <span className="emailProgress">{entry === "notification" ? text.email.information : text.email.interactions}</span>
    <details><summary>{text.email.classificationReason}</summary><p>{origin}</p>
      {presentation?.state === "ready" && presentation.explanation && <p>{presentation.explanation}</p>}
      {presentation?.state === "ready" && Boolean(presentation.evidence?.length) && <section><strong>{text.email.sourceEvidence}</strong>{presentation.evidence!.map((e, i) => <blockquote key={`${e.ref}:${i}`}>{e.text}</blockquote>)}</section>}
      {source === "rule" && sender && <p>{sender}</p>}
    </details>
    <button className="emailTextButton" disabled={busy} onClick={() => setEditing(entry === "notification" ? "interaction" : "notification")}>
      {entry === "notification" ? text.email.moveToInteraction : text.email.moveToInformation}
    </button>
    {editing && <div className="emailClassificationEdit">
      <strong>{editing === "notification" ? text.email.moveToInformation : text.email.moveToInteraction}</strong>
      <p>{sender ? <>{text.email.rememberSender}<br /><b>{sender}</b></> : text.email.senderMissing}</p>
      <button className="emailTextButton" disabled={busy} onClick={() => void save().catch(() => {})}>{text.email.saveClassification}</button>
      <button className="emailTextButton" disabled={busy} onClick={() => setEditing(null)}>{text.common.cancel}</button>
    </div>}
  </section>;
}
