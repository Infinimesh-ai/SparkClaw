package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"golang.org/x/sync/semaphore"
)

type pairingPendingKind string

const (
	pairingPendingStart pairingPendingKind = "start"
	pairingPendingClaim pairingPendingKind = "claim"
)

type pairingPending struct {
	kind              pairingPendingKind
	generation        uint64
	fingerprint       string
	expiresAt         time.Time
	plaintextCode     string
	plaintextToken    string
	attemptedPairing  app.PairingCode
	pairingCandidate  *app.PairingCode
	preClaimPairing   app.PairingCode
	attemptedClient   app.Client
	clientCandidate   *app.Client
	submittedCodeHash string
	timer             *time.Timer
	done              chan struct{}
}

type pairingCoordinator struct {
	gate       *semaphore.Weighted
	pending    *pairingPending
	generation uint64
	now        func() time.Time
}

func newPairingCoordinator() *pairingCoordinator {
	return &pairingCoordinator{gate: semaphore.NewWeighted(1), now: func() time.Time { return time.Now().UTC() }}
}

func (c *pairingCoordinator) currentTime() time.Time {
	if c != nil && c.now != nil {
		return c.now().UTC()
	}
	return time.Now().UTC()
}

func (s *Server) installPairingPendingLocked(pending *pairingPending) {
	coordinator := s.pairing
	coordinator.generation++
	pending.generation = coordinator.generation
	pending.done = make(chan struct{})
	coordinator.pending = pending
	delay := pending.expiresAt.Sub(coordinator.currentTime())
	if delay < 0 {
		delay = 0
	}
	pending.timer = time.AfterFunc(delay, func() { s.clearPairingGeneration(pending.generation) })
	lifecycle := s.executionContext()
	go func(generation uint64, done <-chan struct{}) {
		select {
		case <-lifecycle.Done():
			s.clearPairingGeneration(generation)
		case <-done:
		}
	}(pending.generation, pending.done)
}

func (s *Server) clearPairingGeneration(generation uint64) {
	coordinator := s.pairing
	if coordinator == nil {
		return
	}
	if err := coordinator.gate.Acquire(context.Background(), 1); err != nil {
		return
	}
	defer coordinator.gate.Release(1)
	if coordinator.pending != nil && coordinator.pending.generation == generation {
		s.clearPairingPendingLocked()
	}
}

func (s *Server) clearPairingPendingLocked() {
	coordinator := s.pairing
	if coordinator == nil || coordinator.pending == nil {
		return
	}
	pending := coordinator.pending
	coordinator.pending = nil
	if pending.timer != nil {
		pending.timer.Stop()
	}
	pending.plaintextCode = ""
	pending.plaintextToken = ""
	close(pending.done)
}

func pairingStartFingerprint(ownerID string) string {
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		ownerID = app.DefaultOwnerID
	}
	return ownerID
}

func pairingClaimFingerprint(ownerID, pairingID, codeHash, clientName string) string {
	digest := sha256.Sum256([]byte(strings.Join([]string{
		strings.TrimSpace(ownerID), strings.TrimSpace(pairingID), strings.TrimSpace(codeHash), strings.TrimSpace(clientName),
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

func (s *Server) reconcilePendingStartLocked(ctx context.Context, pending *pairingPending) (map[string]any, int, error) {
	persisted, found, err := s.store.GetPairingCode(ctx, pending.attemptedPairing.ID)
	if err != nil {
		return nil, pairingStoreStatus(err), pairingStorePublicError(err)
	}
	if pending.pairingCandidate != nil && found && store.PairingCodesEqual(persisted, *pending.pairingCandidate) {
		return s.completePendingStartLocked(pending, persisted)
	}
	if !found {
		s.clearPairingPendingLocked()
		return nil, http.StatusServiceUnavailable, errors.New("pairing is temporarily unavailable")
	}
	s.clearPairingPendingLocked()
	return nil, http.StatusConflict, errors.New("pairing state changed")
}

func (s *Server) completePendingStartLocked(pending *pairingPending, pairing app.PairingCode) (map[string]any, int, error) {
	if !pairing.ExpiresAt.After(s.pairing.currentTime()) {
		s.clearPairingPendingLocked()
		return nil, http.StatusServiceUnavailable, errors.New("pairing is temporarily unavailable")
	}
	response := map[string]any{"pairing_id": pairing.ID, "code": pending.plaintextCode, "expires_at": pairing.ExpiresAt}
	s.clearPairingPendingLocked()
	return response, http.StatusCreated, nil
}

func (s *Server) reconcilePendingClaimLocked(ctx context.Context, pending *pairingPending) (map[string]any, int, error) {
	pairing, pairingFound, err := s.store.GetPairingCode(ctx, pending.preClaimPairing.ID)
	if err != nil {
		return nil, pairingStoreStatus(err), pairingStorePublicError(err)
	}
	client, clientFound, err := s.store.GetClient(ctx, pending.attemptedClient.ID)
	if err != nil {
		return nil, pairingStoreStatus(err), pairingStorePublicError(err)
	}
	if pending.pairingCandidate != nil && pending.clientCandidate != nil && pairingFound && clientFound &&
		store.PairingCodesEqual(pairing, *pending.pairingCandidate) && store.ClientsEqual(client, *pending.clientCandidate) {
		return s.completePendingClaimLocked(pending, pairing, client)
	}
	if pairingFound && store.PairingCodesEqual(pairing, pending.preClaimPairing) && !clientFound {
		s.clearPairingPendingLocked()
		return nil, http.StatusServiceUnavailable, errors.New("pairing is temporarily unavailable")
	}
	s.clearPairingPendingLocked()
	return nil, http.StatusConflict, errors.New("pairing state changed")
}

func (s *Server) completePendingClaimLocked(pending *pairingPending, pairing app.PairingCode, client app.Client) (map[string]any, int, error) {
	if !pairing.ExpiresAt.After(s.pairing.currentTime()) {
		s.clearPairingPendingLocked()
		return nil, http.StatusBadRequest, errors.New("pairing code is not active")
	}
	response := map[string]any{"client": client, "token": pending.plaintextToken}
	s.clearPairingPendingLocked()
	return response, http.StatusCreated, nil
}

func pairingStoreStatus(err error) int {
	if errors.Is(err, context.DeadlineExceeded) || store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
		return http.StatusGatewayTimeout
	}
	return http.StatusServiceUnavailable
}

func pairingStorePublicError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) || store.StoreErrorCodeOf(err) == store.StoreErrorTimeout {
		return errors.New("pairing request timed out")
	}
	return errors.New("pairing is temporarily unavailable")
}

func pairingCodesMatch(left, right string) bool {
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
