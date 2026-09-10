import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { EmailSenderRule, EmailEntry } from "../api/email";
import type { Copy } from "../i18n";
import { emailErrorLabel } from "./emailCommon";

export function EmailSenderRules({ text }: { text: Copy }) {
  const [rules, setRules] = useState<EmailSenderRule[]>([]);
  const [cursor, setCursor] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState("");
  useEffect(() => {
    const controller = new AbortController();
    api.emailSenderRules(controller.signal).then((r) => { if (!controller.signal.aborted) { setRules(r.rules ?? []); setCursor(r.next_cursor ?? ""); } }).catch((e) => { if (!controller.signal.aborted) setError(e); });
    return () => controller.abort();
  }, []);
  async function update(rule: EmailSenderRule, entry: EmailEntry, enabled: boolean) {
    setBusy(rule.id); setError(null);
    try {
      await api.updateEmailSenderRule(rule.id, { entry, enabled, expected_version: rule.revision, command_key: crypto.randomUUID() });
      const result = await api.emailSenderRules(); setRules(result.rules ?? []); setCursor(result.next_cursor ?? "");
    } catch (e) { setError(e); }
    finally { setBusy(""); }
  }
  async function more() {
    setBusy("page");
    try { const result = await api.emailSenderRules(undefined, cursor); setRules((old) => [...old, ...(result.rules ?? [])]); setCursor(result.next_cursor ?? ""); }
    catch (e) { setError(e); } finally { setBusy(""); }
  }
  return <section className="emailRules" aria-label={text.email.senderRules}>
    <h3>{text.email.senderRules}</h3>
    {error ? <p role="alert">{emailErrorLabel(error, text)}</p> : null}
    {!rules.length && <p>{text.email.noSenderRules}</p>}
    {rules.map((rule) => <div className="emailRule" key={rule.id}><strong>{rule.address}</strong>
      <select aria-label={`${rule.address}: ${text.email.classificationReason}`} value={rule.entry} disabled={Boolean(busy)} onChange={(e) => void update(rule, e.target.value as EmailEntry, rule.enabled)}>
        <option value="interaction">{text.email.interactions}</option><option value="notification">{text.email.information}</option>
      </select>
      <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void update(rule, rule.entry, !rule.enabled)}>{rule.enabled ? text.email.disableRule : text.email.enableRule}</button>
      <small>{rule.enabled ? text.email.ruleEnabled : text.email.ruleDisabled}</small>
    </div>)}
    {cursor && <button className="emailTextButton" disabled={Boolean(busy)} onClick={() => void more()}>{text.email.moreRules}</button>}
  </section>;
}
