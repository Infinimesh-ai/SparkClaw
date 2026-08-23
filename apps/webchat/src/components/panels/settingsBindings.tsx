// Notification-binding + connector section of the settings panel: per-channel
// enable toggles, bind/refresh/revoke actions, QR / secret-token setup flows,
// and the pending-binding poller. Pure move from settings.tsx.
import { useState } from "react";
import {
  CheckCircle2,
  KeyRound,
  MonitorUp,
  Plus,
  RefreshCw,
  Send,
  Trash2
} from "lucide-react";
import type { ConnectorStatus, NotificationBinding } from "../../api/types";
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
  formatTime,
  isImageLikeQR,
  qrImageSource
} from "../../lib/format";

export function ConnectorBindingSettings({
  connectors,
  notificationBindings,
  text,
  language,
  onStartNotificationBinding,
  onRefreshNotificationBinding,
  onOpenNotificationBindingBrowser,
  onRevokeNotificationBinding,
  onUpdateConnector
}: {
  connectors: ConnectorStatus[];
  notificationBindings: NotificationBinding[];
  text: CopyText;
  language: Language;
  onStartNotificationBinding: (channel: string, botToken?: string) => Promise<void>;
  onRefreshNotificationBinding: (id: string, signal?: AbortSignal) => Promise<NotificationBinding>;
  onOpenNotificationBindingBrowser: (id: string) => Promise<void>;
  onRevokeNotificationBinding: (id: string) => Promise<void>;
  onUpdateConnector: (channel: string, enabled: boolean, expectedVersion: number) => Promise<ConnectorStatus>;
}) {
  const [bindingError, setBindingError] = useState("");
  const [telegramToken, setTelegramToken] = useState("");
  const pendingBindingKey = pendingBindingPollKey(notificationBindings);
  const bindingAction = useAsyncAction({
    clearError: () => setBindingError(""),
    onError: (error) => setBindingError(error instanceof Error ? error.message : text.errors.binding)
  });
  const connectorAction = useAsyncAction({
    clearError: () => setBindingError(""),
    onError: (error) => setBindingError(error instanceof Error ? error.message : text.errors.connectorUpdate)
  });
  const bindingBusy = Boolean(bindingAction.busy);
  const connectorBusy = connectorAction.busy;

  useBindingPolling({
    pendingBindingKey,
    refreshBinding: onRefreshNotificationBinding,
    fallbackError: text.errors.binding,
    onError: setBindingError
  });

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
    <>
      {connectors.filter((item) => item.channel !== "mcp").map(renderNotificationBindingSection)}
      {bindingError && <span className="compactError">{bindingError}</span>}
    </>
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
