import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { initialLanguage, type Language } from "../i18n";
import { desktopCapability } from "./capability";
import type { DesktopBackendDescriptor, DesktopConnectionStatus } from "./types";
import workbenchMark from "../../../desktop/src/assets/icon.png";
import "./desktop-login.css";

const copy = {
  en: {
    title: "Unlock SparkClaw", description: "Confirm your backend identity, then enter the credential issued for this device.",
    backend: "Backend", deployment: "Deployment", owner: "Owner", certificate: "Certificate SHA-256",
    descriptor: "Connection description", descriptorHelp: "Paste the public connection JSON supplied by your backend administrator. It contains the server address and identity, without a token.",
    save: "Confirm backend", change: "Change backend", credential: "Device credential", signIn: "Unlock", retry: "Reconnect",
    source: "Get your first credential on the Linux backend using the initial credential tool. To add another device, use Settings → Devices & credentials on a signed-in device.",
    storage: "The main process stores this credential using the operating system’s secure storage. Local history stays on this device.",
    checking: "Checking connection…", connected: "Connected", locked: "Enter this device’s credential to continue.", reconnecting: "Verifying identity…",
    incomplete_setup: "Confirm the backend connection before signing in.", service_unavailable: "The backend is unavailable. Check the network and reconnect. Your saved credential is retained.",
    invalid_authentication: "This credential was rejected or revoked. Get a new device credential and sign in again.", identity_conflict: "The server certificate or identity differs from the configured backend. Verify the connection description with your administrator.",
    secure_storage_unavailable: "Secure storage is unavailable. Unlock your operating system keychain or secret service, then restart SparkClaw. Credentials cannot be saved as plain text.",
    invalidDescriptor: "The connection description is invalid. Paste the complete public JSON supplied by your administrator.",
    actionFailed: "The connection could not be updated. Check the supplied information and try again.", language: "Language",
  },
  zh: {
    title: "解锁 SparkClaw", description: "确认后端身份，再输入为这台设备签发的凭据。",
    backend: "后端地址", deployment: "部署身份", owner: "Owner", certificate: "证书 SHA-256",
    descriptor: "连接说明", descriptorHelp: "粘贴后端部署用户提供的公开连接 JSON，其中包含服务地址和身份，不含 Token。",
    save: "确认后端", change: "更换后端", credential: "设备凭据", signIn: "解锁", retry: "重新连接",
    source: "首次凭据由 Linux 后端的初始化工具领取。添加其他设备时，在已登录设备的“设置 → 设备与凭据”中签发。",
    storage: "主进程通过操作系统安全存储保存凭据，本地历史保留在这台设备。",
    checking: "正在检查连接…", connected: "已连接", locked: "输入这台设备的凭据以继续。", reconnecting: "正在验证身份…",
    incomplete_setup: "登录前请先确认后端连接。", service_unavailable: "后端暂时不可用。请检查网络后重新连接，已保存的凭据会保留。",
    invalid_authentication: "凭据被拒绝或已撤销。请领取新设备凭据并重新登录。", identity_conflict: "服务证书或身份与配置的后端不符。请向部署用户核实连接说明。",
    secure_storage_unavailable: "安全存储不可用。请解锁系统钥匙串或 Secret Service，再重启 SparkClaw。凭据不能保存为明文。",
    invalidDescriptor: "连接说明无效。请粘贴部署用户提供的完整公开 JSON。", actionFailed: "无法更新连接。请检查提供的信息后重试。", language: "语言",
  },
} as const;

export function DesktopLoginGate({ children }: { children: ReactNode }) {
  const desktop = desktopCapability();
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [status, setStatus] = useState<DesktopConnectionStatus>();
  const [token, setToken] = useState("");
  const [descriptor, setDescriptor] = useState("");
  const [editing, setEditing] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const text = copy[language];

  useEffect(() => {
    if (!desktop?.login) return;
    let active = true;
    const unsubscribe = desktop.onLocalConnection((value) => { if (active) setStatus(value); });
    void desktop.localConnection().then((value) => { if (active) setStatus(value); }).catch(() => {
      if (active) setStatus({ schema_version: 1, state: "service_unavailable" });
    });
    return () => { active = false; unsubscribe(); };
  }, [desktop]);

  // The legacy web app and pre-R3 qualification fixtures use their existing
  // entry point. A saved device may open its local workbench during an outage.
  if (!desktop?.login || status?.state === "connected" ||
      (status?.client_id && ["service_unavailable", "reconnecting"].includes(status.state))) return children;

  const action = async (operation: () => Promise<DesktopConnectionStatus>) => {
    setBusy(true); setError("");
    try { setStatus(await operation()); } catch { setError(text.actionFailed); }
    finally { setBusy(false); setToken(""); }
  };
  const configure = (event: FormEvent) => {
    event.preventDefault();
    let value: DesktopBackendDescriptor;
    try { value = JSON.parse(descriptor) as DesktopBackendDescriptor; } catch { setError(text.invalidDescriptor); return; }
    if (!desktop.configureBackend) return;
    void action(() => desktop.configureBackend!(value)).then(() => { setDescriptor(""); setEditing(false); });
  };
  const unlock = (event: FormEvent) => {
    event.preventDefault();
    const submitted = token;
    setToken("");
    void action(() => desktop.login!(submitted));
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
      <p className="desktopLoginStatus" role="status">{status ? text[status.state] : text.checking}</p>
      {status?.backend && !editing ? <>
        <dl className="desktopLoginIdentity">
          <dt>{text.backend}</dt><dd>{status.backend.origin}</dd>
          <dt>{text.deployment}</dt><dd>{status.backend.deployment_id}</dd>
          {status.backend.owner_id && <><dt>{text.owner}</dt><dd>{status.backend.owner_id}</dd></>}
          {status.backend.tls_certificate_sha256 && <><dt>{text.certificate}</dt><dd>{status.backend.tls_certificate_sha256}</dd></>}
        </dl>
        <button className="desktopLoginSecondary" type="button" disabled={busy} onClick={() => setEditing(true)}>{text.change}</button>
      </> : <form onSubmit={configure}>
        <label htmlFor="desktopConnectionDescription">{text.descriptor}</label>
        <p id="desktopConnectionHelp">{text.descriptorHelp}</p>
        <textarea id="desktopConnectionDescription" aria-describedby="desktopConnectionHelp" value={descriptor} onChange={(event) => setDescriptor(event.target.value)} rows={5} required spellCheck={false} autoComplete="off" />
        <button type="submit" disabled={busy || !descriptor.trim()}>{text.save}</button>
      </form>}
      {status?.backend && !editing && <form onSubmit={unlock}>
        <label htmlFor="desktopCredential">{text.credential}</label>
        <input id="desktopCredential" type="password" value={token} onChange={(event) => setToken(event.target.value)} autoComplete="off" spellCheck={false} required maxLength={512} disabled={busy || status.state === "secure_storage_unavailable"} />
        <div className="desktopLoginActions">
          <button type="submit" disabled={busy || !token || status.state === "secure_storage_unavailable"}>{text.signIn}</button>
          <button className="desktopLoginSecondary" type="button" disabled={busy} onClick={() => void action(() => desktop.retryLocalConnection())}>{text.retry}</button>
        </div>
        <p>{text.source}</p>
      </form>}
      {error && <p className="desktopLoginError" role="alert">{error}</p>}
      <p className="desktopLoginStorage">{text.storage}</p>
    </section>
  </main>;
}
