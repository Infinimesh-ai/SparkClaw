package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/integrationconfig"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

// The settings domain over the link layer: the owner profile, connectors,
// integration credentials, messaging bindings, the browser extension and
// MCP access. Each method answers with the domain result {status, body}
// the desktop's settings screens read; the owner, connector and
// integration values carry their revision, and a change names the
// revision it was made against (expected_revision).

type settingsRevisionArgs struct {
	ExpectedRevision string `json:"expected_revision,omitempty"`
}

type settingsOwnerPatchArgs struct {
	settingsRevisionArgs
	DisplayName string            `json:"display_name"`
	Email       string            `json:"email"`
	Preferences map[string]string `json:"preferences"`
}

// settingsOwner reads the owner profile, or a new one with its defaults.
func (s *Server) settingsOwner(ctx context.Context, principal requestPrincipal) (app.OwnerProfile, bool, error) {
	profile, found, err := s.store.GetOwnerProfileByID(ctx, principal.OwnerID)
	if err == nil && !found {
		profile = app.OwnerProfile{ID: principal.OwnerID, DisplayName: "Owner", Preferences: map[string]string{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{}}
	}
	return profile, found, err
}

func (s *Server) settingsOwnerPatch(ctx context.Context, principal requestPrincipal, input settingsOwnerPatchArgs) domainReply {
	profile, found, err := s.settingsOwner(ctx, principal)
	if err != nil {
		return replyCode(http.StatusServiceUnavailable, "settings_unavailable")
	}
	if input.ExpectedRevision == "" || input.ExpectedRevision != valueRevision(profile) {
		return replyCode(http.StatusConflict, "revision_conflict")
	}
	candidate, err := normalizeOwnerProfileInput(profile, input.DisplayName, input.Email, input.Preferences)
	if err != nil {
		return replyCode(http.StatusBadRequest, "invalid_input")
	}
	updated, err := s.store.SaveOwnerProfile(store.WithOwnerProfilePrecondition(ctx, found, profile.UpdatedAt), candidate)
	updated, err = store.ReconcileOwnerProfileWrite(ctx, s.store, updated, err)
	if errors.Is(err, store.ErrOwnerProfileConflict) {
		return replyCode(http.StatusConflict, "revision_conflict")
	}
	if err != nil {
		return replyCode(http.StatusServiceUnavailable, "settings_outcome_unknown")
	}
	return replyValue(updated)
}

type settingsConnectorArgs struct {
	settingsRevisionArgs
	Channel         string `json:"channel"`
	Enabled         *bool  `json:"enabled"`
	ExpectedVersion *int64 `json:"expected_version"`
}

func (s *Server) settingsConnectors(ctx context.Context, principal requestPrincipal) domainReply {
	if s.connectors == nil {
		return replyCode(http.StatusServiceUnavailable, "connector_unavailable")
	}
	statuses, err := s.connectors.ListStatus(ctx, principal.OwnerID)
	if err != nil {
		return replyCode(http.StatusServiceUnavailable, "connector_unavailable")
	}
	return replyValue(map[string]any{"connectors": statuses})
}

func (s *Server) settingsConnectorPatch(ctx context.Context, principal requestPrincipal, input settingsConnectorArgs) domainReply {
	if s.connectors == nil {
		return replyCode(http.StatusServiceUnavailable, "connector_unavailable")
	}
	if input.Channel == "" || strings.TrimSpace(input.Channel) != input.Channel || input.Enabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
		return replyCode(http.StatusBadRequest, "invalid_input")
	}
	current, err := s.connectors.Status(ctx, principal.OwnerID, input.Channel)
	if err != nil {
		return replyCode(http.StatusNotFound, "connector_not_found")
	}
	if input.ExpectedRevision != "" && input.ExpectedRevision != valueRevision(current) {
		return replyCode(http.StatusConflict, "revision_conflict")
	}
	updated, err := s.connectors.SetEnabled(ctx, principal.OwnerID, principal.ActorID, input.Channel, *input.Enabled, *input.ExpectedVersion)
	if errors.Is(err, store.ErrConnectorSettingConflict) {
		return replyCode(http.StatusConflict, "revision_conflict")
	}
	if err != nil {
		return replyCode(http.StatusServiceUnavailable, "connector_outcome_unknown")
	}
	return replyValue(updated)
}

