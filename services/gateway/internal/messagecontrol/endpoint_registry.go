package messagecontrol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type endpointStore interface {
	GetSession(context.Context, string) (app.Session, bool, error)
	GetNotificationBinding(context.Context, string) (app.NotificationBinding, bool, error)
	GetExternalChatSession(context.Context, string) (app.ExternalChatSession, bool, error)
	ListExternalChatSessions(context.Context, string, string) ([]app.ExternalChatSession, error)
}

type mcpEndpointStore interface {
	GetMCPBinding(string) (app.MCPBinding, bool)
}

type EndpointRegistry struct {
	store          endpointStore
	channelEnabled func(ownerID, channel string) bool
}

func NewEndpointRegistry(st endpointStore) *EndpointRegistry {
	return &EndpointRegistry{store: st}
}

func (r *EndpointRegistry) WithChannelEnabled(enabled func(ownerID, channel string) bool) *EndpointRegistry {
	r.channelEnabled = enabled
	return r
}

func WebEndpointID(sessionID string) app.EndpointID {
	return app.EndpointID("session:" + strings.TrimSpace(sessionID))
}

func BindingEndpointID(bindingID string) app.EndpointID {
	return app.EndpointID(strings.TrimSpace(bindingID))
}

func (r *EndpointRegistry) Get(ctx context.Context, id app.EndpointID) (app.MessageEndpoint, error) {
	return r.get(ctx, id, false)
}

// GetAdmittedSource resolves a source endpoint for work admitted while its
// connector was enabled. It preserves binding and identity checks but does not
// reapply a later owner opt-out to the frozen return path.
func (r *EndpointRegistry) GetAdmittedSource(ctx context.Context, id app.EndpointID) (app.MessageEndpoint, error) {
	return r.get(ctx, id, true)
}

