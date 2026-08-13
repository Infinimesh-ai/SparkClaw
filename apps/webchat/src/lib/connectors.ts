import type { ConnectorStatus, NotificationBinding } from "../api/types";
import { isBindingPending, isVisibleNotificationBinding, sortNotificationBindings } from "./format";

export function isBindingSetupPending(binding: NotificationBinding) {
  return isBindingPending(binding.status) || (
    binding.channel === "telegram" &&
    binding.status === "active" &&
    !binding.external_user_id &&
    !binding.context_token
  );
}

export function pendingBindingPollKey(bindings: NotificationBinding[]) {
  return JSON.stringify(
    bindings
      .filter(isBindingSetupPending)
      .map((binding) => binding.id)
      .sort()
  );
}

export function bindingsForConnector(bindings: NotificationBinding[], channel: string) {
  return sortNotificationBindings(bindings.filter((binding) => binding.channel === channel && isVisibleNotificationBinding(binding.status)));
}

export function connectorBindingStartDisabled(connector: ConnectorStatus, busy: boolean, hasSecret: boolean) {
  if (busy || !connector.enabled || !connector.binding_startable) return true;
  return connector.setup_kind === "secret" && !hasSecret;
}