func (s *Server) settingsIntegrations(ctx context.Context) domainReply {
	if s.integrations == nil {
		return replyCode(http.StatusServiceUnavailable, "integration_unavailable")
	}
	statuses := s.integrations.List(ctx)
	revisions := map[string]string{}
	for _, status := range statuses {
		revisions[status.ID] = integrationconfig.StatusRevision(status)
	}
	return replyValue(map[string]any{"integrations": statuses, "resource_revisions": revisions})
}

type settingsCredentialArgs struct {
	settingsRevisionArgs
	IntegrationID string `json:"integration_id"`
	CredentialID  string `json:"credential_id"`
	UseOperator   bool   `json:"use_operator"`
}

// settingsCredential changes one integration's credentials against the
// integration revision the screen showed.
func (s *Server) settingsCredential(ctx context.Context, method string, input settingsCredentialArgs, raw json.RawMessage) domainReply {
	controller := s.integrations
	if controller == nil {
		return replyCode(http.StatusServiceUnavailable, "integration_unavailable")
	}
	id := input.IntegrationID
	if id != integrationconfig.InfoID && id != integrationconfig.LocalMindID {
		return replyCode(http.StatusNotFound, "integration_not_found")
	}
	current, err := controller.Get(ctx, id)
	if err != nil {
		return replyCode(http.StatusServiceUnavailable, "integration_unavailable")
	}
	if input.ExpectedRevision == "" || input.ExpectedRevision != integrationconfig.StatusRevision(current) {
		return replyCode(http.StatusConflict, "revision_conflict")
	}
	ctx = integrationconfig.WithStatusPrecondition(ctx, input.ExpectedRevision)
	var status integrationconfig.Status
	switch method {
	case "settings.credentials.add":
		if id == integrationconfig.InfoID {
			var credential integrationconfig.AddInfoCredentialInput
			if json.Unmarshal(raw, &credential) != nil {
				return replyCode(http.StatusBadRequest, "invalid_input")
			}
			status, err = controller.AddInfoCredential(ctx, credential)
		} else {
			var credential integrationconfig.AddLocalMindCredentialInput
			if json.Unmarshal(raw, &credential) != nil {
				return replyCode(http.StatusBadRequest, "invalid_input")
			}
			status, err = controller.AddLocalMindCredential(ctx, credential)
		}
	case "settings.credentials.activate":
		status, err = controller.Activate(ctx, id, input.CredentialID, input.UseOperator)
	case "settings.credentials.check", "settings.credentials.delete":
		if input.CredentialID == "" {
			return replyCode(http.StatusBadRequest, "invalid_input")
		}
		if method == "settings.credentials.check" {
			status, err = controller.Check(ctx, id, input.CredentialID)
		} else {
			status, err = controller.Delete(ctx, id, input.CredentialID)
		}
	}
	if err != nil {
		code := integrationconfig.ErrorCode(err)
		if code == "" {
			code = "integration_unavailable"
		}
		httpStatus := http.StatusBadRequest
		if code == "active_credential_replacement_required" || code == "credential_revision_conflict" {
			httpStatus = http.StatusConflict
		}
		if integrationconfig.ErrorRetryable(err) {
			httpStatus = http.StatusServiceUnavailable
		}
		return replyCode(httpStatus, code)
	}
	return replyValue(status)
}

type settingsBindingArgs struct {
	BindingID string `json:"binding_id"`
}

type settingsBindingsListArgs struct {
	Channel string `json:"channel,omitempty"`
	Status  string `json:"status,omitempty"`
}

type settingsBindingStartArgs struct {
	notificationBindingStart
	Channel string `json:"channel"`
}

type settingsTicketArgs struct {
	TicketID string `json:"ticket_id"`
}