func (r *EndpointRegistry) get(ctx context.Context, id app.EndpointID, admittedSource bool) (app.MessageEndpoint, error) {
	if r == nil || r.store == nil {
		return app.MessageEndpoint{}, errors.New("endpoint registry is unavailable")
	}
	value := strings.TrimSpace(string(id))
	if value == "" {
		return app.MessageEndpoint{}, errors.New("endpoint id is required")
	}
	if strings.HasPrefix(value, "session:") {
		sessionID := strings.TrimSpace(strings.TrimPrefix(value, "session:"))
		session, ok, err := r.store.GetSession(ctx, sessionID)
		if err != nil {
			return app.MessageEndpoint{}, fmt.Errorf("read web endpoint %q: %w", value, err)
		}
		if !ok {
			return app.MessageEndpoint{}, fmt.Errorf("web endpoint %q is unavailable", value)
		}
		ownerID := strings.TrimSpace(session.OwnerID)
		if ownerID == "" {
			ownerID = app.DefaultOwnerID
		}
		return app.MessageEndpoint{
			ID:        id,
			OwnerID:   ownerID,
			ActorID:   ownerID,
			Kind:      app.EndpointKindWeb,
			SessionID: session.ID,
			Status:    app.EndpointActive,
			CreatedAt: session.CreatedAt,
			UpdatedAt: session.UpdatedAt,
		}, nil
	}
	if strings.HasPrefix(value, "mcp:") {
		bindingID := strings.TrimSpace(strings.TrimPrefix(value, "mcp:"))
		mcpStore, supported := r.store.(mcpEndpointStore)
		if !supported {
			return app.MessageEndpoint{}, fmt.Errorf("MCP endpoint %q is unavailable", value)
		}
		binding, ok := mcpStore.GetMCPBinding(bindingID)
		if !ok || binding.SchemaVersion != app.MCPBindingSchemaVersion || binding.Scope != app.MCPAccessConversation || binding.Status != app.MCPBindingActive {
			return app.MessageEndpoint{}, fmt.Errorf("MCP endpoint %q is unavailable", value)
		}
		if !admittedSource && !r.connectorEnabled(binding.OwnerID, "mcp") {
			return app.MessageEndpoint{}, newTargetError(CodeConnectorDisabled, "delivery connector is disabled")
		}
		return app.MessageEndpoint{
			ID: id, OwnerID: binding.OwnerID, ActorID: binding.ActorID,
			SourceActorID: binding.RequesterDeviceID, Kind: app.EndpointKindThirdPartyDevice,
			ProviderKey: "mcp", BindingRef: binding.ID, RequesterDeviceID: binding.RequesterDeviceID,
			Address: binding.RequesterDeviceID, ThreadRef: binding.LatestISCPSessionID, SessionID: binding.LinkedSessionID,
			SoftwareDisplayName: "MCP", AccountDisplayName: binding.RequesterDeviceID,
			RecipientDisplayName: binding.RequesterDeviceID, ConversationLabel: "MCP session",
			Status: app.EndpointActive, CreatedAt: binding.CreatedAt, UpdatedAt: binding.UpdatedAt,
		}, nil
	}
	chat, chatOK, err := r.store.GetExternalChatSession(ctx, value)
	if err != nil {
		return app.MessageEndpoint{}, fmt.Errorf("read external chat endpoint %q: %w", value, err)
	}
	if chatOK {
		return r.endpointForChat(ctx, id, chat, admittedSource)
	}
	binding, ok, err := r.store.GetNotificationBinding(ctx, value)
	if err != nil {
		return app.MessageEndpoint{}, fmt.Errorf("read third-party endpoint %q: %w", value, err)
	}
	if !ok || strings.TrimSpace(binding.Status) != string(app.EndpointActive) {
		return app.MessageEndpoint{}, fmt.Errorf("third-party endpoint %q is unavailable", value)
	}
	providerKey := strings.ToLower(strings.TrimSpace(binding.Channel))
	if providerKey == "" {
		return app.MessageEndpoint{}, fmt.Errorf("third-party endpoint %q has no provider registration", value)
	}
	ownerID := strings.TrimSpace(binding.OwnerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	if !admittedSource && !r.connectorEnabled(ownerID, providerKey) {
		return app.MessageEndpoint{}, newTargetError(CodeConnectorDisabled, "delivery connector is disabled")
	}
	return app.MessageEndpoint{
		ID:                   id,
		OwnerID:              ownerID,
		ActorID:              firstEndpointValue(binding.ActorID, ownerID),
		SourceActorID:        ownerID,
		Kind:                 app.EndpointKindThirdPartyDevice,
		ProviderKey:          providerKey,
		BindingRef:           binding.ID,
		Address:              firstEndpointValue(binding.ExternalChatID, binding.ExternalUserID),
		ThreadRef:            binding.ExternalThreadID,
		ContextRef:           binding.ContextToken,
		SoftwareDisplayName:  softwareDisplayName(providerKey),
		AccountDisplayName:   firstEndpointValue(binding.DisplayName, binding.AccountID, providerKey),
		RecipientDisplayName: firstEndpointValue(binding.DisplayName, "Recipient"),
		ConversationLabel:    firstEndpointValue(binding.DisplayName, binding.AccountID, providerKey),
		Status:               app.EndpointActive,
		CreatedAt:            binding.CreatedAt,
		UpdatedAt:            binding.UpdatedAt,
	}, nil
}

func (r *EndpointRegistry) endpointForChat(ctx context.Context, id app.EndpointID, chat app.ExternalChatSession, admittedSource bool) (app.MessageEndpoint, error) {
	if chat.Status != string(app.EndpointActive) {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "delivery endpoint is inactive")
	}
	binding, ok, err := r.store.GetNotificationBinding(ctx, chat.BindingID)
	if err != nil {
		return app.MessageEndpoint{}, fmt.Errorf("read delivery binding: %w", err)
	}
	if !ok || !bindingUsable(binding, time.Now().UTC()) {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "delivery binding is unavailable")
	}
	providerKey := strings.ToLower(firstEndpointValue(chat.Channel, binding.Channel))
	externalUser := strings.TrimSpace(chat.ExternalUserID)
	externalChat := strings.TrimSpace(chat.ExternalChatID)
	if externalUser == "" || externalChat == "" || providerKey == "" {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "delivery endpoint is incomplete")
	}
	ownerID := firstEndpointValue(chat.AuthorizedOwnerID, binding.OwnerID)
	actorID := firstEndpointValue(chat.AuthorizedActorID, binding.ActorID, ownerID)
	if ownerID == "" || actorID == "" {
		return app.MessageEndpoint{}, newTargetError(CodeCrossUserDenied, "delivery endpoint authorization is incomplete")
	}
	if !admittedSource && !r.connectorEnabled(ownerID, providerKey) {
		return app.MessageEndpoint{}, newTargetError(CodeConnectorDisabled, "delivery connector is disabled")
	}
	accountName := firstEndpointValue(binding.DisplayName, binding.AccountID, providerKey)
	recipientName := firstEndpointValue(chat.DisplayName, "Recipient")
	conversation := accountName
	if chat.ExternalThreadID != "" {
		conversation += " / thread"
	}
	return app.MessageEndpoint{
		ID: id, OwnerID: ownerID, ActorID: actorID, SourceActorID: firstEndpointValue(chat.OwnerID, actorID),
		Kind: app.EndpointKindThirdPartyDevice, ProviderKey: providerKey, BindingRef: binding.ID,
		ExternalUserRef: externalUser, Address: externalChat, ThreadRef: chat.ExternalThreadID,
		ContextRef: chat.LastContextToken, SessionID: chat.LinkedSessionID,
		SoftwareDisplayName: softwareDisplayName(providerKey), AccountDisplayName: accountName,
		RecipientDisplayName: recipientName, ConversationLabel: conversation,
		Status: app.EndpointActive, CreatedAt: chat.CreatedAt, UpdatedAt: chat.UpdatedAt,
	}, nil
}

