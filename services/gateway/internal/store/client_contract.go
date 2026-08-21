package store

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneClient(client app.Client) app.Client {
	client.LastSeenAt = cloneTimePointer(client.LastSeenAt)
	client.RevokedAt = cloneTimePointer(client.RevokedAt)
	return client
}

func clonePairingCode(code app.PairingCode) app.PairingCode {
	code.ClaimedAt = cloneTimePointer(code.ClaimedAt)
	return code
}

func cloneClientLifecycleEvent(event app.Event) app.Event {
	switch payload := event.Payload.(type) {
	case app.Client:
		event.Payload = cloneClient(payload)
	case app.PairingCode:
		event.Payload = clonePairingCode(payload)
	case app.NotificationBinding:
		event.Payload = cloneNotificationBinding(payload)
	case app.Message:
		event.Payload = cloneMessage(payload)
	}
	return event
}

func cloneClientLifecycleEvents(events []app.Event) []app.Event {
	out := make([]app.Event, len(events))
	for index, event := range events {
		out[index] = cloneClientLifecycleEvent(event)
	}
	return out
}

func cloneClientMap(values map[string]app.Client) map[string]app.Client {
	out := make(map[string]app.Client, len(values))
	for id, value := range values {
		out[id] = cloneClient(value)
	}
	return out
}

func clonePairingCodeMap(values map[string]app.PairingCode) map[string]app.PairingCode {
	out := make(map[string]app.PairingCode, len(values))
	for id, value := range values {
		out[id] = clonePairingCode(value)
	}
	return out
}

func normalizeClaimClient(client app.Client) (app.Client, error) {
	client.ID = strings.TrimSpace(client.ID)
	client.OwnerID = strings.TrimSpace(client.OwnerID)
	client.ActorID = strings.TrimSpace(client.ActorID)
	client.Name = strings.TrimSpace(client.Name)
	client.TokenHash = strings.TrimSpace(client.TokenHash)
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.OwnerID == "" {
		client.OwnerID = app.DefaultOwnerID
	}
	if client.ActorID == "" {
		client.ActorID = client.OwnerID
	}
	if client.Name == "" || client.TokenHash == "" || client.LastSeenAt != nil || client.RevokedAt != nil {
		return app.Client{}, errors.New("active client name and token hash are required and lifecycle fields must be empty")
	}
	client.CreatedAt = time.Time{}
	return client, nil
}

func normalizePairingSave(code app.PairingCode) (app.PairingCode, error) {
	code.ID = strings.TrimSpace(code.ID)
	code.CodeHash = strings.TrimSpace(code.CodeHash)
	if code.ID == "" {
		code.ID = app.NewID("pair")
	}
	if code.Status == "" {
		code.Status = "pending"
	}
	if code.Status != "pending" || code.CodeHash == "" || code.ExpiresAt.IsZero() || code.ClaimedAt != nil || code.ClientID != "" {
		return app.PairingCode{}, errors.New("pending pairing code with hash and expiry is required")
	}
	code.ExpiresAt = postgresTime(code.ExpiresAt)
	code.CreatedAt = time.Time{}
	return code, nil
}

func postgresTime(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

func nextRepositoryTime(now time.Time, highWater ...time.Time) time.Time {
	candidate := postgresTime(now)
	for _, previous := range highWater {
		if candidate.After(previous) {
			continue
		}
		candidate = previous.UTC().Truncate(time.Microsecond).Add(time.Microsecond)
	}
	return candidate
}

func validatePersistedClient(client app.Client) error {
	if client.ID == "" || client.CreatedAt.IsZero() {
		return errors.New("client identity and creation time are required")
	}
	if client.LastSeenAt != nil && client.LastSeenAt.IsZero() {
		return errors.New("client last-seen time is zero")
	}
	if client.RevokedAt != nil && client.RevokedAt.IsZero() {
		return errors.New("client revoke time is zero")
	}
	return nil
}

func validatePersistedPairingCode(code app.PairingCode, clients map[string]app.Client) error {
	if code.ID == "" || code.CreatedAt.IsZero() || code.ExpiresAt.IsZero() {
		return errors.New("pairing identity and times are required")
	}
	switch code.Status {
	case "pending", "expired":
		if code.ClaimedAt != nil || code.ClientID != "" {
			return errors.New("unclaimed pairing code carries claim fields")
		}
	case "claimed":
		if code.ClaimedAt == nil || code.ClaimedAt.IsZero() || code.ClientID == "" {
			return errors.New("claimed pairing code is missing claim fields")
		}
		if _, ok := clients[code.ClientID]; !ok {
			return errors.New("claimed pairing code references a missing client")
		}
	default:
		return fmt.Errorf("invalid pairing status %q", code.Status)
	}
	return nil
}

func normalizeAndValidatePersistedClientsAndPairings(clients map[string]app.Client, pairings map[string]app.PairingCode) error {
	clientHashes := map[string]string{}
	for id, client := range clients {
		if id == "" || client.ID != id {
			return fmt.Errorf("client key %q does not match embedded ID %q", id, client.ID)
		}
		if strings.TrimSpace(client.OwnerID) == "" {
			client.OwnerID = app.DefaultOwnerID
		}
		if strings.TrimSpace(client.ActorID) == "" {
			client.ActorID = client.OwnerID
		}
		if err := validatePersistedClient(client); err != nil {
			return fmt.Errorf("client %q: %w", id, err)
		}
		hash := strings.TrimSpace(client.TokenHash)
		if hash != "" {
			if prior, ok := clientHashes[hash]; ok {
				return fmt.Errorf("clients %q and %q share a token hash", prior, id)
			}
			clientHashes[hash] = id
		}
		clients[id] = cloneClient(client)
	}
	pairingHashes := map[string]string{}
	for id, code := range pairings {
		if id == "" || code.ID != id {
			return fmt.Errorf("pairing key %q does not match embedded ID %q", id, code.ID)
		}
		if err := validatePersistedPairingCode(code, clients); err != nil {
			return fmt.Errorf("pairing %q: %w", id, err)
		}
		hash := strings.TrimSpace(code.CodeHash)
		if hash != "" {
			if prior, ok := pairingHashes[hash]; ok {
				return fmt.Errorf("pairing codes %q and %q share a code hash", prior, id)
			}
			pairingHashes[hash] = id
		}
		pairings[id] = clonePairingCode(code)
	}
	return nil
}

func ClientsEqual(left, right app.Client) bool {
	return left.ID == right.ID && left.OwnerID == right.OwnerID && left.ActorID == right.ActorID &&
		left.Name == right.Name && left.TokenHash == right.TokenHash && left.CreatedAt.Equal(right.CreatedAt) &&
		timePointersEqual(left.LastSeenAt, right.LastSeenAt) && timePointersEqual(left.RevokedAt, right.RevokedAt)
}

func PairingCodesEqual(left, right app.PairingCode) bool {
	return left.ID == right.ID && left.CodeHash == right.CodeHash && left.Status == right.Status &&
		left.ExpiresAt.Equal(right.ExpiresAt) && left.CreatedAt.Equal(right.CreatedAt) &&
		timePointersEqual(left.ClaimedAt, right.ClaimedAt) && left.ClientID == right.ClientID
}

func timePointersEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func timePointerValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
