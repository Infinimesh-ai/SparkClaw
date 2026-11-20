import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { initialLanguage, type Language } from "../i18n";
import { desktopCapability } from "./capability";
import type { DesktopConnectionStatus } from "./types";
import workbenchMark from "../../../desktop/src/assets/icon.png";
import "./desktop-login.css";

const copy = {
  en: {
    title: "Unlock SparkX", description: "Enter the connection credential issued for this device by your backend.",
    backend: "Backend", deployment: "Deployment", owner: "Owner", certificate: "Certificate SHA-256",
    credential: "Connection credential", credentialHelp: "This single credential contains the pinned backend identity and this device’s access token.", signIn: "Unlock", retry: "Reconnect",
    source: "Get the first credential from the Linux backend’s initialization tool. To add another device, issue one in Settings → Devices & credentials on a signed-in desktop.",
    storage: "The main process stores this credential using the operating system’s secure storage. Local history stays on this device.",
    checking: "Checking connection…", connected: "Connected", locked: "Enter this device’s credential to continue.", reconnecting: "Verifying identity…",
    incomplete_setup: "Enter a connection credential to configure this device.", service_unavailable: "The backend is unavailable. Check the network and reconnect. Your saved credential is retained.",
    invalid_authentication: "This credential was rejected or revoked. Get a new device credential and sign in again.", identity_conflict: "The server certificate or identity differs from the configured backend. Verify the connection description with your administrator.",
    authorization_revoked: "This device’s authorization was revoked. Get a new device credential and sign in again. Reconnecting cannot restore this authorization. Your local history is retained.",
    secure_storage_unavailable: "Secure storage is unavailable. Unlock your operating system keychain or secret service, then restart SparkX. Credentials cannot be saved as plain text.",
    actionFailed: "The connection credential is invalid or the connection could not be updated. Check it and try again.", language: "Language",
  },
  zh: {
    title: "解锁 SparkX", description: "输入后端为这台设备签发的连接凭据。",
    backend: "后端地址", deployment: "部署身份", owner: "Owner", certificate: "证书 SHA-256",
    credential: "连接凭据", credentialHelp: "这一份凭据同时包含已固定的后端身份和本设备的访问令牌。", signIn: "解锁", retry: "重新连接",
    source: "首次凭据由 Linux 后端的初始化工具领取。添加其他设备时，在已登录桌面的“设置 → 设备与凭据”中签发。",
    storage: "主进程通过操作系统安全存储保存凭据，本地历史保留在这台设备。",
    checking: "正在检查连接…", connected: "已连接", locked: "输入这台设备的凭据以继续。", reconnecting: "正在验证身份…",
    incomplete_setup: "输入连接凭据即可配置这台设备。", service_unavailable: "后端暂时不可用。请检查网络后重新连接，已保存的凭据会保留。",
    invalid_authentication: "凭据被拒绝或已撤销。请领取新设备凭据并重新登录。", identity_conflict: "服务证书或身份与配置的后端不符。请向部署用户核实连接说明。",
    authorization_revoked: "此设备授权已撤销。请领取新设备凭据并重新登录，重新连接无法恢复此授权。本地历史仍会保留。",
    secure_storage_unavailable: "安全存储不可用。请解锁系统钥匙串或 Secret Service，再重启 SparkX。凭据不能保存为明文。",
    actionFailed: "连接凭据无效或无法更新连接。请检查后重试。", language: "语言",
  },
} as const;

// The same entry, worded for a device that joins its backend's console
// domain with a SparkClaw access credential (link layer).
const linkCopy = {
  en: {
    description: "Enter the SparkClaw access credential issued for this device on the SparkClaw console.",
    backend: "Backend", deployment: "Domain",
    credential: "Access credential", credentialHelp: "The credential names the console and its domain. This device creates its own key when it joins; the credential works once.",
    source: "Issue a desktop access credential on the SparkClaw console, then paste it here. It expires after a short time.",
    storage: "The device key stays in this app's private storage. Conversations are kept on the backend and cached on this device.",
    locked: "Enter this device's access credential to continue.", incomplete_setup: "Enter a SparkClaw access credential to join this device to your backend.",
    service_unavailable: "The backend is unreachable. Check the network and reconnect. This device stays joined.",
    invalid_authentication: "This device was removed or its credential was rejected. Issue a new access credential on the console and join again.",
    authorization_revoked: "This device was removed from the SparkClaw console. Issue a new access credential and join again. Reconnecting cannot restore it. Cached conversations are retained.",
    actionFailed: "The access credential is invalid, expired or already used, or the console could not be reached. Check it and try again.",
  },
  zh: {
    description: "输入在 SparkClaw 控制台为这台设备签发的访问凭据。",
    backend: "后端", deployment: "域",
    credential: "访问凭据", credentialHelp: "凭据指明控制台及其域。加入时这台设备会生成自己的密钥；凭据只能使用一次。",
    source: "在 SparkClaw 控制台签发桌面端访问凭据，然后粘贴到这里。凭据会在短时间后过期。",
    storage: "设备密钥保存在应用的私有存储中。对话保存在后端，这台设备只做缓存。",
    locked: "输入这台设备的访问凭据以继续。", incomplete_setup: "输入 SparkClaw 访问凭据，让这台设备加入你的后端。",
    service_unavailable: "后端暂时无法连接。请检查网络后重新连接，这台设备仍保持加入状态。",
    invalid_authentication: "此设备已被移除或凭据被拒绝。请在控制台签发新的访问凭据并重新加入。",
    authorization_revoked: "此设备已从 SparkClaw 控制台移除。请签发新的访问凭据并重新加入，重新连接无法恢复。已缓存的对话仍会保留。",
    actionFailed: "访问凭据无效、已过期或已被使用，或无法连接控制台。请检查后重试。",
  },
} as const;