func (r *EndpointRegistry) List(ctx context.Context, ownerID, actorID string) ([]app.MessageEndpoint, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("endpoint registry is unavailable")
	}
	ownerID = strings.TrimSpace(ownerID)
	actorID = strings.TrimSpace(actorID)
	if ownerID == "" || actorID == "" {
		return nil, newTargetError(CodeCrossUserDenied, "delivery owner and actor are required")
	}
	endpoints := []app.MessageEndpoint{}
	chats, err := r.store.ListExternalChatSessions(ctx, "", string(app.EndpointActive))
	if err != nil {
		return nil, fmt.Errorf("list external chat endpoints: %w", err)
	}
	for _, chat := range chats {
		binding, ok, err := r.store.GetNotificationBinding(ctx, chat.BindingID)
		if err != nil {
			return nil, err
		}
		if !ok || !bindingUsable(binding, time.Now().UTC()) || !app.BindingAllowsMessagingScope(binding.Scopes, app.BindingScopeMessageSendSelf) {
			continue
		}
		endpoint, err := r.endpointForChat(ctx, app.EndpointID(chat.ID), chat, false)
		if err != nil || endpoint.OwnerID != ownerID || endpoint.ActorID != actorID {
			continue
		}
		endpoints = append(endpoints, endpoint)
	}
	slices.SortFunc(endpoints, func(a, b app.MessageEndpoint) int {
		if byProvider := strings.Compare(a.ProviderKey, b.ProviderKey); byProvider != 0 {
			return byProvider
		}
		if byRecipient := strings.Compare(strings.ToLower(a.RecipientDisplayName), strings.ToLower(b.RecipientDisplayName)); byRecipient != 0 {
			return byRecipient
		}
		return strings.Compare(string(a.ID), string(b.ID))
	})
	return endpoints, nil
}

type TargetRequest struct {
	OwnerID          string
	ActorID          string
	ExternalIntent   bool
	WebSessionID     string
	SourceEndpointID app.EndpointID
	ProviderKey      string
	RecipientText    string
}

func (r *EndpointRegistry) ResolveTarget(ctx context.Context, request TargetRequest) (app.DeliveryTargetSelection, error) {
	if request.SourceEndpointID != "" {
		endpoint, err := r.Get(ctx, request.SourceEndpointID)
		if err != nil {
			return app.DeliveryTargetSelection{Status: app.TargetUnavailable, ResolutionRule: "frozen_source_unavailable"}, err
		}
		if endpoint.SourceActorID != strings.TrimSpace(request.ActorID) {
			return app.DeliveryTargetSelection{Status: app.TargetUnavailable, ResolutionRule: "frozen_source_actor_mismatch"}, newTargetError(CodeCrossUserDenied, "source endpoint does not belong to the actor")
		}
		return app.DeliveryTargetSelection{Status: app.TargetSourceReply, ResolvedEndpointID: endpoint.ID, ResolutionRule: "frozen_source_endpoint"}, nil
	}
	if !request.ExternalIntent {
		id := WebEndpointID(request.WebSessionID)
		endpoint, err := r.Get(ctx, id)
		if err != nil || endpoint.OwnerID != strings.TrimSpace(request.OwnerID) {
			return app.DeliveryTargetSelection{Status: app.TargetUnavailable, ResolutionRule: "current_web_session_unavailable"}, newTargetError(CodeCrossUserDenied, "current Web session is unavailable")
		}
		return app.DeliveryTargetSelection{Status: app.TargetDefaultWeb, ResolvedEndpointID: id, ResolutionRule: "current_web_session"}, nil
	}
	provider := strings.ToLower(strings.TrimSpace(request.ProviderKey))
	selection := app.DeliveryTargetSelection{
		RequestedProviderKey: provider, RequestedRecipientText: strings.TrimSpace(request.RecipientText),
	}
	if provider == "" {
		selection.Status, selection.ResolutionRule = app.TargetNeedsChannel, "explicit_external_channel_required"
		return selection, nil
	}
	all, err := r.List(ctx, request.OwnerID, request.ActorID)
	if err != nil {
		return selection, err
	}
	candidates := []app.MessageEndpoint{}
	for _, endpoint := range all {
		if strings.EqualFold(endpoint.ProviderKey, provider) || strings.EqualFold(endpoint.SoftwareDisplayName, provider) {
			candidates = append(candidates, endpoint)
		}
	}
	recipient := strings.TrimSpace(request.RecipientText)
	if recipient != "" {
		matched := candidates[:0]
		for _, endpoint := range candidates {
			if endpointMatchesRecipient(endpoint, recipient) {
				matched = append(matched, endpoint)
			}
		}
		candidates = matched
	}
	for _, endpoint := range candidates {
		selection.CandidateEndpointIDs = append(selection.CandidateEndpointIDs, endpoint.ID)
	}
	switch len(candidates) {
	case 0:
		selection.Status = app.TargetUnavailable
		if recipient == "" {
			selection.ResolutionRule = "software_has_no_eligible_endpoint"
		} else {
			selection.ResolutionRule = "recipient_not_found"
		}
	case 1:
		selection.Status, selection.ResolvedEndpointID = app.TargetResolved, candidates[0].ID
		if recipient == "" {
			selection.ResolutionRule = "sole_endpoint_in_explicit_software"
		} else {
			selection.ResolutionRule = "exact_recipient_match"
		}
	default:
		if recipient == "" {
			selection.Status, selection.ResolutionRule = app.TargetNeedsRecipient, "software_has_multiple_endpoints"
		} else {
			selection.Status, selection.ResolutionRule = app.TargetAmbiguous, "recipient_matches_multiple_endpoints"
		}
	}
	return selection, nil
}

