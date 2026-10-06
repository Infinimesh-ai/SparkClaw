import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import {
  ArrowLeft,
  Bot,
  Cable,
  Check,
  ChevronRight,
  CircleUserRound,
  Clock3,
  Cpu,
  DatabaseZap,
  Languages,
  Mail,
  MessageSquare,
  Network,
  Pencil,
  RefreshCw,
  ServerCog,
  Settings,
  ShieldCheck,
  Users,
  X
} from "lucide-react";
import { api } from "../../api/client";
import type {
  Client,
  ConnectorStatus,
  IntegrationStatus,
  NotificationBinding,
  OwnerProfile,
  PublicConfig
} from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import { formatRisk, parseToolList, profileLabel, rateLimitLabel, retentionLabel } from "../../lib/format";
import { currentTextSize, currentTheme, setTextSize, setTheme, type TextSize, type Theme } from "../../lib/appearance";
import { ExternalMCPSettings } from "../externalMCPSettings";
import { SectionHeader } from "./primitives";
import { ConnectorBindingSettings } from "./settingsBindings";
import { PairedClientsSettings, type ClientSettingsActions } from "./settingsClients";
import { AIPlatformLoginSettings } from "./settingsAIPlatforms";
import { BrowserEmailSettings } from "./settingsEmail";
import { BrowserControlSettings } from "./settingsBrowserControl";
import { IntegrationCredentialSettings } from "./settingsIntegrations";
import { integrationStateLabel } from "./settingsIntegrationState";
import { OwnerProfileSettings } from "./settingsOwner";

type SettingsCategory = "account" | "connections" | "agent" | "system";
export type WorkspaceSettingsSection = "general" | "appearance" | "devices" | "models-tools" | "permissions" | "connections";
type SettingsDetail =
  | "owner"
  | "clients"
  | "language"
  | "messaging"
  | "browser-control"
  | "ai-platform-login"
  | "browser-email"
  | "info"
  | "localmind"
  | "external-mcp"
  | "tool-policy"
  | "models"
  | "runtime";

