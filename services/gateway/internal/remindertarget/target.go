package remindertarget

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type Target struct {
	EndpointID       app.EndpointID
	Recipient        string
	RecipientBinding string
	BindingID        string
	CredentialRef    string
	BaseURL          string
}

type Resolver struct {
	store store.Store
}

func NewResolver(st store.Store) *Resolver {
	return &Resolver{store: st}
}

func (r *Resolver) Resolve(ctx context.Context, channel, sessionID, requestedRecipient string) (Target, error) {
	channel = normalizeChannel(channel)
	if channel == "" || r == nil || r.store == nil {
		return Target{}, errors.New("notification target resolver is unavailable")
	}
	requestedRecipient = strings.TrimSpace(requestedRecipient)
	if chatSession, ok := r.store.FindExternalChatSessionByLinkedSessionID(sessionID); ok && normalizeChannel(chatSession.Channel) == channel {
		binding, bindingOK, err := r.store.GetNotificationBinding(ctx, strings.TrimSpace(chatSession.BindingID))
		if err != nil {
			return Target{}, err
		}
		if !bindingOK || binding.Status != "active" || normalizeChannel(binding.Channel) != channel {
			return Target{}, fmt.Errorf("%s notification binding is unavailable", channel)
		}
		if requestedRecipient != "" && !bindingMatchesRecipient(binding, requestedRecipient) {
			return Target{}, fmt.Errorf("%s notification can only target the current active binding", channel)
		}
		return validateTarget(channel, targetFromSession(chatSession, binding))
	}

	bindings, err := r.store.ListNotificationBindings(ctx, channel, "active")
	if err != nil {
		return Target{}, err
	}
	if len(bindings) == 0 {
		return Target{}, fmt.Errorf("%s notification requires an active binding", channel)
	}
	if requestedRecipient == "" {
		if len(bindings) > 1 {
			return Target{}, fmt.Errorf("multiple %s bindings are active; select one of: %s", channel, describeBindings(bindings))
		}
		return validateTarget(channel, targetFromBinding(bindings[0]))
	}

	matches := make([]app.NotificationBinding, 0, 1)
	for _, binding := range bindings {
		if bindingMatchesRecipient(binding, requestedRecipient) {
			matches = append(matches, binding)
		}
	}
	switch len(matches) {
	case 0:
		return Target{}, fmt.Errorf("no %s binding matches %q; available bindings: %s", channel, requestedRecipient, describeBindings(bindings))
	case 1:
		return validateTarget(channel, targetFromBinding(matches[0]))
	default:
		return Target{}, fmt.Errorf("%s binding %q is ambiguous; select one of: %s", channel, requestedRecipient, describeBindings(matches))
	}
}

func targetFromSession(session app.ExternalChatSession, binding app.NotificationBinding) Target {
	return Target{
		EndpointID:       app.EndpointID(session.ID),
		Recipient:        firstNonEmpty(session.ExternalChatID, session.ExternalUserID),
		RecipientBinding: firstNonEmpty(session.ExternalThreadID, session.LastContextToken),
		BindingID:        strings.TrimSpace(binding.ID),
		CredentialRef:    strings.TrimSpace(binding.CredentialRef),
		BaseURL:          strings.TrimSpace(binding.BaseURL),
	}
}

func targetFromBinding(binding app.NotificationBinding) Target {
	return Target{
		EndpointID:       app.EndpointID(binding.ID),
		Recipient:        firstNonEmpty(binding.ExternalChatID, binding.ExternalUserID),
		RecipientBinding: firstNonEmpty(binding.ExternalThreadID, binding.ContextToken),
		BindingID:        strings.TrimSpace(binding.ID),
		CredentialRef:    strings.TrimSpace(binding.CredentialRef),
		BaseURL:          strings.TrimSpace(binding.BaseURL),
	}
}

func validateTarget(channel string, target Target) (Target, error) {
	if target.BindingID == "" || target.Recipient == "" {
		return Target{}, fmt.Errorf("%s notification binding has no deliverable recipient", channel)
	}
	return target, nil
}

func bindingMatchesRecipient(binding app.NotificationBinding, requested string) bool {
	needle := strings.ToLower(strings.TrimSpace(requested))
	for _, value := range []string{binding.ID, binding.ExternalUserID, binding.ExternalChatID, binding.DisplayName, binding.AccountID} {
		if strings.ToLower(strings.TrimSpace(value)) == needle {
			return true
		}
	}
	return false
}

func describeBindings(bindings []app.NotificationBinding) string {
	parts := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		label := firstNonEmpty(binding.DisplayName, binding.AccountID, binding.ExternalUserID, binding.ExternalChatID, binding.ID)
		parts = append(parts, fmt.Sprintf("%s(%s)", label, binding.ID))
	}
	return strings.Join(parts, ", ")
}

func normalizeChannel(channel string) string {
	return strings.ToLower(strings.TrimSpace(channel))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
