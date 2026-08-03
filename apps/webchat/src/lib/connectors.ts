import type { ConnectorStatus, NotificationBinding } from "../api/types";
import { isVisibleNotificationBinding, sortNotificationBindings } from "./format";

export function bindingsForConnector(bindings: NotificationBinding[], channel: string) {
  return sortNotificationBindings(bindings.filter((binding) => binding.channel === channel && isVisibleNotificationBinding(binding.status)));
}

export function connectorBindingStartDisabled(connector: ConnectorStatus, busy: boolean, hasSecret: boolean) {
  if (busy || !connector.enabled || !connector.binding_startable) return true;
  return connector.setup_kind === "secret" && !hasSecret;
}