type settingsBrowserTokenArgs struct {
	Token string `json:"token"`
}

func settingsInvalid() domainReply { return replyCode(http.StatusBadRequest, "invalid_input") }

func init() {
	credential := func(method string) linkDomainMethod {
		return linkDomainMethod{Mutation: true, Call: func(ctx context.Context, s *Server, req *link.Request, raw json.RawMessage) (any, error) {
			return linkReply(true, func(ctx context.Context, s *Server, _ requestPrincipal, args settingsCredentialArgs) domainReply {
				return s.settingsCredential(ctx, method, args, raw)
			}).Call(ctx, s, req, raw)
		}}
	}
	// A record id of the binding and MCP methods names one record, never a path.
	bindingCall := func(mutation bool, call func(*Server, context.Context, requestPrincipal, string) domainReply) linkDomainMethod {
		return linkReply(mutation, func(ctx context.Context, s *Server, p requestPrincipal, args settingsBindingArgs) domainReply {
			if !settingsBindingID(args.BindingID) {
				return settingsInvalid()
			}
			return call(s, ctx, p, args.BindingID)
		})
	}
	mcpRecord := func(call func(*Server, context.Context, requestPrincipal, string) domainReply, ticket bool) linkDomainMethod {
		if ticket {
			return linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args settingsTicketArgs) domainReply {
				if !settingsRecordID(args.TicketID) {
					return settingsInvalid()
				}
				return call(s, ctx, p, args.TicketID)
			})
		}
		return linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args settingsBindingArgs) domainReply {
			if !settingsRecordID(args.BindingID) {
				return settingsInvalid()
			}
			return call(s, ctx, p, args.BindingID)
		})
	}
	registerLinkDomain("settings", map[string]linkDomainMethod{
		"settings.owner.get": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			profile, _, err := s.settingsOwner(ctx, p)
			if err != nil {
				return replyCode(http.StatusServiceUnavailable, "settings_unavailable")
			}
			return replyValue(profile)
		}),
		"settings.owner.patch": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args settingsOwnerPatchArgs) domainReply {
			return s.settingsOwnerPatch(ctx, p, args)
		}),
		"settings.connectors.list": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.settingsConnectors(ctx, p)
		}),
		"settings.connectors.patch": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args settingsConnectorArgs) domainReply {
			return s.settingsConnectorPatch(ctx, p, args)
		}),
		"settings.integrations.list": linkReply(false, func(ctx context.Context, s *Server, _ requestPrincipal, _ struct{}) domainReply {
			return s.settingsIntegrations(ctx)
		}),
		"settings.credentials.add":      credential("settings.credentials.add"),
		"settings.credentials.activate": credential("settings.credentials.activate"),
		"settings.credentials.check":    credential("settings.credentials.check"),
		"settings.credentials.delete":   credential("settings.credentials.delete"),

		"settings.bindings.list": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, args settingsBindingsListArgs) domainReply {
			if args.Channel != "" && args.Channel != "weixin" && args.Channel != "telegram" || !settingsBindingStatus(args.Status) {
				return settingsInvalid()
			}
			return s.notificationBindings(ctx, p, args.Channel, args.Status)
		}),
		"settings.bindings.start": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args settingsBindingStartArgs) domainReply {
			if args.Channel != "weixin" && args.Channel != "telegram" || args.Channel == "weixin" && (args.CredentialSecret != "" || args.BotToken != "") {
				return settingsInvalid()
			}
			return s.notificationBindingStart(ctx, p, args.Channel, args.notificationBindingStart)
		}),
		"settings.bindings.get":    bindingCall(false, (*Server).notificationBinding),
		"settings.bindings.poll":   bindingCall(false, (*Server).notificationBindingPoll),
		"settings.bindings.revoke": bindingCall(true, (*Server).notificationBindingRevoke),
		"settings.bindings.login":  bindingCall(false, (*Server).notificationBindingLoginURL),

		"settings.browser.get": linkReply(false, func(ctx context.Context, s *Server, _ requestPrincipal, _ struct{}) domainReply {
			return s.browserExtension(ctx)
		}),
		"settings.browser.save": linkReply(true, func(ctx context.Context, s *Server, _ requestPrincipal, args settingsBrowserTokenArgs) domainReply {
			if args.Token == "" || len(args.Token) > maxBrowserControlRequestBytes {
				return settingsInvalid()
			}
			token := []byte(args.Token)
			defer zeroBrowserControlBytes(token)
			return s.browserExtensionSave(ctx, token)
		}),
		"settings.browser.check": linkReply(true, func(ctx context.Context, s *Server, _ requestPrincipal, _ struct{}) domainReply {
			return s.browserExtensionCheck(ctx)
		}),
		"settings.browser.delete": linkReply(true, func(ctx context.Context, s *Server, _ requestPrincipal, _ struct{}) domainReply {
			return s.browserExtensionRemove(ctx)
		}),

		"settings.mcp.catalog": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.mcpCatalog(ctx, p)
		}),
		"settings.mcp.transports": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mcpTransports) domainReply {
			return s.mcpTransportsUpdate(ctx, p, args)
		}),
		"settings.mcp.tickets.list": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.mcpTickets(ctx, p)
		}),
		"settings.mcp.tickets.issue": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args mcpaccess.IssueTicketRequest) domainReply {
			if len(args.DomainID) > 256 || strings.ContainsAny(args.DomainID, "\x00\r\n") || args.TTLSeconds != 0 && (args.TTLSeconds < 60 || args.TTLSeconds > 86400) {
				return settingsInvalid()
			}
			return s.mcpTicketIssue(ctx, p, args)
		}),
		"settings.mcp.tickets.revoke": mcpRecord((*Server).mcpTicketRevoke, true),
		"settings.mcp.tickets.delete": mcpRecord((*Server).mcpTicketDelete, true),
		"settings.mcp.bindings.list": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.mcpBindings(ctx, p)
		}),
		"settings.mcp.bindings.revoke": mcpRecord((*Server).mcpBindingRevoke, false),
		"settings.mcp.bindings.delete": mcpRecord((*Server).mcpBindingDelete, false),
		"settings.mcp.records.delete": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.mcpRecordsDelete(ctx, p)
		}),
		"settings.mcp.pairing.status": linkReply(false, func(ctx context.Context, s *Server, _ requestPrincipal, _ struct{}) domainReply {
			return s.mcpPairingStatus(ctx)
		}),
		"settings.mcp.pairing.onboardings": linkReply(false, func(ctx context.Context, s *Server, p requestPrincipal, _ struct{}) domainReply {
			return s.mcpPairingOnboardings(ctx, p)
		}),
		"settings.mcp.pairing.start": linkReply(true, func(ctx context.Context, s *Server, p requestPrincipal, args iscppairing.StartRequest) domainReply {
			// iscppairing.Service.Start's own 120-byte display name limit.
			name := strings.TrimSpace(args.DisplayName)
			if name == "" || len(name) > 120 || strings.ContainsAny(args.DisplayName, "\x00\r\n") || args.TTLSeconds != 0 && (args.TTLSeconds < 60 || args.TTLSeconds > 1800) {
				return settingsInvalid()
			}
			return s.mcpPairingStart(ctx, p, args)
		}),
	})
}

func settingsBindingID(id string) bool {
	return id != "" && len(id) <= 256 && strings.TrimSpace(id) == id &&
		!strings.ContainsAny(id, "/\\?#\x00\r\n\t")
}

func settingsBindingStatus(status string) bool {
	switch status {
	case "", app.NotificationBindingStarting, app.NotificationBindingWaitingScan, app.NotificationBindingWaitingConfirm,
		app.NotificationBindingCredentialPending, app.NotificationBindingActive, app.NotificationBindingRevoking,
		app.NotificationBindingFailed, app.NotificationBindingExpired, app.NotificationBindingRevoked:
		return true
	default:
		return false
	}
}

func settingsRecordID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char != '-' && char != '_' && (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}
