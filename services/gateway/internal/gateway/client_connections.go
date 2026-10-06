package gateway

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Each authenticated request is attached before its persisted revocation is
// checked again, closing the authenticate/revoke race for long-lived streams.
// The registry retains cancellation functions only, never bearer credentials.
type clientConnectionRegistry struct {
	mu     sync.Mutex
	next   uint64
	active map[string]map[uint64]context.CancelFunc
}

func newClientConnectionRegistry() *clientConnectionRegistry {
	return &clientConnectionRegistry{active: map[string]map[uint64]context.CancelFunc{}}
}

func (s *Server) clientConnectionContext(ctx context.Context, clientID string) (context.Context, func(), error) {
	if clientID == "" {
		return ctx, func() {}, nil
	}
	connected, cancel := context.WithCancel(ctx)
	registry := s.clientConnections
	registry.mu.Lock()
	registry.next++
	id := registry.next
	if registry.active[clientID] == nil {
		registry.active[clientID] = map[uint64]context.CancelFunc{}
	}
	registry.active[clientID][id] = cancel
	registry.mu.Unlock()
	release := func() {
		cancel()
		registry.mu.Lock()
		delete(registry.active[clientID], id)
		if len(registry.active[clientID]) == 0 {
			delete(registry.active, clientID)
		}
		registry.mu.Unlock()
	}
	client, found, err := s.store.GetClient(connected, clientID)
	if err != nil || !found || client.RevokedAt != nil {
		release()
		if err == nil {
			err = errors.New("client is revoked or unavailable")
		}
		return connected, func() {}, err
	}
	return connected, release, nil
}

func (s *Server) cancelClientConnections(clientID string) {
	s.r3Mu.Lock()
	broker := s.r3Broker
	service := s.r3Executions
	s.r3Mu.Unlock()
	if service != nil {
		if err := service.RevokeWorkbenchClient(clientID); err != nil {
			slog.Warn("workbench revocation fence persistence unavailable")
		}
	}
	if broker != nil {
		broker.RevokeClientHosts(clientID)
	}
	registry := s.clientConnections
	registry.mu.Lock()
	for _, cancel := range registry.active[clientID] {
		cancel()
	}
	delete(registry.active, clientID)
	registry.mu.Unlock()

	// Single-use speech tickets are a separate authentication path; remove
	// unconsumed tickets as well as closing already-attached socket contexts.
	s.speechRealtimeMu.Lock()
	var tickets []*speechRealtimeTicket
	for hash, ticket := range s.speechRealtimeTickets {
		if ticket.clientID != clientID {
			continue
		}
		delete(s.speechRealtimeTickets, hash)
		delete(s.speechRealtimeTicketIDs, ticket.id)
		if ticket.timer != nil {
			ticket.timer.Stop()
		}
		tickets = append(tickets, ticket)
	}
	s.speechRealtimeMu.Unlock()
	for _, ticket := range tickets {
		_ = ticket.session.Close()
	}
}
