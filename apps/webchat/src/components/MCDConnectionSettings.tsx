import { useEffect, useState } from "react";
import { desktopCapability } from "../desktop/capability";
import type { MCDConnectionStatus } from "../desktop/types";

export function MCDConnectionSettings({ language }: { language: string }) {
  const native = desktopCapability();
  const zh = language === "zh";
  const [status, setStatus] = useState<MCDConnectionStatus>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    let mounted = true;
    void native?.mcdStatus?.().then(value => { if (mounted) setStatus(value); }).catch(() => {});
    return () => { mounted = false; };
  }, [native]);
  async function change(disconnect: boolean) {
    if (busy || !(disconnect ? status?.disconnect_available ?? status?.available : status?.available)) return;
    setBusy(true); setError("");
    try {
      if (disconnect) await native?.mcdDisconnect?.(); else await native?.mcdConnect?.();
      setStatus(await native?.mcdStatus?.());
    } catch { setError(zh ? "连接未完成，请重试。" : "The connection could not be completed. Please retry."); }
    finally { setBusy(false); }
  }
  const connected = status?.state === "ready";
  const revoke = status && !["disconnected", "unavailable"].includes(status.state);
  return <section className="settingsSurfaceSection">
    <p className="settingsSurfaceHint">{zh ? "连接麦当劳账号，查询门店、菜单和优惠。" : "Connect your McDonald’s account to browse restaurants, menus and offers."}</p>
    {connected && <p role="status">{zh ? "已连接" : "Connected"}</p>}
    <div className="buttonRow textActions">
      <button type="button" disabled={busy || !status?.available || status.state === "revoking"} onClick={() => void change(false)}>
        {busy ? (zh ? "处理中…" : "Working…") : connected || status?.state === "expired" ? (zh ? "更新连接" : "Renew connection") : (zh ? "连接账号" : "Connect account")}
      </button>
      {revoke && <button type="button" disabled={busy || !(status.disconnect_available ?? status.available)} onClick={() => void change(true)}>{zh ? "断开连接" : "Disconnect"}</button>}
    </div>
    {error && <p role="alert">{error}</p>}
  </section>;
}