export function SettingsPanel({
  connectionsOnly = false,
  section,
  runtimeConfig,
  ownerProfile,
  clients,
  currentClientID,
  accessMode,
  clientsLoading,
  clientsError,
  onReloadClients,
  onCurrentClientRevoked,
  onLogout,
  connectors,
  notificationBindings,
  text,
  language,
  onLanguageChange,
  onOpenSchedules,
  onUpdateOwner,
  onIssueClient,
  onRevokeClient,
  onStartNotificationBinding,
  onRefreshNotificationBinding,
  onOpenNotificationBindingBrowser,
  onRevokeNotificationBinding,
  onUpdateConnector,
  onUpdatePolicy,
  onCheckStatus
}: ClientSettingsActions & {
  connectionsOnly?: boolean;
  section?: WorkspaceSettingsSection;
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  connectors: ConnectorStatus[];
  notificationBindings: NotificationBinding[];
  text: CopyText;
  language: Language;
  onLanguageChange?: (language: Language) => void;
  onOpenSchedules?: () => void;
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
  onIssueClient?: (name: string, idempotencyKey: string) => Promise<import("../../api/types").IssuedClientCredential>;
  onRevokeClient: (id: string) => Promise<void>;
  onStartNotificationBinding: (channel: string, botToken?: string) => Promise<void>;
  onRefreshNotificationBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  onOpenNotificationBindingBrowser: (id: string) => Promise<void>;
  onRevokeNotificationBinding: (id: string) => Promise<void>;
  onUpdateConnector: (channel: string, enabled: boolean, expectedVersion: number) => Promise<ConnectorStatus>;
  onUpdatePolicy: (deny: string[], approvalRequired: string[], controls?: PublicConfig["tool_policy"]["operator_controls"]) => Promise<void>;
  onCheckStatus?: (section: WorkspaceSettingsSection) => Promise<void>;
}) {
  const [category, setCategory] = useState<SettingsCategory>("account");
  const [detail, setDetail] = useState<SettingsDetail | null>(null);
  const [selectedChannel, setSelectedChannel] = useState("");
  const [integrations, setIntegrations] = useState<IntegrationStatus[]>([]);
  const [integrationLoadFailed, setIntegrationLoadFailed] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const [policyError, setPolicyError] = useState("");
  const [checkingStatus, setCheckingStatus] = useState(false);
  const [statusError, setStatusError] = useState("");
  const [theme, updateTheme] = useState<Theme>(() => currentTheme());
  const [textSize, updateTextSize] = useState<TextSize>(() => currentTextSize());
  const [loginStartup, setLoginStartup] = useState<{ supported: boolean; enabled: boolean } | null>(null);
  const [loginBusy, setLoginBusy] = useState(false);
  const policyAction = useAsyncAction({ clearError: () => setPolicyError(""), onError: (error) => setPolicyError(error instanceof Error ? error.message : String(error)) });
  const savingPolicy = Boolean(policyAction.busy);

  useEffect(() => {
    if (section !== "general") return;
    let active = true;
    window.sparkclawDesktop?.loginStartup().then((result) => { if (active) setLoginStartup(result); })
      .catch(() => { if (active) setLoginStartup({ supported: false, enabled: false }); });
    return () => { active = false; };
  }, [section]);

  useEffect(() => {
    let active = true;
    api.integrations()
      .then((response) => {
        if (active) {
          setIntegrations(response.integrations);
          setIntegrationLoadFailed(false);
        }
      })
      .catch(() => { if (active) setIntegrationLoadFailed(true); });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    setDetail(null);
    setSelectedChannel("");
    setStatusError("");
  }, [section]);

  useEffect(() => {
    if (!detail) return;
    document.querySelector<HTMLElement>(".settingsPageMain")?.scrollTo?.({ top: 0, behavior: "auto" });
  }, [detail]);

  const updateIntegration = useCallback((next: IntegrationStatus) => {
    setIntegrations((current) => [next, ...current.filter((item) => item.id !== next.id)]);
    setIntegrationLoadFailed(false);
  }, []);

  async function changeLoginStartup(enabled: boolean) {
    if (!window.sparkclawDesktop || !loginStartup?.supported) return;
    setLoginBusy(true);
    try { setLoginStartup(await window.sparkclawDesktop.loginStartup(enabled)); }
    catch (error) { setPolicyError(error instanceof Error ? error.message : String(error)); }
    finally { setLoginBusy(false); }
  }

  if (section === "devices") {
    return <div className="panelStack settingsPanel settingsSurface">
      <PairedClientsSettings clients={clients} text={text} language={language} currentClientID={currentClientID} accessMode={accessMode}
        clientsLoading={clientsLoading} clientsError={clientsError} onReloadClients={onReloadClients}
        onIssueClient={onIssueClient} onRevokeClient={onRevokeClient} onCurrentClientRevoked={onCurrentClientRevoked} onLogout={onLogout} />
    </div>;
  }

  if (section === "general") {
    const surface = settingsSurfaceCopy[language];
    return <div className="panelStack settingsPanel settingsSurface">
      <section className="settingsSurfaceSection">
        <h2>{surface.appBehavior}</h2>
        <div className="settingsSurfaceRow">
          <span className="settingsSurfaceCopy"><strong>{surface.openAtLogin}</strong><small>{surface.openAtLoginDescription}</small></span>
          <button type="button" className="settingsSwitch" role="switch" aria-label={surface.openAtLogin} aria-checked={loginStartup?.enabled ?? false} disabled={!loginStartup?.supported || loginBusy} onClick={() => void changeLoginStartup(!loginStartup?.enabled)}><span /></button>
        </div>
        {loginStartup?.supported === false || !window.sparkclawDesktop ? <small className="settingsSurfaceHint">{surface.desktopOnly}</small> : null}
      </section>
      <section className="settingsSurfaceSection">
        <h2>{surface.languageRegion}</h2>
        {onLanguageChange && <label className="settingsSurfaceRow">
          <span className="settingsSurfaceCopy"><strong>{surface.interfaceLanguage}</strong><small>{surface.interfaceLanguageDescription}</small></span>
          <select value={language} onChange={(event) => onLanguageChange(event.target.value as Language)} aria-label={surface.interfaceLanguage}>
            <option value="zh">简体中文</option><option value="en">English</option>
          </select>
        </label>}
      </section>
      {policyError && <p className="compactError" role="alert">{policyError}</p>}
    </div>;
  }

  if (section === "appearance") {
    const surface = settingsSurfaceCopy[language];
    return <div className="panelStack settingsPanel settingsSurface">
      <section className="settingsSurfaceSection"><h2>{surface.interface}</h2>
        <div className="settingsSurfaceRow"><span className="settingsSurfaceCopy"><strong>{surface.theme}</strong><small>{surface.themeDescription}</small></span>
          <div className="settingsSegmented" role="group" aria-label={surface.theme}>{(["system", "light", "dark"] as const).map((value) => <button key={value} type="button" className={theme === value ? "selected" : ""} aria-pressed={theme === value} onClick={() => { setTheme(value); updateTheme(value); }}>{surface[value]}</button>)}</div>
        </div>
        <label className="settingsSurfaceRow"><span className="settingsSurfaceCopy"><strong>{surface.textSize}</strong><small>{surface.textSizeDescription}</small></span>
          <select aria-label={surface.textSize} value={textSize} onChange={(event) => { const value = event.target.value as TextSize; setTextSize(value); updateTextSize(value); }}><option value="default">{surface.defaultSize}</option><option value="large">{surface.largeSize}</option></select>
        </label>
      </section>
    </div>;
  }

  const policy = runtimeConfig?.tool_policy;
  const controls = policy?.operator_controls;
  const riskCounts = policy ? Object.entries(policy.risk_counts).sort(([left], [right]) => left.localeCompare(right)) : [];
  const infoStatus = integrations.find((item) => item.id === "infinimesh-info") ?? null;
  const localMindStatus = integrations.find((item) => item.id === "localmind") ?? null;

  function startPolicyEdit() {
    if (!policy) return;
    setDenyText(policy.denied_tools.join("\n"));
    setApprovalText(policy.configured_approval_required_tools.join("\n"));
    setEditingPolicy(true);
  }

  function cancelPolicyEdit() {
    setEditingPolicy(false);
    setDenyText("");
    setApprovalText("");
  }

  async function savePolicyEdit() {
    await policyAction.run("policy", async () => {
      await onUpdatePolicy(parseToolList(denyText), parseToolList(approvalText));
      cancelPolicyEdit();
    });
  }

  async function saveControls(next: Partial<PublicConfig["tool_policy"]["operator_controls"]>) {
    if (!runtimeConfig || !controls) return;
    const policy = runtimeConfig.tool_policy;
    await policyAction.run("controls", () => onUpdatePolicy(policy.denied_tools, policy.configured_approval_required_tools, { ...policy.operator_controls, ...next }));
  }

  async function checkStatus(target: WorkspaceSettingsSection) {
    if (!onCheckStatus || checkingStatus) return;
    setCheckingStatus(true);
    setStatusError("");
    try { await onCheckStatus(target); }
    catch (error) { setStatusError(error instanceof Error ? error.message : String(error)); }
    finally { setCheckingStatus(false); }
  }

  function changeCategory(next: SettingsCategory) {
    setCategory(next);
    setDetail(null);
  }

  const detailTitle = detail ? settingsDetailTitle(detail, text) : "";

  if (section && !connectionsOnly) {
    const surface = settingsSurfaceCopy[language];
    const messageConnectors = [
      ...(["weixin", "telegram"] as const).map((channel) => connectors.find((item) => item.channel === channel) ?? pendingConnector(channel)),
      ...connectors.filter((item) => item.channel !== "mcp" && item.channel !== "weixin" && item.channel !== "telegram")
    ];
    const selectedConnector = messageConnectors.find((item) => item.channel === selectedChannel);

    if (section === "models-tools") {
      return (
        <div className="panelStack settingsPanel settingsSurface">
          <SettingsStatus available={Boolean(controls)} busy={checkingStatus} error={statusError} surface={surface} onCheck={() => void checkStatus(section)} />
          <section className="settingsSurfaceSection">
            <h2>{surface.tools}</h2>
            {(["web_access", "workspace_files", "shell_commands"] as const).map((control) => {
              const group = control === "web_access" ? "web" : control === "workspace_files" ? "files" : "shell";
              return <div className="settingsSurfaceRow" key={group}>
                <span className="settingsSurfaceCopy"><strong>{surface.toolLabels[group]}</strong><small>{surface.toolDescriptions[group]}</small></span>
                <button type="button" className="settingsSwitch" role="switch" aria-label={surface.toolLabels[group]} aria-checked={controls?.[control] ?? false} disabled={!controls || savingPolicy} onClick={() => void saveControls({ [control]: !controls?.[control] })}><span /></button>
              </div>;
            })}
          </section>
          {policyError && <p className="compactError" role="alert">{policyError}</p>}
        </div>
      );
    }

    if (section === "permissions") {
      return (
        <div className="panelStack settingsPanel settingsSurface">
          <SettingsStatus available={Boolean(controls)} busy={checkingStatus} error={statusError} surface={surface} onCheck={() => void checkStatus(section)} />
          <section className="settingsSurfaceSection">
            <div className="settingsSurfaceSectionHeading">
              <h2>{surface.approvalPolicy}</h2>
              {policy && <span>{policy.definition_count} {text.trace.tools}</span>}
            </div>
            <label className="settingsSurfaceRow"><span className="settingsSurfaceCopy"><strong>{surface.fileChanges}</strong><small>{surface.fileChangesDescription}</small></span>
              <select aria-label={surface.fileChanges} disabled={!controls || savingPolicy} value={controls?.file_changes ?? ""} onChange={(event) => void saveControls({ file_changes: event.target.value as "ask" | "default" })}>
                {!controls && <option value="">{surface.statusPending}</option>}
                <option value="ask">{surface.askEveryTime}</option><option value="default">{surface.askDestructive}</option>
              </select>
            </label>
            <label className="settingsSurfaceRow"><span className="settingsSurfaceCopy"><strong>{surface.externalActions}</strong><small>{surface.externalActionsDescription}</small></span>
              <select aria-label={surface.externalActions} disabled={!controls || savingPolicy} value={controls?.external_actions ?? ""} onChange={(event) => void saveControls({ external_actions: event.target.value as "ask" | "block" })}>
                {!controls && <option value="">{surface.statusPending}</option>}
                <option value="current" disabled>{surface.currentRules}</option><option value="ask">{surface.askEveryTime}</option><option value="block">{surface.block}</option>
              </select>
            </label>
            {policy && <>
              <ReadOnlySetting label={text.settings.external} value={policy.external_content_untrusted ? text.settings.untrusted : text.settings.trusted} />
              <ReadOnlySetting label={text.settings.dangerous} value={policy.approval_required_for_dangerous_tools ? text.settings.approvalRequired : text.settings.notForced} />
              <ReadOnlySetting label={text.settings.verifier} value={policy.dangerous_tools_deep_verification ? text.settings.deepCheck : text.settings.standard} />
              <ReadOnlySetting label={text.settings.sandbox} value={policy.sandbox_required_for_mutating_tools ? text.settings.mutationsRequireSandbox : text.settings.notForced} />
            </>}
          </section>
          {policyError && <p className="compactError" role="alert">{policyError}</p>}

          {policy && <section className="settingsSurfaceSection policySettingsSection">
            <div className="settingsSurfaceSectionHeading">
              <h2>{surface.toolRules}</h2>
              <div className="buttonRow compactButtons">
                {editingPolicy ? (
                  <>
                    <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title={text.settings.saveToolPolicy}><Check size={15} /></button>
                    <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title={text.settings.cancelPolicy}><X size={15} /></button>
                  </>
                ) : <button className="edit" onClick={startPolicyEdit} title={text.settings.editPolicy}><Pencil size={15} /></button>}
              </div>
            </div>
            <div className="settingsPolicySummary">
              <strong>{text.settings.definitionApprovalTools}</strong>
              <div className="evalCases">
                {policy.definition_approval_required_tools.map((tool) => <span key={tool}>{tool}</span>)}
                {policy.definition_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
              </div>
            </div>
            {editingPolicy ? (
              <div className="policyEditor settingsPolicyEditor">
                <label><span>{text.settings.configApprovalAdditions}</span><textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} /></label>
                <label><span>{text.settings.deniedTools}</span><textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} /></label>
              </div>
            ) : (
              <div className="settingsPolicyColumns">
                <div><strong>{text.settings.configApprovalAdditions}</strong><div className="evalCases">{policy.configured_approval_required_tools.map((tool) => <span key={`configured-${tool}`}>{tool}</span>)}{policy.configured_approval_required_tools.length === 0 && <span>{text.common.none}</span>}</div></div>
                <div><strong>{text.settings.deniedTools}</strong><div className="evalCases">{policy.denied_tools.map((tool) => <span className="failed" key={tool}>{tool}</span>)}{policy.denied_tools.length === 0 && <span>{text.common.none}</span>}</div></div>
              </div>
            )}
            <div className="evalCases settingsRiskCounts">{riskCounts.map(([risk, count]) => <span key={risk}>{formatRisk(risk, text)}:{count}</span>)}</div>
          </section>}
        </div>
      );
    }

    if (detail) {
      const connectionDetailTitle = detail === "messaging" && selectedConnector
        ? connectorTitle(selectedConnector, text)
        : detailTitle;
      return (
        <div className="panelStack settingsPanel settingsSurface settingsConnectionDetail">
          <SettingsStatus available={connectors.length > 0} busy={checkingStatus} error={statusError} surface={surface} onCheck={() => void checkStatus(section)} />
          <button className="settingsBack" type="button" onClick={() => { setDetail(null); setSelectedChannel(""); }} title={text.common.back}>
            <ArrowLeft size={16} />
            <span>{connectionDetailTitle}</span>
          </button>
          {detail === "messaging" && selectedConnector && <ConnectorBindingSettings
            connectors={[selectedConnector]}
            notificationBindings={notificationBindings}
            text={text}
            language={language}
            onStartNotificationBinding={onStartNotificationBinding}
            onRefreshNotificationBinding={onRefreshNotificationBinding}
            onOpenNotificationBindingBrowser={onOpenNotificationBindingBrowser}
            onRevokeNotificationBinding={onRevokeNotificationBinding}
            onUpdateConnector={onUpdateConnector}
          />}
          {detail === "browser-control" && <BrowserControlSettings text={text} language={language} />}
          {detail === "ai-platform-login" && <AIPlatformLoginSettings text={text} language={language} />}
          {detail === "browser-email" && <BrowserEmailSettings text={text} />}
          {detail === "info" && <IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={text} language={language} onStatus={updateIntegration} />}
          {detail === "localmind" && <IntegrationCredentialSettings id="localmind" status={localMindStatus} text={text} language={language} onStatus={updateIntegration} />}
          {detail === "external-mcp" && <ExternalMCPSettings connector={connectors.find((item) => item.channel === "mcp")} text={text} language={language} onUpdateConnector={onUpdateConnector} />}
        </div>
      );
    }

    return (
      <div className="panelStack settingsPanel settingsSurface settingsConnectionsDirectory">
        <SettingsStatus available={connectors.length > 0} busy={checkingStatus} error={statusError} surface={surface} onCheck={() => void checkStatus(section)} />
        <section className="settingsSurfaceSection">
          <h2>{surface.messagingChannels}</h2>
          <ConnectionRow icon={<Mail size={17} />} title={surface.email} description={surface.emailDescription} action={surface.manage} onClick={() => setDetail("browser-email")} />
          {messageConnectors.map((connector) => <ConnectionRow
            key={connector.channel}
            icon={<MessageSquare size={17} />}
            title={connectorTitle(connector, text)}
            description={`${connector.provider ? `${connector.provider} · ` : ""}${connector.state === "status_pending" ? surface.statusPending : connectorDirectoryStatus(connector, text)}`}
            status={connector.state === "status_pending" ? undefined : connector.enabled ? text.settings.active : text.common.disabled}
            action={connector.available ? connector.enabled ? surface.manage : surface.connect : surface.manage}
            connected={connector.state === "active"}
            onClick={() => { setSelectedChannel(connector.channel); setDetail("messaging"); }}
          />)}
        </section>

        <section className="settingsSurfaceSection">
          <h2>{surface.services}</h2>
          <ConnectionRow icon={<Cable size={17} />} title={text.settings.browserControl} description={text.settings.browserControlSharedProfile} action={surface.manage} onClick={() => setDetail("browser-control")} />
          <ConnectionRow icon={<Bot size={17} />} title={text.settings.aiPlatformLogin} description={text.settings.aiPlatformNames} action={surface.manage} onClick={() => setDetail("ai-platform-login")} />
          <ConnectionRow icon={<DatabaseZap size={17} />} title={text.settings.info} description={integrationDirectoryStatus(infoStatus, integrationLoadFailed, text)} action={surface.manage} onClick={() => setDetail("info")} />
          <ConnectionRow icon={<Network size={17} />} title={text.settings.localMind} description={integrationDirectoryStatus(localMindStatus, integrationLoadFailed, text)} action={surface.manage} onClick={() => setDetail("localmind")} />
          <ConnectionRow icon={<Cable size={17} />} title={text.settings.externalMCP} description={connectors.find((item) => item.channel === "mcp")?.enabled ? text.settings.active : text.common.disabled} action={surface.manage} onClick={() => setDetail("external-mcp")} />
        </section>
      </div>
    );
  }

  if (!runtimeConfig || !policy) {
    return (
      <div className="panelStack">
        <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
        <span className="muted">{text.settings.unavailable}</span>
      </div>
    );
  }

  return (
    <div className={`panelStack settingsPanel ${connectionsOnly ? "connectionSettings" : ""}`}>
      {!connectionsOnly && <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />}
      {!connectionsOnly && <div className="settingsCategoryTabs" role="tablist" aria-label={text.settings.categories}>
        <CategoryTab selected={category === "account"} label={text.settings.account} icon={<CircleUserRound size={15} />} onClick={() => changeCategory("account")} />
        <CategoryTab selected={category === "connections"} label={text.settings.connections} icon={<Cable size={15} />} onClick={() => changeCategory("connections")} />
        <CategoryTab selected={category === "agent"} label={text.settings.agent} icon={<Bot size={15} />} onClick={() => changeCategory("agent")} />
        <CategoryTab selected={category === "system"} label={text.settings.system} icon={<ServerCog size={15} />} onClick={() => changeCategory("system")} />
      </div>}

      {detail ? (
        <div className={`settingsDetailView ${connectionsOnly && detail === "messaging" ? "connectionMessagingDetail" : ""}`}>
          <button className="settingsBack" type="button" onClick={() => setDetail(null)} title={text.common.back}>
            <ArrowLeft size={16} />
            <span>{detailTitle}</span>
          </button>
          {detail === "owner" && <OwnerProfileSettings ownerProfile={ownerProfile} text={text} onUpdateOwner={onUpdateOwner} />}
          {detail === "clients" && <PairedClientsSettings clients={clients} text={text} language={language} currentClientID={currentClientID} accessMode={accessMode}
            clientsLoading={clientsLoading} clientsError={clientsError} onReloadClients={onReloadClients}
            onIssueClient={onIssueClient} onRevokeClient={onRevokeClient} onCurrentClientRevoked={onCurrentClientRevoked} onLogout={onLogout} />}
          {detail === "language" && (
            <article className="settingsBlock">
              <strong>{text.nav.language}</strong>
              <div className="settingsLanguageChoices" role="radiogroup" aria-label={text.nav.language}>
                <button type="button" className={language === "zh" ? "selected" : ""} role="radio" aria-checked={language === "zh"} onClick={() => onLanguageChange?.("zh")}>简体中文</button>
                <button type="button" className={language === "en" ? "selected" : ""} role="radio" aria-checked={language === "en"} onClick={() => onLanguageChange?.("en")}>English</button>
              </div>
            </article>
          )}
          {detail === "messaging" && (
            <ConnectorBindingSettings
              connectors={connectors}
              notificationBindings={notificationBindings}
              text={text}
              language={language}
              onStartNotificationBinding={onStartNotificationBinding}
              onRefreshNotificationBinding={onRefreshNotificationBinding}
              onOpenNotificationBindingBrowser={onOpenNotificationBindingBrowser}
              onRevokeNotificationBinding={onRevokeNotificationBinding}
              onUpdateConnector={onUpdateConnector}
            />
          )}
          {detail === "browser-control" && <BrowserControlSettings text={text} language={language} />}
          {detail === "ai-platform-login" && <AIPlatformLoginSettings text={text} language={language} />}
          {detail === "browser-email" && <BrowserEmailSettings text={text} />}
          {detail === "info" && (
            <IntegrationCredentialSettings id="infinimesh-info" status={infoStatus} text={text} language={language} onStatus={updateIntegration} />
          )}
          {detail === "localmind" && (
            <IntegrationCredentialSettings id="localmind" status={localMindStatus} text={text} language={language} onStatus={updateIntegration} />
          )}
          {detail === "external-mcp" && (
            <ExternalMCPSettings
              connector={connectors.find((item) => item.channel === "mcp")}
              text={text}
              language={language}
              onUpdateConnector={onUpdateConnector}
            />
          )}
          {detail === "tool-policy" && (
            <>
              <article className="settingsBlock">
                <div className="approvalTop">
                  <strong>{text.settings.toolPolicy}</strong>
                  <span className="pill">{policy.definition_count} {text.trace.tools}</span>
                </div>
                <dl className="statusGrid compact">
                  <dt>{text.settings.file}</dt>
                  <dd>{policy.policy_path}</dd>
                  <dt>{text.settings.external}</dt>
                  <dd>{policy.external_content_untrusted ? text.settings.untrusted : text.settings.trusted}</dd>
                  <dt>{text.settings.dangerous}</dt>
                  <dd>{policy.approval_required_for_dangerous_tools ? text.settings.approvalRequired : text.settings.notForced}</dd>
                  <dt>{text.settings.verifier}</dt>
                  <dd>{policy.dangerous_tools_deep_verification ? text.settings.deepCheck : text.settings.standard}</dd>
                  <dt>{text.settings.sandbox}</dt>
                  <dd>{policy.sandbox_required_for_mutating_tools ? text.settings.mutationsRequireSandbox : text.settings.notForced}</dd>
                </dl>
                <div className="evalCases">
                  {riskCounts.map(([risk, count]) => <span key={risk}>{formatRisk(risk, text)}:{count}</span>)}
                </div>
              </article>
              <article className="settingsBlock">
                <strong>{text.settings.definitionApprovalTools}</strong>
                <div className="evalCases">
                  {policy.definition_approval_required_tools.map((tool) => <span key={tool}>{tool}</span>)}
                  {policy.definition_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
                </div>
              </article>
              <article className="settingsBlock">
                <strong>{text.settings.configApprovalAdditions}</strong>
                {editingPolicy ? (
                  <div className="policyEditor">
                    <label><span>{text.settings.approval}</span><textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} /></label>
                  </div>
                ) : (
                  <div className="evalCases">
                    {policy.configured_approval_required_tools.map((tool) => <span key={`configured-${tool}`}>{tool}</span>)}
                    {policy.configured_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
                  </div>
                )}
              </article>
              <article className="settingsBlock">
                <div className="approvalTop">
                  <strong>{text.settings.deniedTools}</strong>
                  <div className="buttonRow compactButtons">
                    {editingPolicy ? (
                      <>
                        <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title={text.settings.saveToolPolicy}><Check size={15} /></button>
                        <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title={text.settings.cancelPolicy}><X size={15} /></button>
                      </>
                    ) : <button className="edit" onClick={startPolicyEdit} title={text.settings.editPolicy}><Pencil size={15} /></button>}
                  </div>
                </div>
                {editingPolicy ? (
                  <div className="policyEditor">
                    <label><span>{text.settings.deny}</span><textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} /></label>
                  </div>
                ) : (
                  <div className="evalCases">
                    {policy.denied_tools.map((tool) => <span className="failed" key={tool}>{tool}</span>)}
                    {policy.denied_tools.length === 0 && <span>{text.common.none}</span>}
                  </div>
                )}
              </article>
            </>
          )}
          {detail === "models" && (
            <article className="settingsBlock">
              <strong>{text.settings.modelProfiles}</strong>
              <dl className="statusGrid compact">
                <dt>{text.settings.mode}</dt><dd>{runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel}</dd>
                <dt>{text.settings.capacityProfile}</dt><dd>{runtimeConfig.model.capacity_profile}</dd>
                <dt>{text.settings.fast}</dt><dd>{profileLabel(runtimeConfig.model.fast, text)}</dd>
                <dt>{text.settings.deep}</dt><dd>{profileLabel(runtimeConfig.model.deep, text)}</dd>
                <dt>{text.settings.embed}</dt><dd>{profileLabel(runtimeConfig.model.embedding, text)}</dd>
                <dt>{text.settings.guard}</dt><dd>{profileLabel(runtimeConfig.model.guard, text)}</dd>
              </dl>
            </article>
          )}
          {detail === "runtime" && (
            <article className="settingsBlock">
              <strong>{text.settings.runtimeBoundaries}</strong>
              <dl className="statusGrid compact">
                <dt>{text.status.gateway}</dt><dd>{runtimeConfig.gateway.bind}:{runtimeConfig.gateway.port}</dd>
                <dt>{text.settings.remote}</dt><dd>{runtimeConfig.gateway.remote_access}</dd>
                <dt>{text.status.rateLimit}</dt><dd>{rateLimitLabel(runtimeConfig.gateway.rate_limit, text)}</dd>
                <dt>{text.status.workspace}</dt><dd>{runtimeConfig.workspaces.default_root}</dd>
                <dt>{text.settings.sandbox}</dt><dd>{runtimeConfig.sandbox.enabled ? `${runtimeConfig.sandbox.backend} · ${runtimeConfig.sandbox.network}` : text.common.disabled}</dd>
                <dt>{text.status.state}</dt>
                <dd>{runtimeConfig.state.backend} · {runtimeConfig.state.path || runtimeConfig.state.dsn}{runtimeConfig.state.encrypt_at_rest ? ` · ${text.settings.encrypted}` : ""}</dd>
                <dt>{text.settings.artifacts}</dt><dd>{runtimeConfig.storage.artifact_backend} · {runtimeConfig.storage.artifact_dir || runtimeConfig.storage.artifact_bucket}</dd>
                <dt>{text.settings.memory}</dt><dd>{runtimeConfig.memory.enabled ? `${runtimeConfig.memory.write_policy} · ${retentionLabel(runtimeConfig.memory.retention_days, text)}` : text.common.disabled}</dd>
              </dl>
            </article>
          )}
        </div>
      ) : (
        <div className="settingsDirectory">
          {!connectionsOnly && category === "account" && (
            <>
              <DirectoryRow icon={<CircleUserRound size={17} />} title={text.settings.ownerProfile} status={ownerProfile?.display_name || text.settings.ownerUnavailable} onClick={() => setDetail("owner")} />
              <DirectoryRow icon={<Users size={17} />} title={text.settings.clients} status={String(clients.length)} onClick={() => setDetail("clients")} />
              {onLanguageChange && <DirectoryRow icon={<Languages size={17} />} title={text.nav.language} status={language === "zh" ? "简体中文" : "English"} onClick={() => setDetail("language")} />}
            </>
          )}
          {(connectionsOnly || (!connectionsOnly && category === "connections")) && (
            <>
              <DirectoryRow icon={<MessageSquare size={17} />} title={text.settings.messaging} status={connectionCountLabel(connectors, text)} onClick={() => setDetail("messaging")} />
              <DirectoryRow icon={<Cable size={17} />} title={text.settings.browserControl} status={text.settings.browserControlSharedProfile} onClick={() => setDetail("browser-control")} />
              <DirectoryRow icon={<Bot size={17} />} title={text.settings.aiPlatformLogin} status={text.settings.aiPlatformNames} onClick={() => setDetail("ai-platform-login")} />
              <DirectoryRow icon={<Mail size={17} />} title={text.settings.browserEmail} status={text.settings.browserEmailProviders} onClick={() => setDetail("browser-email")} />
              <DirectoryRow icon={<DatabaseZap size={17} />} title={text.settings.info} status={integrationDirectoryStatus(infoStatus, integrationLoadFailed, text)} onClick={() => setDetail("info")} />
              <DirectoryRow icon={<Network size={17} />} title={text.settings.localMind} status={integrationDirectoryStatus(localMindStatus, integrationLoadFailed, text)} onClick={() => setDetail("localmind")} />
              <DirectoryRow icon={<Cable size={17} />} title={text.settings.externalMCP} status={connectors.find((item) => item.channel === "mcp")?.enabled ? text.settings.active : text.common.disabled} onClick={() => setDetail("external-mcp")} />
            </>
          )}
          {!connectionsOnly && category === "agent" && (
            <>
              {onOpenSchedules && <DirectoryRow icon={<Clock3 size={17} />} title={text.schedules.title} status={text.schedules.nextRun} onClick={onOpenSchedules} />}
              <DirectoryRow icon={<ShieldCheck size={17} />} title={text.settings.toolPolicy} status={`${policy.definition_count} ${text.trace.tools}`} onClick={() => setDetail("tool-policy")} />
              <DirectoryRow icon={<Cpu size={17} />} title={text.settings.modelProfiles} status={runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel} onClick={() => setDetail("models")} />
            </>
          )}
          {!connectionsOnly && category === "system" && <DirectoryRow icon={<ServerCog size={17} />} title={text.settings.runtimeBoundaries} status={runtimeConfig.state.backend} onClick={() => setDetail("runtime")} />}
        </div>
      )}
    </div>
  );
}

