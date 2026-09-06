import type { IntegrationState } from "../../api/types";
import type { Copy } from "../../i18n";

export function integrationStateLabel(state: IntegrationState, text: Copy) {
  switch (state) {
    case "ready": return text.settings.integrationReady;
    case "configured": return text.settings.integrationConfigured;
    case "checking": return text.settings.integrationChecking;
    case "needs_attention": return text.settings.integrationNeedsAttention;
    case "temporarily_unavailable": return text.settings.integrationTemporarilyUnavailable;
    case "vault_unavailable": return text.settings.integrationVaultUnavailable;
    case "not_configured": return text.settings.integrationNotConfigured;
  }
}
