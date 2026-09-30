package gateway

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

const clientIssuanceRetention = 10 * time.Minute

var clientIssuanceKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{7,159}$`)

type clientIssuanceCoordinator struct {
	mu      sync.Mutex
	pending map[string]*pendingClientIssuance
}

type pendingClientIssuance struct {
	name      string
	token     string
	client    app.Client
	committed bool
	expiresAt time.Time
}

func newClientIssuanceCoordinator() *clientIssuanceCoordinator {
	return &clientIssuanceCoordinator{pending: map[string]*pendingClientIssuance{}}
}

func (s *Server) issueClient(w http.ResponseWriter, r *http.Request) {
	principal := principalForRequest(r)
	if !principal.Authenticated {
		writeError(w, http.StatusForbidden, errors.New("authenticated Owner management authority is required to issue a Client"))
		return
	}
	requestKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if !clientIssuanceKeyPattern.MatchString(requestKey) {
		writeError(w, http.StatusBadRequest, errors.New("a valid Idempotency-Key is required"))
		return
	}
	var input struct {
		ClientName string `json:"client_name"`
	}
	if err := readJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, errors.New("invalid Client issuance request"))
		return
	}
	clientName := strings.TrimSpace(input.ClientName)
	if clientName == "" {
		clientName = "SparkClaw Web"
	}
	if len([]rune(clientName)) > 80 {
		writeError(w, http.StatusBadRequest, errors.New("client_name must be 80 characters or fewer"))
		return
	}

	coordinator := s.clientIssuance
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	now := time.Now().UTC()
	for key, pending := range coordinator.pending {
		if !pending.expiresAt.After(now) {
			delete(coordinator.pending, key)
		}
	}
	key := principal.OwnerID + "\x00" + requestKey
	pending := coordinator.pending[key]
	replayed := pending != nil
	if pending != nil && pending.name != clientName {
		writeError(w, http.StatusConflict, errors.New("Idempotency-Key was already used for another Client issuance request"))
		return
	}
	if pending == nil {
		token, err := randomSecret(32)
		if err != nil {
			writeError(w, http.StatusInternalServerError, errors.New("generate Client credential"))
			return
		}
		pending = &pendingClientIssuance{
			name:  clientName,
			token: token,
			client: app.Client{
				ID: stableIssuedClientID(principal.OwnerID, requestKey), OwnerID: principal.OwnerID, ActorID: principal.ActorID,
				Name: clientName, TokenHash: hashSecret(token),
			},
			expiresAt: now.Add(clientIssuanceRetention),
		}
		coordinator.pending[key] = pending
	}
	if !pending.committed {
		registered, err := s.store.RegisterClient(r.Context(), pending.client)
		if err != nil && store.StoreErrorCodeOf(err) == store.StoreErrorUnknownOutcome {
			persisted, found, readErr := s.store.GetClient(r.Context(), pending.client.ID)
			if readErr == nil && found && sameIssuedClient(persisted, pending.client) {
				registered, err = persisted, nil
			}
		}
		if err != nil {
			if store.StoreErrorCodeOf(err) == store.StoreErrorConflict || store.StoreErrorCodeOf(err) == store.StoreErrorInvalid {
				delete(coordinator.pending, key)
			}
			switch store.StoreErrorCodeOf(err) {
			case store.StoreErrorConflict:
				persisted, found, readErr := s.store.GetClient(r.Context(), pending.client.ID)
				if readErr == nil && found && sameIssuedClientMetadata(persisted, pending.client) {
					writeJSON(w, http.StatusConflict, map[string]any{"error": "this Client issuance was already persisted, but its one-time credential is no longer recoverable; revoke it and retry with a new Idempotency-Key", "code": "CLIENT_CREDENTIAL_UNRECOVERABLE", "client_id": persisted.ID})
					return
				}
				writeError(w, http.StatusConflict, errors.New("Client issuance identity conflicts with persisted state"))
			case store.StoreErrorTimeout:
				writeError(w, http.StatusGatewayTimeout, errors.New("Client issuance timed out; retry with the same Idempotency-Key"))
			default:
				writeError(w, http.StatusServiceUnavailable, errors.New("Client issuance is temporarily unavailable; retry with the same Idempotency-Key"))
			}
			return
		}
		pending.client = registered
		pending.committed = true
	}
	current, found, err := s.store.GetClient(r.Context(), pending.client.ID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("Client issuance verification unavailable; retry with the same Idempotency-Key"))
		return
	}
	if !found || current.RevokedAt != nil {
		delete(coordinator.pending, key)
		writeJSON(w, http.StatusConflict, map[string]any{"error": "issued Client has been revoked", "code": "CLIENT_REVOKED", "client_id": pending.client.ID})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"client": pending.client, "token": pending.token})
}

func stableIssuedClientID(ownerID, requestKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(ownerID) + "\x00" + strings.TrimSpace(requestKey)))
	return "client_web_" + base64.RawURLEncoding.EncodeToString(digest[:18])
}

func sameIssuedClientMetadata(left, right app.Client) bool {
	return left.ID == right.ID && left.OwnerID == right.OwnerID && left.ActorID == right.ActorID && left.Name == right.Name
}

func sameIssuedClient(left, right app.Client) bool {
	return left.RevokedAt == nil && sameIssuedClientMetadata(left, right) && left.TokenHash == right.TokenHash
}