const settingsSurfaceCopy = {
  zh: {
    appBehavior: "应用行为",
    openAtLogin: "登录时启动",
    openAtLoginDescription: "登录这台电脑时启动应用。",
    desktopOnly: "此选项仅在已安装的桌面应用中可用。",
    languageRegion: "语言与地区",
    interfaceLanguage: "界面语言",
    interfaceLanguageDescription: "更改后立即应用到当前工作区。",
    interface: "界面",
    theme: "主题",
    themeDescription: "跟随系统外观，或固定为浅色、深色。",
    system: "跟随系统", light: "浅色", dark: "深色",
    textSize: "文字大小",
    textSizeDescription: "调整整个界面的阅读尺寸。",
    defaultSize: "默认", largeSize: "较大",
    tools: "工具",
    toolLabels: { web: "网络访问", files: "工作区文件", shell: "终端命令", "file-changes": "文件变更", external: "外部操作" },
    toolDescriptions: { web: "允许任务浏览网站并获取实时信息。", files: "允许任务读取和处理工作区文件。", shell: "在审批策略下运行本地命令。", "file-changes": "", external: "" },
    approvalPolicy: "审批策略",
    fileChanges: "文件变更",
    fileChangesDescription: "移动、覆盖或删除文件前的确认方式。",
    externalActions: "外部操作",
    externalActionsDescription: "发送消息、提交表单或发布内容前的确认方式。",
    askEveryTime: "每次询问", askDestructive: "仅危险变更时询问", block: "阻止", currentRules: "当前规则",
    toolRules: "工具规则",
    runtime: "运行环境",
    localRuntime: "本地运行环境",
    messagingChannels: "消息渠道",
    email: "邮箱", emailDescription: "连接邮箱，读取消息并发送结果。",
    services: "服务",
    manage: "管理",
    connect: "连接",
    noMessagingChannels: "当前运行环境未提供消息渠道。",
    statusReady: "配置已读取", statusPending: "状态待检查", checkStatus: "检查状态", checkingStatus: "检查中…"
  },
  en: {
    appBehavior: "App behavior",
    openAtLogin: "Open at login",
    openAtLoginDescription: "Start the app when you sign in to this computer.",
    desktopOnly: "Available in the installed desktop app.",
    languageRegion: "Language & region",
    interfaceLanguage: "Interface language",
    interfaceLanguageDescription: "Changes apply immediately to this workspace.",
    interface: "Interface",
    theme: "Theme",
    themeDescription: "Follow the system appearance or choose a fixed theme.",
    system: "System", light: "Light", dark: "Dark",
    textSize: "Text size",
    textSizeDescription: "Adjust the reading size across the interface.",
    defaultSize: "Default", largeSize: "Large",
    tools: "Tools",
    toolLabels: { web: "Web access", files: "Workspace files", shell: "Shell commands", "file-changes": "File changes", external: "External actions" },
    toolDescriptions: { web: "Allow tasks to browse sites and retrieve current information.", files: "Allow tasks to read and work with workspace files.", shell: "Run local commands under your approval policy.", "file-changes": "", external: "" },
    approvalPolicy: "Approval policy",
    fileChanges: "File changes",
    fileChangesDescription: "Confirm before moving, overwriting, or deleting files.",
    externalActions: "External actions",
    externalActionsDescription: "Confirm before sending messages, submitting forms, or publishing content.",
    askEveryTime: "Ask every time", askDestructive: "Ask for destructive changes", block: "Block", currentRules: "Current rules",
    toolRules: "Tool rules",
    runtime: "Runtime",
    localRuntime: "Local runtime",
    messagingChannels: "Messaging channels",
    email: "Email", emailDescription: "Connect email to read messages and send results.",
    services: "Services",
    manage: "Manage",
    connect: "Connect",
    noMessagingChannels: "The current runtime does not expose any messaging channels.",
    statusReady: "Configuration loaded", statusPending: "Status pending", checkStatus: "Check status", checkingStatus: "Checking…"
  }
} as const;

