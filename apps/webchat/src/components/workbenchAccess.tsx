import { KeyRound, PlugZap } from "lucide-react";
import { useState } from "react";
import { hasConfiguredAPIToken, localAccessAvailable } from "../api/client";
import type { Copy, Language } from "../i18n";
import { workbenchCopy } from "./workbench";

export function WorkbenchAccess({ text, language, connecting, error, onToken, onLocal, onRetry }: {
  text: Copy;
  language: Language;
  connecting: boolean;
  error: string;
  onToken: (token: string) => Promise<void>;
  onLocal: () => Promise<void>;
  onRetry: () => Promise<void>;
}) {
  const [token, setToken] = useState("");
  const [pairing, setPairing] = useState(false);
  const copy = workbenchCopy[language];
  const fixedToken = hasConfiguredAPIToken();
  return <div className={`connectionNotice ${pairing ? "pairing" : ""}`} role="status">
    <span className="connectionNoticeIcon" aria-hidden="true"><PlugZap size={17} /></span>
    <span className="connectionNoticeCopy">
      <strong>{connecting ? text.auth.checkingAccess : copy.connectGateway}</strong>
      <small>{fixedToken ? text.auth.configuredToken : error || copy.connectGatewayDescription}</small>
    </span>
    {!connecting && <div className="authActions connectionNoticeAuth">
      <button className="connectionNoticeAction" type="button" onClick={() => void onRetry()}>{text.auth.retryConnection}</button>
      {localAccessAvailable() && !fixedToken && <button className="connectionNoticeAction" type="button" onClick={() => void onLocal()}>{text.auth.useLocalAccess}</button>}
      {!fixedToken && (pairing ? <form className="tokenForm" onSubmit={(event) => { event.preventDefault(); void onToken(token.trim()); }}>
        <input aria-label={text.auth.gatewayToken} value={token} onChange={(event) => setToken(event.target.value)} placeholder={text.auth.gatewayToken} type="password" />
        <button type="submit" disabled={!token.trim()} title={text.common.saveToken}><KeyRound size={15} /></button>
      </form> : <button className="connectionNoticeAction" type="button" onClick={() => setPairing(true)}>{copy.pairRuntime}</button>)}
    </div>}
  </div>;
}
