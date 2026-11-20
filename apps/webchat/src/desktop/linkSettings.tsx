import { api } from "../api/client";
import type { Language } from "../i18n";
import type { SettingsAccess } from "../components/panels/settings";
import { operationBackend, surfaceEnabled } from "./capability";
import type { DesktopConnectionStatus } from "./types";

export function linkSurfaceUnavailable(language: Language) {
  return language === "zh" ? "当前连接暂不支持此操作。" : "This action is unavailable through the current connection.";
}

export function desktopSettingsAccess(connection: DesktopConnectionStatus | undefined, language: Language): SettingsAccess {
  const backendLoginNotice = language === "zh"
    ? "此连接需要复用后端浏览器的登录态，请在后台完成登录绑定。"
    : "This connection uses the backend browser’s saved sign-in session. Complete sign-in in the backend.";
  const access = connection && operationBackend(connection)
    ? linkSettingsAccess(connection, language)
    : { connectorsAvailable: true, unavailable: {} };
  return {
    ...access,
    emailLoginNotice: backendLoginNotice,
    emailLoginAvailable: false,
    aiPlatformLoginNotice: backendLoginNotice,
    aiPlatformLoginAvailable: false,
    aiPlatformStatusAvailable: !operationBackend(connection),
    mcpLANOrigin: !operationBackend(connection) && connection?.backend?.origin
      ? connection.backend.origin : null,
  };
}

// Transport constraints belong to actions, never to the shared menu or layout.
function linkSettingsAccess(connection: DesktopConnectionStatus, language: Language): SettingsAccess {
  const unavailable = linkSurfaceUnavailable(language);
  const credentials = surfaceEnabled(connection, "settings_credentials");
  return {
    connectorsAvailable: surfaceEnabled(connection, "settings_connectors"),
    bindingsUnavailable: surfaceEnabled(connection, "settings_bindings") ? undefined : unavailable,
    clientDeletionAvailable: surfaceEnabled(connection, "settings_devices_delete"),
    unavailable: {
      ...(!surfaceEnabled(connection, "settings_devices") ? { clients: language === "zh" ? "设备在 SparkClaw 控制台加入和移除。" : "Devices join and are removed on the SparkClaw console." } : {}), "tool-policy": unavailable,
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

export function LinkDeviceAuthorization({ language }: { connection: DesktopConnectionStatus; language: Language }) {
  const zh = language === "zh";
  // A link device's membership belongs to the SparkClaw console, where it is
  // removed; the original section stays with its action disabled.
  return <section className="settingsSurfaceSection">
    <h2>{zh ? "此设备授权" : "This device authorization"}</h2>
    <p className="settingsSurfaceHint">{zh ? "此设备已加入 SparkClaw 控制台的域，在控制台移除前一直有效。对话保存在后端。" : "This device stays joined to its SparkClaw console domain until it is removed on the console. Conversations are kept on the backend."}</p>
    <button type="button" className="reject" disabled>{zh ? "删除此设备授权" : "Delete this device authorization"}</button>
  </section>;
}
