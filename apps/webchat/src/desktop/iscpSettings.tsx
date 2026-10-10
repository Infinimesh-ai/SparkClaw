import { useState } from "react";
import { api } from "../api/client";
import type { Language } from "../i18n";
import type { SettingsAccess } from "../components/panels/settings";
import { surfaceEnabled } from "./capability";
import type { DesktopConnectionStatus } from "./types";

export function iscpSettingsUnavailable(language: Language) {
  return language === "zh" ? "当前连接暂不支持此操作。" : "This action is unavailable through the current connection.";
}

// Transport constraints belong to actions, never to the shared menu or layout.
export function iscpSettingsAccess(connection: DesktopConnectionStatus, language: Language): SettingsAccess {
  const unavailable = iscpSettingsUnavailable(language);
  const credentials = surfaceEnabled(connection, "settings_credentials");
  return {
    connectorsAvailable: surfaceEnabled(connection, "settings_connectors"),
    bindingsUnavailable: unavailable,
    unavailable: {
      clients: unavailable, "tool-policy": unavailable,
      ...(!surfaceEnabled(connection, "settings_connectors") ? { messaging: unavailable } : {}),
      "browser-control": unavailable, "ai-platform-login": unavailable, "external-mcp": unavailable,
      ...(!surfaceEnabled(connection, "mail_settings") ? { "browser-email": unavailable } : {}),
      ...(!credentials ? { info: unavailable, localmind: unavailable } : {}),
    },
    loadIntegrationStatus: async id => {
      const response = await api.integrations();
      const status = response.integrations.find(item => item.id === id);
      if (!status) throw new Error(unavailable);
      return status;
    },
    emailLoginNotice: language === "zh"
      ? "登录操作将在后端专用浏览器打开邮箱页面；请在那里完成登录，然后检查连接。打开页面不会证明已登录。"
      : "Sign-in opens the mailbox in the backend’s dedicated browser. Sign in there, then check the connection. Opening a page does not confirm sign-in.",
  };
}

export function ISCPDeviceAuthorization({ connection, language }: { connection: DesktopConnectionStatus; language: Language }) {
  const zh = language === "zh";
  const [review, setReview] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function remove() {
    if (busy || !window.sparkclawDesktop?.deleteAuthorization) return;
    setBusy(true); setError("");
    try { await window.sparkclawDesktop.deleteAuthorization(); }
    catch (error) { setError(error instanceof Error ? error.message : String(error)); }
    finally { setBusy(false); }
  }
  return <section className="settingsSurfaceSection">
    <h2>{zh ? "此设备授权" : "This device authorization"}</h2>
    <p className="settingsSurfaceHint">{zh ? "此设备授权一直有效，直至手动删除。删除后不能继续调用，历史仍保存在本机。" : "This device remains authorized until you delete its authorization. Deletion stops access and retains local history."}</p>
    {connection.authorization_revision && <p className="settingsSurfaceHint">{zh ? "当前授权版本：" : "Current authorization revision: "}{connection.authorization_revision}</p>}
    {review ? <div role="region" aria-label={zh ? "删除授权确认" : "Authorization deletion confirmation"}>
      <p>{zh ? "确认永久删除此设备的当前授权？完成后需安装新的授权配置才能重新连接。" : "Permanently delete this device’s current authorization? Reconnecting afterward requires newly installed authorization."}</p>
      <div className="buttonRow"><button type="button" className="ghost" disabled={busy} onClick={() => setReview(false)}>{zh ? "取消" : "Cancel"}</button>
        <button type="button" className="reject" disabled={busy} onClick={() => void remove()}>{zh ? "确认删除授权" : "Confirm deletion"}</button></div>
    </div> : <button type="button" className="reject" disabled={busy || !window.sparkclawDesktop?.deleteAuthorization} onClick={() => setReview(true)}>{zh ? "删除此设备授权" : "Delete this device authorization"}</button>}
    {error && <p className="compactError" role="alert">{error}</p>}
  </section>;
}
