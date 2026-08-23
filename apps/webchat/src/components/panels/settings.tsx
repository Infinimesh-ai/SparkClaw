// Settings panel orchestrator. The owner-profile, paired-clients, and
// connector/notification-binding sections live in sibling files
// (settingsOwner.tsx / settingsClients.tsx / settingsBindings.tsx); this file
// keeps the tool-policy editors, model profiles, and runtime boundaries.
import { useState } from "react";
import { Check, Pencil, Settings, X } from "lucide-react";
import type {
  Client,
  ConnectorStatus,
  NotificationBinding,
  OwnerProfile,
  PublicConfig
} from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import {
  formatRisk,
  parseToolList,
  profileLabel,
  rateLimitLabel,
  retentionLabel
} from "../../lib/format";
import { ExternalMCPSettings } from "../externalMCPSettings";
import { SectionHeader } from "./primitives";
import { ConnectorBindingSettings } from "./settingsBindings";
import { PairedClientsSettings } from "./settingsClients";
import { OwnerProfileSettings } from "./settingsOwner";

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
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const policyAction = useAsyncAction();
  const savingPolicy = Boolean(policyAction.busy);

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

  return (
    <div className="panelStack">
      <SectionHeader icon={<Settings size={17} />} title={text.settings.title} />
      <ExternalMCPSettings
		connector={connectors.find((item) => item.channel === "mcp")}
		text={text}
		language={language}
		onUpdateConnector={onUpdateConnector}
	  />
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
      <OwnerProfileSettings ownerProfile={ownerProfile} text={text} onUpdateOwner={onUpdateOwner} />
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
          {riskCounts.map(([risk, count]) => (
            <span key={risk}>{formatRisk(risk, text)}:{count}</span>
          ))}
        </div>
      </article>
      <PairedClientsSettings clients={clients} text={text} language={language} onRevokeClient={onRevokeClient} />
      <article className="settingsBlock">
        <strong>{text.settings.definitionApprovalTools}</strong>
        <div className="evalCases">
          {policy.definition_approval_required_tools.map((tool) => (
            <span key={tool}>{tool}</span>
          ))}
          {policy.definition_approval_required_tools.length === 0 && <span>{text.common.none}</span>}
        </div>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.configApprovalAdditions}</strong>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.approval}</span>
              <textarea value={approvalText} onChange={(event) => setApprovalText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.configured_approval_required_tools.map((tool) => (
              <span key={`configured-${tool}`}>{tool}</span>
            ))}
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
                <button className="approve" onClick={() => void savePolicyEdit()} disabled={savingPolicy} title={text.settings.saveToolPolicy}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelPolicyEdit} disabled={savingPolicy} title={text.settings.cancelPolicy}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startPolicyEdit} title={text.settings.editPolicy}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingPolicy ? (
          <div className="policyEditor">
            <label>
              <span>{text.settings.deny}</span>
              <textarea value={denyText} onChange={(event) => setDenyText(event.target.value)} disabled={savingPolicy} />
            </label>
          </div>
        ) : (
          <div className="evalCases">
            {policy.denied_tools.map((tool) => (
              <span className="failed" key={tool}>{tool}</span>
            ))}
            {policy.denied_tools.length === 0 && <span>{text.common.none}</span>}
          </div>
        )}
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.modelProfiles}</strong>
        <dl className="statusGrid compact">
          <dt>{text.settings.mode}</dt>
          <dd>{runtimeConfig.model.mock ? text.settings.mock : text.settings.externalModel}</dd>
          <dt>{text.settings.fast}</dt>
          <dd>{profileLabel(runtimeConfig.model.fast, text)}</dd>
          <dt>{text.settings.deep}</dt>
          <dd>{profileLabel(runtimeConfig.model.deep, text)}</dd>
          <dt>{text.settings.embed}</dt>
          <dd>{profileLabel(runtimeConfig.model.embedding, text)}</dd>
          <dt>{text.settings.guard}</dt>
          <dd>{profileLabel(runtimeConfig.model.guard, text)}</dd>
        </dl>
      </article>
      <article className="settingsBlock">
        <strong>{text.settings.runtimeBoundaries}</strong>
        <dl className="statusGrid compact">
          <dt>{text.status.gateway}</dt>
          <dd>{runtimeConfig.gateway.bind}:{runtimeConfig.gateway.port}</dd>
          <dt>{text.settings.remote}</dt>
          <dd>{runtimeConfig.gateway.remote_access}</dd>
          <dt>{text.status.rateLimit}</dt>
          <dd>{rateLimitLabel(runtimeConfig.gateway.rate_limit, text)}</dd>
          <dt>{text.status.workspace}</dt>
          <dd>{runtimeConfig.workspaces.default_root}</dd>
          <dt>{text.settings.sandbox}</dt>
          <dd>{runtimeConfig.sandbox.enabled ? `${runtimeConfig.sandbox.backend} · ${runtimeConfig.sandbox.network}` : text.common.disabled}</dd>
          <dt>{text.status.state}</dt>
          <dd>
            {runtimeConfig.state.backend} · {runtimeConfig.state.path || runtimeConfig.state.dsn}
            {runtimeConfig.state.encrypt_at_rest ? ` · ${text.settings.encrypted}` : ""}
          </dd>
          <dt>{text.settings.artifacts}</dt>
          <dd>{runtimeConfig.storage.artifact_backend} · {runtimeConfig.storage.artifact_dir || runtimeConfig.storage.artifact_bucket}</dd>
          <dt>{text.settings.memory}</dt>
          <dd>
            {runtimeConfig.memory.enabled
              ? `${runtimeConfig.memory.write_policy} · ${retentionLabel(runtimeConfig.memory.retention_days, text)}`
              : text.common.disabled}
          </dd>
        </dl>
      </article>
    </div>
  );
}
