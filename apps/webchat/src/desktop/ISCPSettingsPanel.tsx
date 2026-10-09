import { useEffect, useState } from "react";
import { api } from "../api/client";
import type { ConnectorStatus, IntegrationStatus, OwnerProfile } from "../api/types";
import type { Copy, Language } from "../i18n";
import type { PanelTab } from "../components/inspector";
import { OwnerProfileSettings } from "../components/panels/settingsOwner";
import { IntegrationCredentialSettings } from "../components/panels/settingsIntegrations";
import { currentTheme, setTheme, type Theme } from "../lib/appearance";
import { surfaceEnabled } from "./capability";
import type { DesktopConnectionStatus } from "./types";

// Each section mounts only its own qualified API. Opening appearance cannot
// initiate credential, browser, connector or mail network activity.
export function ISCPSettingsPanel({connection,tab,text,language}:{connection:DesktopConnectionStatus;tab:PanelTab;text:Copy;language:Language}) {
 const zh=language==='zh';const [owner,setOwner]=useState<OwnerProfile|null>(null);
 const [connectors,setConnectors]=useState<ConnectorStatus[]>([]);const [integrations,setIntegrations]=useState<IntegrationStatus[]>([]);
 const [theme,changeTheme]=useState<Theme>(currentTheme);const [error,setError]=useState('');const [busy,setBusy]=useState(false);
 const surface=tab==='settings'?'settings_owner':tab==='connections'?'settings_connectors':'settings_credentials';
 const enabled=tab==='appearance'||tab==='devices'||surfaceEnabled(connection,surface);
 useEffect(()=>{let active=true;setError('');if(!enabled||tab==='appearance'||tab==='devices')return;
 const load=async()=>{if(tab==='settings'){const value=await api.owner();if(active)setOwner(value);}else if(tab==='connections'){const value=await api.connectors();if(active)setConnectors(value.connectors);}else{const value=await api.integrations();if(active)setIntegrations(value.integrations);}};
 void load().catch(e=>{if(active)setError(String(e.message||e));});return()=>{active=false;};},[tab,enabled,connection.client_id]);
 if(!enabled)return <p role="status">{zh?'此功能尚未完成授权或验收。':'This feature still requires authorization or qualification.'}</p>;
 return <div className="panelStack settingsPanel">
 {error&&<p role="alert">{error}</p>}
 {tab==='devices'&&<section><p>{zh?'此设备授权一直有效，直至手动删除。删除后不能继续调用，历史仍保存在本机。':'This device remains authorized until you delete its authorization. Deletion stops access and retains local history.'}</p><button type="button" disabled={busy||!window.sparkclawDesktop?.deleteAuthorization} onClick={()=>{setBusy(true);void window.sparkclawDesktop!.deleteAuthorization!().catch(e=>setError(String(e.message||e))).finally(()=>setBusy(false));}}>{zh?'删除此设备授权':'Delete this device authorization'}</button></section>}
 {tab==='appearance'?<label>{zh?'主题':'Theme'}<select value={theme} onChange={event=>{const next=event.target.value as Theme;setTheme(next);changeTheme(next);}}>{(['system','light','dark'] as const).map(value=><option key={value} value={value}>{value}</option>)}</select></label>:null}
 {tab==='settings'&&owner&&<OwnerProfileSettings ownerProfile={owner} text={text} onUpdateOwner={async(name,email,preferences)=>{setOwner(await api.updateOwner(name,email,preferences));}}/>}
 {tab==='connections'&&connectors.map(connector=><label className="settingsSurfaceRow" key={connector.channel}><span>{connector.channel}</span><input type="checkbox" checked={connector.enabled} disabled={busy} onChange={event=>{const enabled=event.target.checked;setBusy(true);void api.updateConnector(connector.channel,enabled,connector.version).then(next=>setConnectors(rows=>rows.map(row=>row.channel===next.channel?next:row))).catch(e=>setError(String(e.message||e))).finally(()=>setBusy(false));}}/></label>)}
 {tab==='models-tools'&&integrations.map(status=><IntegrationCredentialSettings key={status.id} id={status.id} status={status} text={text} language={language} loadStatus={async id=>{const value=await api.integrations();setIntegrations(value.integrations);const found=value.integrations.find(status=>status.id===id);if(!found)throw new Error(text.errors.integration);return found;}} onStatus={next=>setIntegrations(rows=>rows.map(row=>row.id===next.id?next:row))}/>)}
 </div>;
}