export function DesktopLoginGate({ children }: { children: ReactNode }) {
  const desktop = desktopCapability();
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [status, setStatus] = useState<DesktopConnectionStatus>();
  const [credential, setCredential] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  // A desktop without a configured backend joins through the link layer.
  const link = !status?.backend || status.backend.transport === "link";
  const text = link ? { ...copy[language], ...linkCopy[language] } : copy[language];
  const revoked = status?.state === "invalid_authentication" && status.transport_stage === "authorization_revoked";

  useEffect(() => {
    if (!desktop?.enroll) return;
    let active = true;
    const unsubscribe = desktop.onLocalConnection((value) => { if (active) setStatus(value); });
    void desktop.localConnection().then((value) => { if (active) setStatus(value); }).catch(() => {
      if (active) setStatus({ schema_version: 1, state: "service_unavailable" });
    });
    return () => { active = false; unsubscribe(); };
  }, [desktop]);

  // The host WebChat and explicit qualification fixtures use their own
  // entry point. A saved device may open its local workbench during an outage.
  if (!desktop?.enroll || status?.state === "connected" ||
      (status?.client_id && ["service_unavailable", "reconnecting"].includes(status.state))) return children;

  const action = async (operation: () => Promise<DesktopConnectionStatus>) => {
    setBusy(true); setError("");
    try { setStatus(await operation()); } catch { setError(text.actionFailed); }
    finally { setBusy(false); setCredential(""); }
  };
  const unlock = (event: FormEvent) => {
    event.preventDefault();
    const submitted = credential.trim();
    setCredential("");
    void action(() => desktop.enroll!(submitted));
  };

  return <main className="desktopLogin" aria-busy={busy}>
    <section className="desktopLoginContent" aria-labelledby="desktopLoginTitle">
      <div className="desktopLoginHeading">
        <h1 id="desktopLoginTitle"><img src={workbenchMark} alt="" aria-hidden="true" />{text.title}</h1>
        <select aria-label={text.language} value={language} onChange={(event) => setLanguage(event.target.value as Language)}>
          <option value="zh">简体中文</option><option value="en">English</option>
        </select>
      </div>
      <p>{text.description}</p>
      <p className="desktopLoginStatus" role="status">{revoked ? text.authorization_revoked : status ? text[status.state] : text.checking}</p>
      {status?.authorization_deletion && <p role="status">{status.authorization_deletion.state==='pending' ? (language==='zh'?'删除请求已保存在本机，等待核实后端回执；本机已停止访问。':'Deletion is saved locally and awaits its verified receipt. Access from this device has stopped.') : (language==='zh'?'此设备授权已删除。重新授权需新的显式配置。':'This device authorization was deleted. Reauthorization requires new explicit configuration.')}</p>}
      {status?.authorization_deletion?.state==='pending'&&desktop.deleteAuthorization&&<button type="button" disabled={busy} onClick={()=>void action(()=>desktop.deleteAuthorization!())}>{language==='zh'?'核实删除结果':'Reconcile deletion'}</button>}
      {status?.authorization_deletion?.state==='revoked'&&desktop.checkNewAuthorization&&<button type="button" disabled={busy} onClick={()=>void action(()=>desktop.checkNewAuthorization!())}>{language==='zh'?'检查新安装的授权':'Check newly installed authorization'}</button>}
      {status?.backend && <dl className="desktopLoginIdentity">
        <dt>{text.backend}</dt><dd>{link ? status.backend.host_name || status.backend.deployment_id : status.backend.origin}</dd>
        <dt>{text.deployment}</dt><dd>{link ? status.backend.domain_name || status.backend.domain_id : status.backend.deployment_id}</dd>
        {status.backend.owner_id && <><dt>{text.owner}</dt><dd>{status.backend.owner_id}</dd></>}
        {status.backend.tls_certificate_sha256 && <><dt>{text.certificate}</dt><dd>{status.backend.tls_certificate_sha256}</dd></>}
      </dl>}
      {status?.test_mode ? <button type="button" disabled={busy || revoked} onClick={() => void action(() => desktop.retryLocalConnection())}>{text.retry}</button> : <form onSubmit={unlock}>
        <label htmlFor="desktopCredential">{text.credential}</label>
        <p id="desktopCredentialHelp">{text.credentialHelp}</p>
        <input id="desktopCredential" type="password" aria-describedby="desktopCredentialHelp" value={credential} onChange={(event) => setCredential(event.target.value)} autoComplete="off" spellCheck={false} required maxLength={98_304} disabled={busy || status?.state === "secure_storage_unavailable"} />
        <div className="desktopLoginActions">
          <button type="submit" disabled={busy || !credential.trim() || status?.state === "secure_storage_unavailable"}>{text.signIn}</button>
          <button className="desktopLoginSecondary" type="button" disabled={busy || revoked} onClick={() => void action(() => desktop.retryLocalConnection())}>{text.retry}</button>
        </div>
        <p>{text.source}</p>
      </form>}
      {error && <p className="desktopLoginError" role="alert">{error}</p>}
      <p className="desktopLoginStorage">{text.storage}</p>
    </section>
  </main>;
}
