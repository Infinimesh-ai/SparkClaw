import type { Copy } from "../../i18n";

export function integrationStateLabel(state: string, text: Copy) {
  switch (state) {
    case "ready": return text.settings.integrationReady;
    case "configured": return text.settings.integrationConfigured;
    case "checking": return text.settings.integrationChecking;
    case "needs_attention": return text.settings.integrationNeedsAttention;
    case "temporarily_unavailable": return text.settings.integrationTemporarilyUnavailable;
    case "vault_unavailable": return text.settings.integrationVaultUnavailable;
    default: return text.settings.integrationNotConfigured;
  }
}
