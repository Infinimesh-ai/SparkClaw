import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { EmailMessage } from "../api/email";
import type { Copy } from "../i18n";

export function EmailBody({ mail, text }: { mail: EmailMessage; text: Copy }) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState<string | null>(mail.body_text ?? null);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open || body !== null) return;
    const controller = new AbortController();
    let active = true;
    setFailed(false);
    void api.emailMessage(mail.id, controller.signal).then((message) => {
      if (active) setBody(message.body_text ?? "");
    }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; controller.abort(); };
  }, [open, mail.id, body, retry]);
  return <details className="emailBody" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>{text.email.body}</summary>
    {open && (body !== null ? <pre>{body}</pre> : failed ? <p role="alert">{text.email.loadFailed}<button className="emailTextButton" onClick={() => setRetry((value) => value + 1)}>{text.common.refresh}</button></p> : <p role="status">{text.email.loading}</p>)}
  </details>;
}