function pendingConnector(channel: "weixin" | "telegram"): ConnectorStatus {
  return {
    channel,
    provider: "",
    setup_kind: channel === "weixin" ? "qr" : "secret",
    available: false,
    enabled: false,
    running: false,
    state: "status_pending",
    binding_status: "",
    binding_startable: false,
    supports_multiple_bindings: true,
    version: 0
  };
}

function SettingsStatus({ available, busy, error, surface, onCheck }: {
  available: boolean;
  busy: boolean;
  error: string;
  surface: { statusReady: string; statusPending: string; checkStatus: string; checkingStatus: string };
  onCheck: () => void;
}) {
  return <div className="settingsStatusArea">
    <div className="settingsStatusBar" role="status">
      <span className={available ? "ready" : "pending"}><i aria-hidden="true" />{available ? surface.statusReady : surface.statusPending}</span>
      <button type="button" className="secondaryButton" onClick={onCheck} disabled={busy}>
        <RefreshCw size={14} className={busy ? "spin" : ""} />{busy ? surface.checkingStatus : surface.checkStatus}
      </button>
    </div>
    {error && <p className="compactError" role="alert">{error}</p>}
  </div>;
}

function ReadOnlySetting({ label, value }: { label: string; value: string }) {
  return (
    <div className="settingsSurfaceRow">
      <span className="settingsSurfaceCopy"><strong>{label}</strong></span>
      <output>{value}</output>
    </div>
  );
}

