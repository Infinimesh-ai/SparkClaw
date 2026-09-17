import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { EmailMessage } from "../api/email";
import type { Copy } from "../i18n";

// Reads the parsed preview rather than the captured bytes, so it keeps working
// after the local original is cleaned up, and renders into <pre> so nothing a
// sender wrote is ever interpreted as markup.
export function EmailBody({ mail, text }: { mail: EmailMessage; text: Copy }) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState<string | null>(mail.body_text ?? null);
  const [headers, setHeaders] = useState<string[]>([]);
  const [failed, setFailed] = useState(false);
  const [retry, setRetry] = useState(0);
  useEffect(() => {
    if (!open || body !== null) return;
    const controller = new AbortController();
    let active = true;
    setFailed(false);
    void api.emailPreview(mail.id, controller.signal).then((preview) => {
      if (!active) return;
      setBody(preview.body_text ?? "");
      setHeaders(preview.header_lines ?? []);
    }).catch(() => { if (active) setFailed(true); });
    return () => { active = false; controller.abort(); };
  }, [open, mail.id, body, retry]);
  return <details className="emailBody" onToggle={(event) => setOpen(event.currentTarget.open)}>
    <summary>{text.email.originalPreview}</summary>
    {open && (body !== null
      ? <>{headers.length > 0 && <pre className="emailHeaderLines">{headers.join("\n")}</pre>}<pre>{body}</pre></>
      : failed ? <p role="alert">{text.email.loadFailed}<button className="emailTextButton" onClick={() => setRetry((value) => value + 1)}>{text.common.refresh}</button></p> : <p role="status">{text.email.loading}</p>)}
  </details>;
}
