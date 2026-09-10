import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { EmailVerification as Verification } from "../api/email";
import type { Copy, Language } from "../i18n";
import { formatDateTime } from "../lib/format";
import { emailErrorLabel } from "./emailCommon";

export function EmailVerification({ mailId, value, text, language, purpose }: { mailId: string; value: Verification; text: Copy; language: Language; purpose?: string }) {
  const [current, setCurrent] = useState(value);
  useEffect(() => { setCurrent(value); }, [value]);
  const [now, setNow] = useState(() => Date.parse(value.server_now));
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState<unknown>(null);
  useEffect(() => {
    const base = Date.parse(current.server_now);
    const start = performance.now();
    const update = () => setNow(base + performance.now() - start);
    update();
    const timer = setInterval(update, 1000);
    return () => clearInterval(timer);
  }, [current.server_now]);
  useEffect(() => {
    const controller = new AbortController();
    let busy = false;
    const refresh = async () => {
      if (busy || document.visibilityState === "hidden") return;
      busy = true;
      try { const mail = await api.emailMessage(mailId, controller.signal); if (!controller.signal.aborted && mail.verification) setCurrent(mail.verification); }
      catch { /* Keep the existing server clock until the next successful refresh. */ }
      finally { busy = false; }
    };
    window.addEventListener("focus", refresh); document.addEventListener("visibilitychange", refresh);
    return () => { controller.abort(); window.removeEventListener("focus", refresh); document.removeEventListener("visibilitychange", refresh); };
  }, [mailId]);
  useEffect(() => { setCode(""); setCopied(false); }, [mailId]);
  const deadline = current.expires_at ? Date.parse(current.expires_at) : NaN;
  const expired = Number.isFinite(deadline) && Number.isFinite(now) ? now >= deadline : current.state === "expired";
  async function reveal() {
    setBusy(true); setError(null);
    try { setCode((await api.emailVerification(mailId)).code ?? ""); }
    catch (reason) { setError(reason); }
    finally { setBusy(false); }
  }
  async function copy() {
    try { await navigator.clipboard.writeText(code); setCopied(true); }
    catch { setError(new Error("copy")); }
  }
  return <section className="emailVerification"><strong>{text.email.verification}</strong>
    {purpose && <p>{purpose}</p>}
    <p>{expired ? text.email.expired : current.state === "validity_unknown" ? text.email.expiryUnknown : text.email.notExpired}</p>
    {current.source_time && <p>{text.email.codeSourceTime}: {formatDateTime(current.source_time, language)}</p>}
    {current.received_at && <p>{text.email.arrivalTime}: {formatDateTime(current.received_at, language)}</p>}
    {current.timing_evidence && <details><summary>{text.email.codeTimingEvidence}</summary><blockquote>{current.timing_evidence}</blockquote></details>}
    {!expired && Number.isFinite(deadline) && Number.isFinite(now) && <p>{text.email.codeTimeRemaining}: {new Intl.NumberFormat(language).format(Math.max(0, Math.ceil((deadline - now) / 1000)))} {text.email.seconds}</p>}
    {current.expires_at && <time dateTime={current.expires_at}>{formatDateTime(current.expires_at, language)}</time>}
    {code ? <div><code>{code}</code><button className="emailTextButton" onClick={() => void copy()}>{text.email.copyCode}</button></div> : current.can_reveal ? <button className="emailTextButton" disabled={busy} onClick={() => void reveal()}>{text.email.revealCode}</button> : <p>{text.email.codeUnavailable}</p>}
    {copied && <p role="status">{text.email.codeCopied}</p>}
    {error ? <p role="alert">{error instanceof Error && error.message === "copy" ? text.email.copyFailed : emailErrorLabel(error, text)}</p> : null}
  </section>;
}