function ConnectionRow({
  icon,
  title,
  description,
  status,
  action,
  connected = false,
  onClick
}: {
  icon: ReactNode;
  title: string;
  description: string;
  status?: string;
  action: string;
  connected?: boolean;
  onClick: () => void;
}) {
  return (
    <button className="settingsConnectionRow" type="button" onClick={onClick}>
      <span className="settingsConnectionIcon">{icon}</span>
      <span className="settingsSurfaceCopy"><strong>{title}</strong><small>{description}</small></span>
      {status && <span className={`settingsConnectionStatus ${connected ? "connected" : ""}`}><i />{status}</span>}
      <span className="settingsConnectionAction">{action}</span>
    </button>
  );
}

function connectorTitle(connector: ConnectorStatus, text: CopyText) {
  if (connector.channel === "telegram") return text.settings.telegramBinding;
  if (connector.channel === "weixin") return text.settings.weixinBinding;
  return connector.channel;
}

function connectorDirectoryStatus(connector: ConnectorStatus, text: CopyText) {
  switch (connector.state) {
    case "disabled": return text.settings.connectorDisabled;
    case "starting": return text.settings.connectorStarting;
    case "setup_required": return text.settings.connectorNeedsSetup;
    case "active": return text.settings.bound;
    case "error": return text.settings.connectorError;
    case "unavailable": return text.settings.bindingUnavailable;
    default: return connector.provider;
  }
}

