import { useCallback, useEffect, useState } from "react";
import type { ReactNode } from "react";
import {
  ArrowLeft,
  Bot,
  Cable,
  Check,
  ChevronRight,
  CircleUserRound,
  Cpu,
  DatabaseZap,
  MessageSquare,
  Network,
  Pencil,
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
import { ExternalMCPSettings } from "../externalMCPSettings";
import { SectionHeader } from "./primitives";
import { ConnectorBindingSettings } from "./settingsBindings";
import { PairedClientsSettings } from "./settingsClients";
import { IntegrationCredentialSettings } from "./settingsIntegrations";
import { integrationStateLabel } from "./settingsIntegrationState";
import { OwnerProfileSettings } from "./settingsOwner";

type SettingsCategory = "account" | "connections" | "agent" | "system";
type SettingsDetail =
  | "owner"
  | "clients"
  | "messaging"
  | "info"
  | "localmind"
  | "external-mcp"
  | "tool-policy"
  | "models"
  | "runtime";

export function SettingsPanel({
  runtimeConfig,
  ownerProfile,
  clients,
  connectors,
  notificationBindings,
  text,
  language,
  onUpdateOwner,
  onRevokeClient,
  onStartNotificationBinding,
  onRefreshNotificationBinding,
  onOpenNotificationBindingBrowser,
  onRevokeNotificationBinding,
  onUpdateConnector,
  onUpdatePolicy
}: {
  runtimeConfig: PublicConfig | null;
  ownerProfile: OwnerProfile | null;
  clients: Client[];
  connectors: ConnectorStatus[];
  notificationBindings: NotificationBinding[];
  text: CopyText;
  language: Language;
  onUpdateOwner: (displayName: string, email: string, preferences: Record<string, string>) => Promise<void>;
  onRevokeClient: (id: string) => Promise<void>;
  onStartNotificationBinding: (channel: string, botToken?: string) => Promise<void>;
  onRefreshNotificationBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  onOpenNotificationBindingBrowser: (id: string) => Promise<void>;
  onRevokeNotificationBinding: (id: string) => Promise<void>;
  onUpdateConnector: (channel: string, enabled: boolean, expectedVersion: number) => Promise<ConnectorStatus>;
  onUpdatePolicy: (deny: string[], approvalRequired: string[]) => Promise<void>;
}) {
  const [category, setCategory] = useState<SettingsCategory>("connections");
  const [detail, setDetail] = useState<SettingsDetail | null>(null);
  const [integrations, setIntegrations] = useState<IntegrationStatus[]>([]);
  const [integrationLoadFailed, setIntegrationLoadFailed] = useState(false);
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const policyAction = useAsyncAction();
  const savingPolicy = Boolean(policyAction.busy);

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

  const updateIntegration = useCallback((next: IntegrationStatus) => {
    setIntegrations((current) => [next, ...current.filter((item) => item.id !== next.id)]);
    setIntegrationLoadFailed(false);
  }, []);

  if (!runtimeConfig) {
    return (
      <div className="panelStack">
        <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
        <span className="muted">{text.settings.unavailable}</span>
      </div>
    );
  }

  const policy = runtimeConfig.tool_policy;
  const riskCounts = Object.entries(policy.risk_counts).sort(([left], [right]) => left.localeCompare(right));
  const infoStatus = integrations.find((item) => item.id === "infinimesh-info") ?? null;
  const localMindStatus = integrations.find((item) => item.id === "localmind") ?? null;

  function startPolicyEdit() {
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

  function changeCategory(next: SettingsCategory) {
    setCategory(next);
    setDetail(null);
  }

  const detailTitle = detail ? settingsDetailTitle(detail, text) : "";

  return (
    <div className="panelStack settingsPanel">
      <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
      <div className="settingsCategoryTabs" role="tablist" aria-label={text.settings.categories}>
        <CategoryTab selected={category === "account"} label={text.settings.account} icon={<CircleUserRound size={15} />} onClick={() => changeCategory("account")} />
        <CategoryTab selected={category === "connections"} label={text.settings.connections} icon={<Cable size={15} />} onClick={() => changeCategory("connections")} />
        <CategoryTab selected={category === "agent"} label={text.settings.agent} icon={<Bot size={15} />} onClick={() => changeCategory("agent")} />
        <CategoryTab selected={category === "system"} label={text.settings.system} icon={<ServerCog size={15} />} onClick={() => changeCategory("system")} />
      </div>

      {detail ? (
        <div className="settingsDetailView">
          <button className="settingsBack" type="button" onClick={() => setDetail(null)} title={text.common.back}>
            <ArrowLeft size={16} />
            <span>{detailTitle}</span>
          </button>
          {detail === "owner" && <OwnerProfileSettings ownerProfile={ownerProfile} text={text} onUpdateOwner={onUpdateOwner} />}
          {detail === "clients" && <PairedClientsSettings clients={clients} text={text} language={language} onRevokeClient={onRevokeClient} />}
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
          {category === "account" && (
            <>
              <DirectoryRow icon={<CircleUserRound size={17} />} title={text.settings.ownerProfile} status={ownerProfile?.display_name || text.settings.ownerUnavailable} onClick={() => setDetail("owner")} />
              <DirectoryRow icon={<Users size={17} />} title={text.settings.pairedClients} status={String(clients.length)} onClick={() => setDetail("clients")} />
            </>
          )}
          {category === "connections" && (
            <>
              <DirectoryRow icon={<MessageSquare size={17} />} title={text.settings.messaging} status={connectionCountLabel(connectors, text)} onClick={() => setDetail("messaging")} />
              <DirectoryRow icon={<DatabaseZap size={17} />} title={text.settings.info} status={integrationDirectoryStatus(infoStatus, integrationLoadFailed, text)} onClick={() => setDetail("info")} />
              <DirectoryRow icon={<Network size={17} />} title={text.settings.localMind} status={integrationDirectoryStatus(localMindStatus, integrationLoadFailed, text)} onClick={() => setDetail("localmind")} />
              <DirectoryRow icon={<Cable size={17} />} title={text.settings.externalMCP} status={connectors.find((item) => item.channel === "mcp")?.enabled ? text.settings.active : text.common.disabled} onClick={() => setDetail("external-mcp")} />
            </>
          )}
          {category === "agent" && (
            <>
              <DirectoryRow icon={<ShieldCheck size={17} />} title={text.settings.toolPolicy} status={`${policy.definition_count} ${text.trace.tools}`} onClick={() => setDetail("tool-policy")} />
              <DirectoryRow icon={<Cpu size={17} />} title={text.settings.modelProfiles} status={runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel} onClick={() => setDetail("models")} />
            </>
          )}
          {category === "system" && <DirectoryRow icon={<ServerCog size={17} />} title={text.settings.runtimeBoundaries} status={runtimeConfig.state.backend} onClick={() => setDetail("runtime")} />}
        </div>
      )}
    </div>
  );
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
    clients: text.settings.pairedClients,
    messaging: text.settings.messaging,
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
