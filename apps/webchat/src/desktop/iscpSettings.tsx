import { useState } from "react";
import { api } from "../api/client";
import type { Language } from "../i18n";
import type { SettingsAccess } from "../components/panels/settings";
import { surfaceEnabled } from "./capability";
import type { DesktopConnectionStatus } from "./types";

export function iscpSettingsUnavailable(language: Language) {
  return language === "zh" ? "当前连接暂不支持此操作。" : "This action is unavailable through the current connection.";
}

export function desktopSettingsAccess(connection: DesktopConnectionStatus | undefined, language: Language): SettingsAccess {
  const backendLoginNotice = language === "zh"
    ? "此连接需要复用后端浏览器的登录态，请在后台完成登录绑定。"
    : "This connection uses the backend browser’s saved sign-in session. Complete sign-in in the backend.";
  const access = connection?.backend?.transport === "iscp"
    ? iscpTransportSettingsAccess(connection, language)
    : { connectorsAvailable: true, unavailable: {} };
  return {
    ...access,
    emailLoginNotice: backendLoginNotice,
    emailLoginAvailable: false,
    aiPlatformLoginNotice: backendLoginNotice,
    aiPlatformLoginAvailable: false,
    aiPlatformStatusAvailable: connection?.backend?.transport !== "iscp",
    mcpLANOrigin: connection?.backend?.transport !== "iscp" && connection?.backend?.origin
      ? connection.backend.origin : null,
  };
}

// Transport constraints belong to actions, never to the shared menu or layout.
function iscpTransportSettingsAccess(connection: DesktopConnectionStatus, language: Language): SettingsAccess {
  const unavailable = iscpSettingsUnavailable(language);
  const credentials = surfaceEnabled(connection, "settings_credentials");
  return {
    connectorsAvailable: surfaceEnabled(connection, "settings_connectors"),
    bindingsUnavailable: surfaceEnabled(connection, "settings_bindings") ? undefined : unavailable,
    clientDeletionAvailable: surfaceEnabled(connection, "settings_devices_delete"),
    unavailable: {
      ...(!surfaceEnabled(connection, "settings_devices") ? { clients: unavailable } : {}), "tool-policy": unavailable,
      ...(!surfaceEnabled(connection, "settings_connectors") ? { messaging: unavailable } : {}),
      ...(!surfaceEnabled(connection, "settings_browser_control") ? { "browser-control": unavailable } : {}),
      ...(!surfaceEnabled(connection, "settings_mcp_access") ? { "external-mcp": unavailable } : {}),
      ...(!surfaceEnabled(connection, "mail_settings") ? { "browser-email": unavailable } : {}),
      ...(!credentials ? { info: unavailable, localmind: unavailable } : {}),
    },
    loadIntegrationStatus: async id => {
      const response = await api.integrations();
      const status = response.integrations.find(item => item.id === id);
      if (!status) throw new Error(unavailable);
      return status;
    },
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