function CategoryTab({ selected, label, icon, onClick }: { selected: boolean; label: string; icon: ReactNode; onClick: () => void }) {
  return <button className={selected ? "selected" : ""} type="button" role="tab" aria-selected={selected} onClick={onClick}>{icon}<span>{label}</span></button>;
}

function DirectoryRow({ icon, title, status, onClick }: { icon: ReactNode; title: string; status: string; onClick: () => void }) {
  return (
    <button className="settingsDirectoryRow" type="button" onClick={onClick}>
      <span className="settingsDirectoryIcon">{icon}</span>
      <span className="settingsDirectoryIdentity"><strong>{title}</strong><small>{status}</small></span>
      <ChevronRight size={16} />
    </button>
  );
}

function settingsDetailTitle(detail: SettingsDetail, text: CopyText) {
  const labels: Record<SettingsDetail, string> = {
    owner: text.settings.ownerProfile,
    clients: text.settings.clients,
    language: text.nav.language,
    messaging: text.settings.messaging,
    "browser-control": text.settings.browserControl,
    "ai-platform-login": text.settings.aiPlatformLogin,
    "browser-email": text.settings.browserEmail,
    info: text.settings.info,
    localmind: text.settings.localMind,
    "external-mcp": text.settings.externalMCP,
    "tool-policy": text.settings.toolPolicy,
    models: text.settings.modelProfiles,
    runtime: text.settings.runtimeBoundaries
  };
  return labels[detail];
}

function integrationDirectoryStatus(status: IntegrationStatus | null, failed: boolean, text: CopyText) {
  if (failed) return text.settings.integrationUnavailable;
  if (!status) return text.settings.loadingIntegrations;
  return integrationStateLabel(status.state, text);
}

function connectionCountLabel(connectors: ConnectorStatus[], text: CopyText) {
  const enabled = connectors.filter((item) => item.channel !== "mcp" && item.enabled).length;
  return enabled > 0 ? `${enabled} ${text.settings.active}` : text.common.disabled;
}
