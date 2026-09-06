package app

// Integration states are the bounded public vocabulary shared by every
// credential-backed household integration surface (Browser control,
// Infinimesh Info, LocalMind). Each surface reports the subset it can reach;
// the WebChat renders one label table for all of them.
const (
	IntegrationStateNotConfigured          = "not_configured"
	IntegrationStateConfigured             = "configured"
	IntegrationStateChecking               = "checking"
	IntegrationStateReady                  = "ready"
	IntegrationStateNeedsAttention         = "needs_attention"
	IntegrationStateTemporarilyUnavailable = "temporarily_unavailable"
	IntegrationStateVaultUnavailable       = "vault_unavailable"
)
