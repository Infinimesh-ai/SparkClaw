import { useState } from "react";
import {
  Check,
  CheckCircle2,
  KeyRound,
  MonitorUp,
  Pencil,
  Plus,
  RefreshCw,
  Send,
  Settings,
  Trash2,
  UserRound,
  X
} from "lucide-react";
import type {
  Client,
  ConnectorStatus,
  NotificationBinding,
  OwnerProfile,
  PublicConfig
} from "../../api/types";
import type { Copy as CopyText, Language } from "../../i18n";
import { useAsyncAction } from "../../hooks/useAsyncAction";
import { useBindingPolling } from "../../hooks/useBindingPolling";
import {
  bindingsForConnector,
  connectorBindingStartDisabled,
  isBindingSetupPending,
  pendingBindingPollKey
} from "../../lib/connectors";
import {
  bindingStatusLabel,
  formatPreferences,
  formatRisk,
  formatTime,
  isImageLikeQR,
  parsePreferences,
  parseToolList,
  profileLabel,
  qrImageSource,
  rateLimitLabel,
  retentionLabel
} from "../../lib/format";
import { ExternalMCPSettings } from "../externalMCPSettings";
import { SectionHeader } from "./primitives";

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
  const [editingOwner, setEditingOwner] = useState(false);
  const [ownerName, setOwnerName] = useState("");
  const [ownerEmail, setOwnerEmail] = useState("");
  const [ownerPrefsText, setOwnerPrefsText] = useState("");
  const [ownerError, setOwnerError] = useState("");
  const [editingPolicy, setEditingPolicy] = useState(false);
  const [denyText, setDenyText] = useState("");
  const [approvalText, setApprovalText] = useState("");
  const [bindingError, setBindingError] = useState("");
  const [telegramToken, setTelegramToken] = useState("");
  const pendingBindingKey = pendingBindingPollKey(notificationBindings);
  const ownerAction = useAsyncAction({
    clearError: () => setOwnerError(""),
    onError: (error) => setOwnerError(error instanceof Error ? error.message : text.errors.ownerUpdate)
  });
  const policyAction = useAsyncAction();
  const clientAction = useAsyncAction();
  const bindingAction = useAsyncAction({
    clearError: () => setBindingError(""),
    onError: (error) => setBindingError(error instanceof Error ? error.message : text.errors.binding)
  });
  const connectorAction = useAsyncAction({
    clearError: () => setBindingError(""),
    onError: (error) => setBindingError(error instanceof Error ? error.message : text.errors.connectorUpdate)
  });
  const savingOwner = Boolean(ownerAction.busy);
  const savingPolicy = Boolean(policyAction.busy);
  const revokingClient = clientAction.busy;
  const bindingBusy = Boolean(bindingAction.busy);
  const connectorBusy = connectorAction.busy;

  useBindingPolling({
    pendingBindingKey,
    refreshBinding: onRefreshNotificationBinding,
    fallbackError: text.errors.binding,
    onError: setBindingError
  });

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
  const preferences = ownerProfile?.preferences ?? {};

  function startOwnerEdit() {
    setOwnerName(ownerProfile?.display_name ?? "");
    setOwnerEmail(ownerProfile?.email ?? "");
    setOwnerPrefsText(formatPreferences(preferences));
    setOwnerError("");
    setEditingOwner(true);
  }

  function cancelOwnerEdit() {
    setEditingOwner(false);
    setOwnerName("");
    setOwnerEmail("");
    setOwnerPrefsText("");
    setOwnerError("");
  }

  async function saveOwnerEdit() {
    await ownerAction.run("owner", async () => {
      await onUpdateOwner(ownerName, ownerEmail, parsePreferences(ownerPrefsText, text));
      cancelOwnerEdit();
    });
  }

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

  async function revokeClient(id: string) {
    await clientAction.run(id, async () => {
      await onRevokeClient(id);
    });
  }

  async function startBinding(channel: string) {
    const connector = connectors.find((item) => item.channel === channel);
    const needsSecret = connector?.setup_kind === "secret";
    const botToken = needsSecret ? telegramToken.trim() : "";
    if (needsSecret && !botToken) {
      setBindingError(text.settings.telegramTokenRequired);
      return;
    }
    await bindingAction.run(`start:${channel}`, async () => {
      await onStartNotificationBinding(channel, botToken);
      if (needsSecret) {
        setTelegramToken("");
      }
    });
  }

  async function toggleConnector(connector: ConnectorStatus) {
    await connectorAction.run(connector.channel, async () => {
      await onUpdateConnector(connector.channel, !connector.enabled, connector.version);
    });
  }

  async function refreshBinding(id: string) {
    await bindingAction.run(`refresh:${id}`, async () => {
      await onRefreshNotificationBinding(id);
    });
  }

  async function openBindingBrowser(id: string) {
    await bindingAction.run(`open:${id}`, async () => {
      await onOpenNotificationBindingBrowser(id);
    });
  }

  async function revokeBinding(id: string) {
    await bindingAction.run(`revoke:${id}`, async () => {
      await onRevokeNotificationBinding(id);
    });
  }

  function renderNotificationBindingSection(connector: ConnectorStatus) {
    const channel = connector.channel;
    const bindings = bindingsForConnector(notificationBindings, channel);
    const isTelegram = channel === "telegram";
    const isSecret = connector.setup_kind === "secret";
    const Icon = isTelegram ? Send : KeyRound;
    const title = isTelegram ? text.settings.telegramBinding : channel === "weixin" ? text.settings.weixinBinding : channel;
    const addTitle = isTelegram ? text.settings.addTelegramBinding : text.settings.addWeixinBinding;
    const bindLabel = isTelegram ? text.settings.bindTelegram : text.settings.bindWeixin;
    const missing = isTelegram ? text.settings.telegramBindingMissing : text.settings.bindingMissing;
    const waitingInstruction = text.settings.scanWeixin;
    const scannedInstruction = text.settings.scannedWeixin;
    const tokenEditable = isSecret && connector.enabled && connector.binding_startable;
    const startDisabled = connectorBindingStartDisabled(connector, bindingBusy || connectorBusy !== "", Boolean(telegramToken.trim()));
    const capabilityNote = connectorStatusLabel(connector, text);
    const toggleTitle = connector.enabled ? text.settings.disableConnector : text.settings.enableConnector;
    return (
      <article className="settingsBlock" key={channel}>
        <div className="approvalTop">
          <span className="settingsTitle">
            <Icon size={15} />
            <strong>{title}</strong>
          </span>
          <div className="buttonRow compactButtons">
            <label className="connectorToggle" title={toggleTitle}>
              <input
                type="checkbox"
                checked={connector.enabled}
                onChange={() => void toggleConnector(connector)}
                disabled={connectorBusy !== ""}
                aria-label={toggleTitle}
              />
              <span aria-hidden="true" />
            </label>
            <button className="approve" onClick={() => void startBinding(channel)} disabled={startDisabled} title={addTitle}>
              <Plus size={15} />
            </button>
          </div>
        </div>
        {capabilityNote && <span className="muted bindingCapability">{capabilityNote}</span>}
        {isSecret && (
          <label className="inputGroup compact telegramTokenInput">
            <span>{text.settings.telegramToken}</span>
            <input
              type="password"
              value={telegramToken}
              onChange={(event) => setTelegramToken(event.target.value)}
              placeholder={text.settings.telegramTokenPlaceholder}
              autoComplete="new-password"
              spellCheck={false}
              disabled={bindingBusy || connectorBusy !== "" || !tokenEditable}
            />
          </label>
        )}
        {bindings.length > 0 ? (
          <div className="bindingList">
            {bindings.map((binding) => (
              <div className="bindingItem" key={binding.id}>
                <div className="bindingItemTop">
                  <div>
                    <strong>{binding.display_name || binding.external_user_id || binding.account_id || binding.id}</strong>
                    <span className="muted">{bindingStatusLabel(binding.status, text)}{binding.default_for_channel ? ` · ${text.settings.defaultBinding}` : ""}</span>
                  </div>
                  <div className="buttonRow compactButtons">
                    <button className="edit" onClick={() => void refreshBinding(binding.id)} disabled={bindingBusy || !isBindingSetupPending(binding)} title={text.common.refresh}>
                      <RefreshCw size={15} />
                    </button>
                    <button className="reject" onClick={() => void revokeBinding(binding.id)} disabled={bindingBusy || binding.status === "revoked"} title={text.settings.revokeBinding}>
                      <Trash2 size={15} />
                    </button>
                  </div>
                </div>
                <dl className="statusGrid compact">
                  <dt>{text.settings.bindingProvider}</dt>
                  <dd>{binding.provider}</dd>
                  <dt>{text.settings.bindingAccount}</dt>
                  <dd>{binding.external_user_id || binding.account_id || text.common.notSet}</dd>
                  <dt>{text.settings.bindingContext}</dt>
                  <dd>{binding.context_token || text.common.notSet}</dd>
                  <dt>{text.settings.bindingBaseUrl}</dt>
                  <dd>{binding.base_url || text.common.notSet}</dd>
                  <dt>{text.settings.bindingExpires}</dt>
                  <dd>{binding.expires_at ? formatTime(binding.expires_at, language) : text.common.none}</dd>
                </dl>
                {binding.status === "waiting_scan" && (
                  <div className="bindingQr">
                    {binding.qr_code_image || isImageLikeQR(binding.qr_code_url) ? (
                      <img src={qrImageSource(binding.qr_code_image || binding.qr_code_url)} alt={waitingInstruction} />
                    ) : binding.qr_code_url ? (
                      <button className="secondaryButton" onClick={() => void openBindingBrowser(binding.id)} disabled={bindingBusy}>
                        <MonitorUp size={15} />
                        <span>{text.settings.openWeixinLogin}</span>
                      </button>
                    ) : (
                      <span className="muted">{text.settings.bindingQrUnavailable}</span>
                    )}
                    <small>{waitingInstruction}</small>
                  </div>
                )}
                {binding.status === "waiting_confirm" && !isTelegram && (
                  <div className="bindingScanned">
                    <CheckCircle2 size={18} />
                    <span>{scannedInstruction}</span>
                  </div>
                )}
                {isSecret && binding.status === "active" && !binding.external_user_id && !binding.context_token && (
                  <div className="bindingScanned">
                    <Send size={18} />
                    <span>{text.settings.telegramAwaitingMessage}</span>
                  </div>
                )}
                {binding.last_error && <span className="compactError">{binding.last_error}</span>}
              </div>
            ))}
          </div>
        ) : (
          <div className="bindingEmpty">
            <span className="muted">{missing}</span>
            <button className="secondaryButton" onClick={() => void startBinding(channel)} disabled={startDisabled}>
              <Icon size={15} />
              <span>{bindLabel}</span>
            </button>
          </div>
        )}
      </article>
    );
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
      {connectors.filter((item) => item.channel !== "mcp").map(renderNotificationBindingSection)}
      {bindingError && <span className="compactError">{bindingError}</span>}
      <article className="settingsBlock">
        <div className="approvalTop">
          <span className="settingsTitle">
            <UserRound size={15} />
            <strong>{text.settings.ownerProfile}</strong>
          </span>
          <div className="buttonRow compactButtons">
            {editingOwner ? (
              <>
                <button className="approve" onClick={() => void saveOwnerEdit()} disabled={savingOwner} title={text.settings.saveOwner}>
                  <Check size={15} />
                </button>
                <button className="edit" onClick={cancelOwnerEdit} disabled={savingOwner} title={text.settings.cancelOwner}>
                  <X size={15} />
                </button>
              </>
            ) : (
              <button className="edit" onClick={startOwnerEdit} title={text.settings.editOwner}>
                <Pencil size={15} />
              </button>
            )}
          </div>
        </div>
        {editingOwner ? (
          <div className="ownerEditor">
            <label>
              <span>{text.settings.name}</span>
              <input value={ownerName} onChange={(event) => setOwnerName(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.email}</span>
              <input value={ownerEmail} onChange={(event) => setOwnerEmail(event.target.value)} disabled={savingOwner} />
            </label>
            <label>
              <span>{text.settings.preferences}</span>
              <textarea value={ownerPrefsText} onChange={(event) => setOwnerPrefsText(event.target.value)} disabled={savingOwner} />
            </label>
            {ownerError && <span className="compactError">{ownerError}</span>}
          </div>
        ) : ownerProfile ? (
          <>
            <dl className="statusGrid compact">
              <dt>{text.settings.name}</dt>
              <dd>{ownerProfile.display_name}</dd>
              <dt>{text.settings.email}</dt>
              <dd>{ownerProfile.email || text.common.notSet}</dd>
            </dl>
            <div className="evalCases">
              {Object.entries(preferences).map(([key, value]) => (
                <span key={key}>{key}:{value}</span>
              ))}
              {Object.keys(preferences).length === 0 && <span>{text.common.none}</span>}
            </div>
          </>
        ) : (
          <span className="muted">{text.settings.ownerUnavailable}</span>
        )}
      </article>
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
      <article className="settingsBlock">
        <div className="approvalTop">
          <strong>{text.settings.pairedClients}</strong>
          <span className="pill">{clients.length}</span>
        </div>
        {clients.length === 0 ? (
          <span className="muted">{text.settings.noClients}</span>
        ) : (
          <div className="clientList">
            {clients.map((client) => (
              <div className="clientItem" key={client.id}>
                <div>
                  <strong>{client.name}</strong>
                  <small>
                    {client.revoked_at
                      ? text.common.revoked
                      : client.last_seen_at
                        ? `${text.settings.seen} ${formatTime(client.last_seen_at, language)}`
                        : text.settings.notSeen}
                  </small>
                </div>
                {!client.revoked_at && (
                  <button className="reject" onClick={() => void revokeClient(client.id)} disabled={revokingClient === client.id} title={text.settings.revokeClient}>
                    <Trash2 size={14} />
                  </button>
                )}
              </div>
            ))}
          </div>
        )}
      </article>
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

function connectorStatusLabel(connector: ConnectorStatus, text: CopyText) {
  switch (connector.state) {
    case "disabled":
      return text.settings.connectorDisabled;
    case "unavailable":
      return connector.disabled_reason === "credential_key_unavailable"
        ? text.settings.bindingCredentialUnavailable
        : text.settings.bindingUnavailable;
    case "starting":
      return text.settings.connectorStarting;
    case "setup_required":
      return text.settings.connectorNeedsSetup;
    case "setup_pending":
      return bindingStatusLabel(connector.binding_status, text);
    case "active":
      return text.settings.bound;
    case "error":
      return text.settings.connectorError;
    default:
      return connector.provider;
  }
}
