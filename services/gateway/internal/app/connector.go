package app

import "time"

const (
	ConnectorSetupQR       = "qr"
	ConnectorSetupSecret   = "secret"
	ConnectorSetupExternal = "external"

	ConnectorStateDisabled      = "disabled"
	ConnectorStateUnavailable   = "unavailable"
	ConnectorStateStarting      = "starting"
	ConnectorStateSetupRequired = "setup_required"
	ConnectorStateSetupPending  = "setup_pending"
	ConnectorStateActive        = "active"
	ConnectorStateError         = "error"
)

// ConnectorStatus is the provider-neutral control-plane projection consumed
// by Gateway and WebChat. Runtime state is computed; only ConnectorSetting is
// durable.
type ConnectorStatus struct {
	Channel                  string    `json:"channel"`
	Provider                 string    `json:"provider"`
	SetupKind                string    `json:"setup_kind"`
	Available                bool      `json:"available"`
	Enabled                  bool      `json:"enabled"`
	Running                  bool      `json:"running"`
	State                    string    `json:"state"`
	BindingStatus            string    `json:"binding_status"`
	BindingStartable         bool      `json:"binding_startable"`
	SupportsMultipleBindings bool      `json:"supports_multiple_bindings"`
	DisabledReason           string    `json:"disabled_reason,omitempty"`
	LastError                string    `json:"last_error,omitempty"`
	ISCPEnabled              bool      `json:"iscp_enabled,omitempty"`
	LANAccessEnabled         bool      `json:"lan_access_enabled,omitempty"`
	Version                  int64     `json:"version"`
	UpdatedAt                time.Time `json:"updated_at,omitempty"`
}
