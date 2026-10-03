package gateway

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/r3execution"
)

const r3ScheduleLease = 30 * time.Second

// Only bounded online registrations live here. Definitions, contexts and task
// history remain client-owned; expiration never creates catch-up executions.
type r3Schedule struct {
	e           r3execution.Envelope
	digest      string
	due, expiry time.Time
	state       string
}
type r3ScheduleRegistry struct {
	mu      sync.Mutex
	entries map[string]*r3Schedule
}

func scheduleKey(owner, client, request string) string {
	return owner + "\x00" + client + "\x00" + request
}
func (s *Server) registerR3ScheduleRoutes() {
	s.mux.HandleFunc("POST /api/r3/schedules/lease", s.r3ScheduleRegister)
	s.mux.HandleFunc("POST /api/r3/schedules/{request}/renew", s.r3ScheduleRenew)
	s.mux.HandleFunc("POST /api/r3/schedules/{request}/cancel", s.r3ScheduleCancel)
}
func (s *Server) scheduleRegistry() *r3ScheduleRegistry {
	s.r3Mu.Lock()
	defer s.r3Mu.Unlock()
	if s.r3Schedules != nil {
		return s.r3Schedules
	}
	registry := &r3ScheduleRegistry{entries: map[string]*r3Schedule{}}
	s.r3Schedules = registry
	ctx := s.executionContext()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				registry.mu.Lock()
				clear(registry.entries)
				registry.mu.Unlock()
				return
			case now := <-ticker.C:
				s.r3ScheduleTick(now)
			}
		}
	}()
	return registry
}
func (s *Server) r3ScheduleTick(now time.Time) {
	registry := s.r3Schedules
	if registry == nil {
		return
	}
	registry.mu.Lock()
	ready := []*r3Schedule{}
	for key, entry := range registry.entries {
		if !entry.expiry.After(now) {
			delete(registry.entries, key)
			continue
		}
		if entry.state == "leased" && !entry.due.After(now) {
			entry.state = "firing"
			ready = append(ready, entry)
		}
	}
	registry.mu.Unlock()
	for _, entry := range ready {
		go func(entry *r3Schedule) {
			_, err := s.r3Executions.Submit(s.executionContext(), entry.e, entry.digest)
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if err != nil {
				entry.state = "admission_rejected"
			} else {
				entry.state = "accepted"
			}
		}(entry)
	}
}
func (s *Server) writeR3Schedule(w http.ResponseWriter, p requestPrincipal, request string, entry *r3Schedule) {
	if status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, request); err == nil {
		writeJSON(w, 200, status)
		return
	}
	writeJSON(w, 200, map[string]any{"schema_version": 1, "request_id": request, "state": entry.state, "lease_expires_at": entry.expiry})
}
func (s *Server) r3ScheduleRegister(w http.ResponseWriter, r *http.Request) {
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	var input struct {
		SchemaVersion int       `json:"schema_version"`
		DueAt         time.Time `json:"due_at"`
		Context       string    `json:"context"`
		Digest        string    `json:"digest"`
	}
	if err = readR3JSON(r, &input); err != nil || input.SchemaVersion != 1 {
		writeError(w, 400, errors.New("invalid R3 schedule lease"))
		return
	}
	e, err := r3execution.Decode([]byte(input.Context), input.Digest)
	if err != nil || e.OwnerID != p.OwnerID || e.ClientID != p.ClientID || e.DeploymentID != s.cfg.Gateway.DeploymentID || e.InstallationID != r.Header.Get("X-SparkClaw-Installation") || len(e.InputFiles) > 0 {
		writeError(w, 400, errors.New("invalid R3 scheduled context"))
		return
	}
	if status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, e.RequestID); err == nil {
		if status.InputDigest != input.Digest {
			r3Error(w, r3execution.ErrConflict)
			return
		}
		writeJSON(w, 200, status)
		return
	}
	now := time.Now().UTC()
	if input.DueAt.Before(now) || input.DueAt.After(now.Add(24*time.Hour)) {
		writeError(w, 400, errors.New("R3 online schedules must be in the next 24 hours"))
		return
	}
	registry := s.scheduleRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := scheduleKey(p.OwnerID, p.ClientID, e.RequestID)
	if entry := registry.entries[key]; entry != nil {
		if entry.digest != input.Digest || !entry.due.Equal(input.DueAt) {
			r3Error(w, r3execution.ErrConflict)
			return
		}
		entry.expiry = now.Add(r3ScheduleLease)
		s.writeR3Schedule(w, p, e.RequestID, entry)
		return
	}
	ownerCount := 0
	for _, entry := range registry.entries {
		if entry.e.OwnerID == p.OwnerID {
			ownerCount++
		}
	}
	if ownerCount >= 8 || len(registry.entries) >= 32 {
		r3Error(w, r3execution.ErrCapacity)
		return
	}
	entry := &r3Schedule{e: e, digest: input.Digest, due: input.DueAt, expiry: now.Add(r3ScheduleLease), state: "leased"}
	registry.entries[key] = entry
	s.writeR3Schedule(w, p, e.RequestID, entry)
}
func (s *Server) r3ScheduleRenew(w http.ResponseWriter, r *http.Request) {
	if !emptyR3Request(w, r) {
		return
	}
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	registry := s.scheduleRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := scheduleKey(p.OwnerID, p.ClientID, r.PathValue("request"))
	entry := registry.entries[key]
	if entry == nil || !entry.expiry.After(time.Now()) {
		delete(registry.entries, key)
		if status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request")); err == nil {
			writeJSON(w, 200, status)
			return
		}
		r3Error(w, r3execution.ErrNotFound)
		return
	}
	entry.expiry = time.Now().UTC().Add(r3ScheduleLease)
	s.writeR3Schedule(w, p, r.PathValue("request"), entry)
}
func (s *Server) r3ScheduleCancel(w http.ResponseWriter, r *http.Request) {
	if !emptyR3Request(w, r) {
		return
	}
	p, err := s.r3Principal(r)
	if err != nil {
		writeError(w, 403, err)
		return
	}
	registry := s.scheduleRegistry()
	registry.mu.Lock()
	defer registry.mu.Unlock()
	key := scheduleKey(p.OwnerID, p.ClientID, r.PathValue("request"))
	entry := registry.entries[key]
	if entry != nil && entry.state == "firing" {
		writeError(w, 409, errors.New("schedule admission is being reconciled"))
		return
	}
	if status, err := s.r3Executions.Lookup(p.OwnerID, p.ClientID, r.PathValue("request")); err == nil {
		delete(registry.entries, key)
		writeJSON(w, 200, status)
		return
	}
	delete(registry.entries, key)
	writeJSON(w, 200, map[string]any{"schema_version": 1, "request_id": r.PathValue("request"), "state": "canceled"})
}
func (s *Server) revokeR3Schedules(client string) {
	s.r3Mu.Lock()
	registry := s.r3Schedules
	s.r3Mu.Unlock()
	if registry == nil {
		return
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for key, entry := range registry.entries {
		if entry.e.ClientID == client {
			delete(registry.entries, key)
		}
	}
}