func (r *EndpointRegistry) GetForMessageSend(ctx context.Context, id app.EndpointID, ownerID, actorID string) (app.MessageEndpoint, error) {
	if r == nil || r.store == nil {
		return app.MessageEndpoint{}, errors.New("endpoint registry is unavailable")
	}
	value := strings.TrimSpace(string(id))
	chat, ok, err := r.store.GetExternalChatSession(ctx, value)
	if err != nil {
		return app.MessageEndpoint{}, fmt.Errorf("read external chat endpoint %q: %w", value, err)
	}
	if !ok {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "message delivery requires an exact recipient endpoint")
	}
	endpoint, err := r.endpointForChat(ctx, id, chat, false)
	if err != nil {
		return app.MessageEndpoint{}, err
	}
	if endpoint.Kind != app.EndpointKindThirdPartyDevice || endpoint.OwnerID != strings.TrimSpace(ownerID) || endpoint.ActorID != strings.TrimSpace(actorID) {
		return app.MessageEndpoint{}, newTargetError(CodeCrossUserDenied, "delivery endpoint is outside the actor scope")
	}
	if endpoint.BindingRef == "" || endpoint.ProviderKey == "" || endpoint.ExternalUserRef == "" || endpoint.Address == "" {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "direct delivery endpoint is incomplete")
	}
	binding, ok, err := r.store.GetNotificationBinding(ctx, endpoint.BindingRef)
	if err != nil {
		return app.MessageEndpoint{}, fmt.Errorf("read delivery binding: %w", err)
	}
	if !ok || !bindingUsable(binding, time.Now().UTC()) {
		return app.MessageEndpoint{}, newTargetError(CodeBindingUnavailable, "delivery binding is unavailable")
	}
	if !app.BindingAllowsMessagingScope(binding.Scopes, app.BindingScopeMessageSendSelf) {
		return app.MessageEndpoint{}, newTargetError(CodeScopeDenied, "delivery binding lacks ordinary message scope")
	}
	return endpoint, nil
}

const (
	CodeBindingUnavailable = app.DeliveryCodeBindingUnavailable
	CodeConnectorDisabled  = app.DeliveryCodeConnectorDisabled
	CodeScopeDenied        = app.DeliveryCodeScopeDenied
	CodeCrossUserDenied    = app.DeliveryCodeCrossUserDenied
)

type TargetError struct {
	Code    string
	Message string
}

func (e *TargetError) Error() string     { return e.Message }
func (e *TargetError) ErrorCode() string { return e.Code }

func newTargetError(code, message string) error { return &TargetError{Code: code, Message: message} }

func (r *EndpointRegistry) connectorEnabled(ownerID, channel string) bool {
	if channel == "mcp" {
		return r.channelEnabled != nil && r.channelEnabled(ownerID, channel)
	}
	return r.channelEnabled == nil || r.channelEnabled(ownerID, channel)
}

func bindingUsable(binding app.NotificationBinding, now time.Time) bool {
	return binding.Status == string(app.EndpointActive) && binding.RevokedAt == nil && (binding.ExpiresAt == nil || binding.ExpiresAt.After(now))
}

func endpointMatchesRecipient(endpoint app.MessageEndpoint, recipient string) bool {
	return strings.EqualFold(string(endpoint.ID), recipient) ||
		strings.EqualFold(endpoint.RecipientDisplayName, recipient) ||
		strings.EqualFold(endpoint.ConversationLabel, recipient)
}

func softwareDisplayName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "telegram":
		return "Telegram"
	case "weixin":
		return "Weixin"
	default:
		if provider == "" {
			return "Messaging"
		}
		return strings.ToUpper(provider[:1]) + provider[1:]
	}
}

func firstEndpointValue(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
