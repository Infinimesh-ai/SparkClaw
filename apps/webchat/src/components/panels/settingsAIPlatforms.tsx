import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { AIPlatform, AIPlatformLoginOverview, AIPlatformLoginState } from "../../api/types";
import type { Copy, Language } from "../../i18n";
import { formatDateTime } from "../../lib/format";
import { integrationStateLabel } from "./settingsIntegrationState";

const platforms: {id: AIPlatform; name: string}[] = [
  {id: "chatgpt", name: "ChatGPT"}, {id: "claude", name: "Claude"},
  {id: "gemini", name: "Gemini"}, {id: "grok", name: "Grok"}
];

export function AIPlatformLoginSettings({text, language}: {text: Copy; language: Language}) {
  const [status, setStatus] = useState<AIPlatformLoginOverview | null>(null);
  const [now, setNow] = useState(Date.now());
  const [busy, setBusy] = useState("");
  const [feedback, setFeedback] = useState("");
  const lock = useRef(false);
  const active = useRef(true);
  useEffect(() => {
    active.current = true;
    api.aiPlatformLogins().then(value => {if (active.current) setStatus(value);})
      .catch(() => {if (active.current) setFeedback(text.settings.aiPlatformLoadFailed);});
    return () => {active.current = false;};
  }, [text.settings.aiPlatformLoadFailed]);
  useEffect(() => {const timer = window.setInterval(() => setNow(Date.now()), 30000);return () => window.clearInterval(timer);}, []);
  const labels: Record<AIPlatformLoginState, string> = {
    unchecked: text.settings.aiPlatformUnchecked, signed_in: text.settings.aiPlatformSignedIn,
    signed_out: text.settings.aiPlatformSignedOut, user_action_required: text.settings.aiPlatformActionRequired,
    unconfirmed: text.settings.aiPlatformUnconfirmed
  };
  async function run(provider: AIPlatform | "all" | "refresh", login = false) {
    if (lock.current) return;
    lock.current = true; setBusy(provider); setFeedback("");
    const errors: string[] = [];
    try {
      if (provider === "refresh") {const value = await api.aiPlatformLogins(); if(active.current) setStatus(value);return;}
      for (const item of provider === "all" ? platforms : platforms.filter(p => p.id === provider)) {
        if (!active.current) break;
        setBusy(item.id);
        try {
          const value = login ? await api.openAIPlatformLogin(item.id) : await api.checkAIPlatformLogin(item.id);
          if (active.current) setStatus(value);
        } catch(reason) {errors.push(`${item.name}: ${reason instanceof Error ? reason.message : text.settings.aiPlatformUnconfirmed}`);}
      }
      // Reload display-only status after errors, retaining the original actionable error.
      if(errors.length) {try {const value = await api.aiPlatformLogins(); if(active.current) setStatus(value);} catch {/* Keep error feedback. */}}
      if(active.current) setFeedback(errors.join("\n") || (login ? text.settings.aiPlatformOpened : ""));
    } catch {if(active.current) setFeedback(text.settings.aiPlatformLoadFailed);}
    finally {lock.current = false;if(active.current) setBusy("");}
  }
  return <div className="integrationDetail" aria-busy={!!busy}>
    <p className="muted">{text.settings.aiPlatformDescription}</p>
    <div className="integrationStatusBar">
      <strong>{text.settings.browserControl}</strong>
      <span>{status ? integrationStateLabel(status.browser_state, text) : text.settings.aiPlatformUnchecked}</span>
      <button type="button" disabled={!!busy} onClick={() => void run("refresh")}>{text.settings.aiPlatformRefresh}</button>
    </div>
    <button type="button" disabled={!!busy || !status} onClick={() => void run("all")}>{text.settings.aiPlatformCheckAll}</button>
    {feedback && <p role="status">{feedback}</p>}
    {platforms.map(p => {
      const value = status?.providers.find(s => s.provider === p.id);
      const stale = value?.checked_at && now - Date.parse(value.checked_at) > 300000;
      return <article className="settingsBlock" key={p.id}>
        <strong>{p.name}</strong>
        <p>{busy === p.id ? text.settings.aiPlatformChecking : labels[stale ? "unchecked" : value?.state || "unchecked"]}</p>
        {value?.checked_at && <p className="muted">{text.settings.aiPlatformRecentCheck}: {formatDateTime(value.checked_at, language)}</p>}
        {value?.error_code && <p>{value.error_code}</p>}
        <div className="row">
          <button type="button" disabled={!!busy || !status} onClick={() => void run(p.id, true)}>{text.settings.aiPlatformOpenLogin}</button>
          <button type="button" disabled={!!busy || !status} onClick={() => void run(p.id)}>{text.settings.aiPlatformCheck}</button>
        </div>
      </article>;
    })}
  </div>;
}
