package store

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type MemoryStore struct {
	mu               sync.RWMutex
	sessions         map[string]app.Session
	clients          map[string]app.Client
	ownerProfile     app.OwnerProfile
	pairingCodes     map[string]app.PairingCode
	messages         map[string][]app.Message
	runFeedback      map[string][]app.RunFeedback
	runs             map[string]app.AgentRun
	modelCalls       map[string]app.ModelCall
	toolCalls        map[string]app.ToolCall
	approvals        map[string]app.Approval
	memories         map[string]app.Memory
	memoryCandidates map[string]app.MemoryCandidate
	auditEvents      []app.AuditEvent
	events           []app.Event
	evalRuns         map[string]app.EvalRun
	artifactObjects  map[string]app.ArtifactObject
	episodeSummaries map[string]app.EpisodeSummary
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:         map[string]app.Session{},
		clients:          map[string]app.Client{},
		ownerProfile:     app.DefaultOwnerProfile(),
		pairingCodes:     map[string]app.PairingCode{},
		messages:         map[string][]app.Message{},
		runFeedback:      map[string][]app.RunFeedback{},
		runs:             map[string]app.AgentRun{},
		modelCalls:       map[string]app.ModelCall{},
		toolCalls:        map[string]app.ToolCall{},
		approvals:        map[string]app.Approval{},
		memories:         map[string]app.Memory{},
		memoryCandidates: map[string]app.MemoryCandidate{},
		auditEvents:      []app.AuditEvent{},
		events:           []app.Event{},
		evalRuns:         map[string]app.EvalRun{},
		artifactObjects:  map[string]app.ArtifactObject{},
		episodeSummaries: map[string]app.EpisodeSummary{},
	}
}

func (s *MemoryStore) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{
		Sessions:         cloneMap(s.sessions),
		Clients:          cloneMap(s.clients),
		OwnerProfile:     cloneOwnerProfile(s.ownerProfile),
		PairingCodes:     cloneMap(s.pairingCodes),
		Messages:         cloneSliceMap(s.messages),
		RunFeedback:      cloneSliceMap(s.runFeedback),
		Runs:             cloneMap(s.runs),
		ModelCalls:       cloneMap(s.modelCalls),
		ToolCalls:        cloneMap(s.toolCalls),
		Approvals:        cloneMap(s.approvals),
		Memories:         cloneMap(s.memories),
		MemoryCandidates: cloneMap(s.memoryCandidates),
		AuditEvents:      append([]app.AuditEvent(nil), s.auditEvents...),
		Events:           append([]app.Event(nil), s.events...),
		EvalRuns:         cloneMap(s.evalRuns),
		ArtifactObjects:  cloneMap(s.artifactObjects),
		EpisodeSummaries: cloneMap(s.episodeSummaries),
	}
}

func (s *MemoryStore) loadSnapshot(snapshot Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions = ensureMap(snapshot.Sessions)
	s.clients = ensureMap(snapshot.Clients)
	s.ownerProfile = normalizeOwnerProfile(snapshot.OwnerProfile)
	s.pairingCodes = ensureMap(snapshot.PairingCodes)
	s.messages = ensureSliceMap(snapshot.Messages)
	s.runFeedback = ensureSliceMap(snapshot.RunFeedback)
	s.runs = ensureMap(snapshot.Runs)
	s.modelCalls = ensureMap(snapshot.ModelCalls)
	s.toolCalls = ensureMap(snapshot.ToolCalls)
	s.approvals = ensureMap(snapshot.Approvals)
	s.memories = ensureMap(snapshot.Memories)
	s.memoryCandidates = ensureMap(snapshot.MemoryCandidates)
	s.auditEvents = append([]app.AuditEvent(nil), snapshot.AuditEvents...)
	s.events = append([]app.Event(nil), snapshot.Events...)
	s.evalRuns = ensureMap(snapshot.EvalRuns)
	s.artifactObjects = ensureMap(snapshot.ArtifactObjects)
	s.episodeSummaries = ensureMap(snapshot.EpisodeSummaries)
}

func (s *MemoryStore) CreateSession(title string) app.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if title == "" {
		title = "New SparkClaw Session"
	}
	session := app.Session{ID: app.NewID("s"), Title: title, CreatedAt: now, UpdatedAt: now}
	s.sessions[session.ID] = session
	s.appendAuditLocked("session.created", session.ID, "", "system", "Session created", map[string]any{"title": title})
	s.appendEventLocked("session.created", session.ID, "", session)
	return session
}

func (s *MemoryStore) ListSessions() []app.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]app.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		out = append(out, session)
	}
	slices.SortFunc(out, func(a, b app.Session) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) GetSession(id string) (app.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.sessions[id]
	return session, ok
}

func (s *MemoryStore) SaveClient(client app.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if client.ID == "" {
		client.ID = app.NewID("client")
	}
	if client.CreatedAt.IsZero() {
		client.CreatedAt = time.Now().UTC()
	}
	s.clients[client.ID] = client
	s.appendAuditLocked("client.saved", "", "", "gateway", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLocked("client.saved", "", "", client)
}

func (s *MemoryStore) GetClient(id string) (app.Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	client, ok := s.clients[id]
	return client, ok
}

func (s *MemoryStore) ListClients() []app.Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Client{}
	for _, client := range s.clients {
		out = append(out, client)
	}
	slices.SortFunc(out, func(a, b app.Client) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) RevokeClient(id string) (app.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return app.Client{}, errors.New("client not found")
	}
	now := time.Now().UTC()
	client.RevokedAt = &now
	s.clients[id] = client
	s.appendAuditLocked("client.revoked", "", "", "owner", client.Name, map[string]any{"client_id": client.ID})
	s.appendEventLocked("client.revoked", "", "", client)
	return client, nil
}

func (s *MemoryStore) FindClientByTokenHash(tokenHash string) (app.Client, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, client := range s.clients {
		if client.TokenHash == tokenHash && client.RevokedAt == nil {
			return client, true
		}
	}
	return app.Client{}, false
}

func (s *MemoryStore) TouchClient(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.clients[id]
	if !ok {
		return
	}
	now := time.Now().UTC()
	client.LastSeenAt = &now
	s.clients[id] = client
}

func (s *MemoryStore) GetOwnerProfile() app.OwnerProfile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneOwnerProfile(normalizeOwnerProfile(s.ownerProfile))
}

func (s *MemoryStore) UpdateOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := normalizeOwnerProfile(s.ownerProfile)
	now := time.Now().UTC()
	profile.ID = current.ID
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = current.CreatedAt
	}
	profile.UpdatedAt = now
	profile.Preferences = cloneStringMap(profile.Preferences)
	s.ownerProfile = normalizeOwnerProfile(profile)
	s.appendAuditLocked("owner_profile.updated", "", "", "owner", s.ownerProfile.DisplayName, map[string]any{
		"owner_id":     s.ownerProfile.ID,
		"email_set":    s.ownerProfile.Email != "",
		"preferences":  len(s.ownerProfile.Preferences),
		"display_name": s.ownerProfile.DisplayName,
	})
	s.appendEventLocked("owner_profile.updated", "", "", s.ownerProfile)
	return cloneOwnerProfile(s.ownerProfile)
}

func (s *MemoryStore) SavePairingCode(code app.PairingCode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code.ID == "" {
		code.ID = app.NewID("pair")
	}
	if code.CreatedAt.IsZero() {
		code.CreatedAt = time.Now().UTC()
	}
	if code.Status == "" {
		code.Status = "pending"
	}
	s.pairingCodes[code.ID] = code
	s.appendAuditLocked("pairing_code.created", "", "", "gateway", "Pairing code created", map[string]any{"pairing_id": code.ID})
	s.appendEventLocked("pairing_code.created", "", "", code)
}

func (s *MemoryStore) GetPairingCode(id string) (app.PairingCode, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	code, ok := s.pairingCodes[id]
	return code, ok
}

func (s *MemoryStore) ClaimPairingCode(id, clientID string) (app.PairingCode, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	code, ok := s.pairingCodes[id]
	if !ok {
		return app.PairingCode{}, errors.New("pairing code not found")
	}
	if code.Status != "pending" {
		return app.PairingCode{}, errors.New("pairing code is not pending")
	}
	now := time.Now().UTC()
	if now.After(code.ExpiresAt) {
		code.Status = "expired"
		s.pairingCodes[id] = code
		return app.PairingCode{}, errors.New("pairing code expired")
	}
	code.Status = "claimed"
	code.ClaimedAt = &now
	code.ClientID = clientID
	s.pairingCodes[id] = code
	s.appendAuditLocked("pairing_code.claimed", "", "", "gateway", "Pairing code claimed", map[string]any{"pairing_id": code.ID, "client_id": clientID})
	s.appendEventLocked("pairing_code.claimed", "", "", code)
	return code, nil
}

func (s *MemoryStore) AddMessage(message app.Message) app.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.ID == "" {
		message.ID = app.NewID("m")
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = time.Now().UTC()
	}
	s.messages[message.SessionID] = append(s.messages[message.SessionID], message)
	if session, ok := s.sessions[message.SessionID]; ok {
		session.UpdatedAt = message.CreatedAt
		if session.Title == "" || session.Title == "New SparkClaw Session" {
			session.Title = deriveTitle(message.Content)
		}
		s.sessions[message.SessionID] = session
	}
	s.appendEventLocked("message.created", message.SessionID, message.RunID, message)
	return message
}

func (s *MemoryStore) ListMessages(sessionID string) []app.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	messages := s.messages[sessionID]
	if len(messages) == 0 {
		return []app.Message{}
	}
	return append([]app.Message{}, messages...)
}

func (s *MemoryStore) SaveRunFeedback(feedback app.RunFeedback) app.RunFeedback {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if feedback.ID == "" {
		feedback.ID = app.NewID("fb")
	}
	if feedback.CreatedAt.IsZero() {
		feedback.CreatedAt = now
	}
	feedback.UpdatedAt = now
	feedback.Rating = strings.TrimSpace(feedback.Rating)
	feedback.Note = strings.TrimSpace(feedback.Note)
	feedback.Correction = strings.TrimSpace(feedback.Correction)
	items := s.runFeedback[feedback.RunID]
	replaced := false
	for i, existing := range items {
		if existing.ID == feedback.ID || existing.MessageID != "" && existing.MessageID == feedback.MessageID {
			feedback.ID = existing.ID
			feedback.CreatedAt = existing.CreatedAt
			items[i] = feedback
			replaced = true
			break
		}
	}
	if !replaced {
		items = append(items, feedback)
	}
	s.runFeedback[feedback.RunID] = items
	s.appendAuditLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, "owner", feedback.Rating, map[string]any{
		"feedback_id":    feedback.ID,
		"message_id":     feedback.MessageID,
		"has_note":       feedback.Note != "",
		"has_correction": feedback.Correction != "",
	})
	s.appendEventLocked("run_feedback.saved", feedback.SessionID, feedback.RunID, feedback)
	return feedback
}

func (s *MemoryStore) ListRunFeedback(runID string) []app.RunFeedback {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.RunFeedback{}
	if runID != "" {
		out = append(out, s.runFeedback[runID]...)
	} else {
		for _, items := range s.runFeedback {
			out = append(out, items...)
		}
	}
	slices.SortFunc(out, func(a, b app.RunFeedback) int {
		return b.UpdatedAt.Compare(a.UpdatedAt)
	})
	return out
}

func (s *MemoryStore) SaveRun(run app.AgentRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runs[run.ID] = run
	s.appendEventLocked("run."+run.State, run.SessionID, run.ID, run)
}

func (s *MemoryStore) GetRun(id string) (app.AgentRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[id]
	return run, ok
}

func (s *MemoryStore) ListRuns(sessionID string) []app.AgentRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.AgentRun{}
	for _, run := range s.runs {
		if sessionID == "" || run.SessionID == sessionID {
			out = append(out, run)
		}
	}
	slices.SortFunc(out, func(a, b app.AgentRun) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveModelCall(call app.ModelCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if call.ID == "" {
		call.ID = app.NewID("mc")
	}
	if call.StartedAt.IsZero() {
		call.StartedAt = time.Now().UTC()
	}
	s.modelCalls[call.ID] = call
	s.appendAuditLocked("model_call."+call.Status, call.SessionID, call.RunID, "model-router", call.Model, map[string]any{
		"lane":       call.Lane,
		"profile":    call.Profile,
		"operation":  call.Operation,
		"latency_ms": call.LatencyMS,
	})
	s.appendEventLocked("model_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *MemoryStore) ListModelCalls(sessionID, runID string) []app.ModelCall {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ModelCall{}
	for _, call := range s.modelCalls {
		if (sessionID == "" || call.SessionID == sessionID) && (runID == "" || call.RunID == runID) {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ModelCall) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveToolCall(call app.ToolCall) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolCalls[call.ID] = call
	s.appendAuditLocked("tool_call."+call.Status, call.SessionID, call.RunID, "agent", call.Tool, map[string]any{
		"risk": call.Risk,
		"id":   call.ID,
	})
	s.appendEventLocked("tool_call."+call.Status, call.SessionID, call.RunID, call)
}

func (s *MemoryStore) GetToolCall(id string) (app.ToolCall, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	call, ok := s.toolCalls[id]
	return call, ok
}

func (s *MemoryStore) ListToolCalls(sessionID string) []app.ToolCall {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ToolCall{}
	for _, call := range s.toolCalls {
		if sessionID == "" || call.SessionID == sessionID {
			out = append(out, call)
		}
	}
	slices.SortFunc(out, func(a, b app.ToolCall) int {
		return a.StartedAt.Compare(b.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveApproval(approval app.Approval) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.approvals[approval.ID] = approval
	s.appendAuditLocked("approval."+approval.Status, approval.SessionID, approval.RunID, "policy", approval.Summary, map[string]any{
		"tool": approval.Tool,
		"risk": approval.Risk,
	})
	s.appendEventLocked("approval."+approval.Status, approval.SessionID, approval.RunID, approval)
}

func (s *MemoryStore) ResolveApproval(id, status, note string) (app.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.approvals[id]
	if !ok {
		return app.Approval{}, errors.New("approval not found")
	}
	if approval.Status != "pending" {
		return app.Approval{}, errors.New("approval already resolved")
	}
	now := time.Now().UTC()
	approval.Status = status
	approval.ResolvedAt = &now
	approval.ResolutionNote = note
	s.approvals[id] = approval
	s.appendAuditLocked("approval."+status, approval.SessionID, approval.RunID, "owner", approval.Summary, map[string]any{"note": note})
	s.appendEventLocked("approval."+status, approval.SessionID, approval.RunID, approval)
	return approval, nil
}

func (s *MemoryStore) ListApprovals(status string) []app.Approval {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Approval{}
	for _, approval := range s.approvals {
		if status == "" || approval.Status == status {
			out = append(out, approval)
		}
	}
	slices.SortFunc(out, func(a, b app.Approval) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) AddMemoryCandidate(candidate app.MemoryCandidate) app.MemoryCandidate {
	s.mu.Lock()
	defer s.mu.Unlock()
	if candidate.ID == "" {
		candidate.ID = app.NewID("mc")
	}
	if candidate.CreatedAt.IsZero() {
		candidate.CreatedAt = time.Now().UTC()
	}
	if candidate.Status == "" {
		candidate.Status = "pending"
	}
	s.memoryCandidates[candidate.ID] = candidate
	s.appendAuditLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, "agent", candidate.Content, map[string]any{"kind": candidate.Kind})
	s.appendEventLocked("memory_candidate.created", candidate.SessionID, candidate.RunID, candidate)
	return candidate
}

func (s *MemoryStore) ResolveMemoryCandidate(id, status string) (app.MemoryCandidate, *app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate, ok := s.memoryCandidates[id]
	if !ok {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate not found")
	}
	if candidate.Status != "pending" {
		return app.MemoryCandidate{}, nil, errors.New("memory candidate already resolved")
	}
	now := time.Now().UTC()
	candidate.Status = status
	candidate.ResolvedAt = &now
	s.memoryCandidates[id] = candidate
	var memory *app.Memory
	if status == "accepted" {
		m := app.Memory{
			ID:        app.NewID("mem"),
			Kind:      candidate.Kind,
			Content:   candidate.Content,
			SourceID:  candidate.RunID,
			CreatedAt: now,
		}
		s.memories[m.ID] = m
		memory = &m
	}
	s.appendAuditLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, "owner", candidate.Content, nil)
	s.appendEventLocked("memory_candidate."+status, candidate.SessionID, candidate.RunID, candidate)
	return candidate, memory, nil
}

func (s *MemoryStore) ListMemoryCandidates(status string) []app.MemoryCandidate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.MemoryCandidate{}
	for _, candidate := range s.memoryCandidates {
		if status == "" || candidate.Status == status {
			out = append(out, candidate)
		}
	}
	slices.SortFunc(out, func(a, b app.MemoryCandidate) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) SearchMemories(query string) []app.Memory {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Memory{}
	q := strings.ToLower(query)
	for _, memory := range s.memories {
		if q == "" || strings.Contains(strings.ToLower(memory.Content), q) || strings.Contains(strings.ToLower(memory.Kind), q) {
			out = append(out, memory)
		}
	}
	slices.SortFunc(out, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) UpdateMemory(id, kind, content string) (app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, errors.New("memory not found")
	}
	memory.Kind = kind
	memory.Content = content
	s.memories[id] = memory
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.updated", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.updated", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) DeleteMemory(id string) (app.Memory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	memory, ok := s.memories[id]
	if !ok {
		return app.Memory{}, errors.New("memory not found")
	}
	delete(s.memories, id)
	sessionID := s.sessionIDForRunLocked(memory.SourceID)
	s.appendAuditLocked("memory.deleted", sessionID, memory.SourceID, "owner", memory.Content, map[string]any{"memory_id": memory.ID, "kind": memory.Kind})
	s.appendEventLocked("memory.deleted", sessionID, memory.SourceID, memory)
	return memory, nil
}

func (s *MemoryStore) PruneMemories(cutoff time.Time) []app.Memory {
	if cutoff.IsZero() {
		return []app.Memory{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pruned := []app.Memory{}
	for id, memory := range s.memories {
		if memory.CreatedAt.IsZero() || !memory.CreatedAt.Before(cutoff) {
			continue
		}
		delete(s.memories, id)
		pruned = append(pruned, memory)
		sessionID := s.sessionIDForRunLocked(memory.SourceID)
		s.appendAuditLocked("memory.pruned", sessionID, memory.SourceID, "memory-retention", memory.Kind, map[string]any{
			"memory_id": memory.ID,
			"cutoff":    cutoff.UTC().Format(time.RFC3339),
		})
		s.appendEventLocked("memory.pruned", sessionID, memory.SourceID, memory)
	}
	slices.SortFunc(pruned, func(a, b app.Memory) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return pruned
}

func (s *MemoryStore) AddAudit(event app.AuditEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if event.ID == "" {
		event.ID = app.NewID("audit")
	}
	if event.Time.IsZero() {
		event.Time = time.Now().UTC()
	}
	s.auditEvents = append(s.auditEvents, event)
}

func (s *MemoryStore) ListAudit(sessionID string) []app.AuditEvent {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.AuditEvent{}
	for _, event := range s.auditEvents {
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	slices.SortFunc(out, func(a, b app.AuditEvent) int {
		return b.Time.Compare(a.Time)
	})
	return out
}

func (s *MemoryStore) EventsAfter(sessionID, after string) []app.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.Event{}
	started := after == ""
	for _, event := range s.events {
		if !started {
			if event.ID == after {
				started = true
			}
			continue
		}
		if sessionID == "" || event.SessionID == sessionID {
			out = append(out, event)
		}
	}
	return out
}

func (s *MemoryStore) SaveEvalRun(run app.EvalRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run.ID == "" {
		run.ID = app.NewID("eval")
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	s.evalRuns[run.ID] = run
	s.appendAuditLocked("eval."+run.Status, "", "", "evaluator", run.Summary, map[string]any{
		"profile":          run.Profile,
		"id":               run.ID,
		"failure_archives": len(run.FailureArchives),
	})
	s.appendEventLocked("eval."+run.Status, "", run.ID, run)
}

func (s *MemoryStore) GetEvalRun(id string) (app.EvalRun, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.evalRuns[id]
	return run, ok
}

func (s *MemoryStore) ListEvalRuns() []app.EvalRun {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.EvalRun{}
	for _, run := range s.evalRuns {
		out = append(out, run)
	}
	slices.SortFunc(out, func(a, b app.EvalRun) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return out
}

func (s *MemoryStore) SaveArtifactObject(object app.ArtifactObject) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if object.ID == "" {
		object.ID = app.NewID("obj")
	}
	if object.CreatedAt.IsZero() {
		object.CreatedAt = time.Now().UTC()
	}
	s.artifactObjects[object.ID] = object
	s.appendAuditLocked("artifact.saved", object.SessionID, object.RunID, "artifact-store", object.URI, map[string]any{
		"kind":    object.Kind,
		"backend": object.Backend,
		"key":     object.Key,
		"bytes":   object.Bytes,
		"eval_id": object.EvalID,
	})
	s.appendEventLocked("artifact.saved", object.SessionID, object.RunID, object)
}

func (s *MemoryStore) ListArtifactObjects(limit int) []app.ArtifactObject {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.ArtifactObject{}
	for _, object := range s.artifactObjects {
		out = append(out, object)
	}
	slices.SortFunc(out, func(a, b app.ArtifactObject) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func (s *MemoryStore) SaveEpisodeSummary(summary app.EpisodeSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if summary.ID == "" {
		summary.ID = app.NewID("ep")
	}
	if summary.CreatedAt.IsZero() {
		summary.CreatedAt = time.Now().UTC()
	}
	s.episodeSummaries[summary.ID] = summary
	s.appendAuditLocked("episode_summary.saved", summary.SessionID, summary.RunID, "runtime", summary.Outcome, map[string]any{
		"tools":            summary.Tools,
		"repair_performed": summary.RepairPerformed,
	})
	s.appendEventLocked("episode_summary.saved", summary.SessionID, summary.RunID, summary)
}

func (s *MemoryStore) ListEpisodeSummaries(sessionID string) []app.EpisodeSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []app.EpisodeSummary{}
	for _, summary := range s.episodeSummaries {
		if sessionID == "" || summary.SessionID == sessionID {
			out = append(out, summary)
		}
	}
	slices.SortFunc(out, func(a, b app.EpisodeSummary) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	return out
}

func (s *MemoryStore) appendAuditLocked(typ, sessionID, runID, actor, summary string, fields map[string]any) {
	s.auditEvents = append(s.auditEvents, app.AuditEvent{
		ID:        app.NewID("audit"),
		Time:      time.Now().UTC(),
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Actor:     actor,
		Summary:   summary,
		Fields:    fields,
	})
}

func (s *MemoryStore) appendEventLocked(typ, sessionID, runID string, payload any) {
	s.events = append(s.events, app.Event{
		ID:        app.NewID("evt"),
		Time:      time.Now().UTC(),
		Type:      typ,
		SessionID: sessionID,
		RunID:     runID,
		Payload:   payload,
	})
}

func (s *MemoryStore) sessionIDForRunLocked(runID string) string {
	if run, ok := s.runs[runID]; ok {
		return run.SessionID
	}
	return ""
}

func deriveTitle(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "New SparkClaw Session"
	}
	runes := []rune(content)
	if len(runes) > 42 {
		return string(runes[:42]) + "..."
	}
	return content
}

func cloneMap[T any](in map[string]T) map[string]T {
	out := map[string]T{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSliceMap[T any](in map[string][]T) map[string][]T {
	out := map[string][]T{}
	for key, value := range in {
		out[key] = append([]T(nil), value...)
	}
	return out
}

func ensureMap[T any](in map[string]T) map[string]T {
	if in == nil {
		return map[string]T{}
	}
	return in
}

func ensureSliceMap[T any](in map[string][]T) map[string][]T {
	if in == nil {
		return map[string][]T{}
	}
	return in
}

func normalizeOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	if profile.ID == "" && profile.DisplayName == "" && profile.CreatedAt.IsZero() && profile.UpdatedAt.IsZero() {
		return app.DefaultOwnerProfile()
	}
	now := time.Now().UTC()
	if profile.ID == "" {
		profile.ID = app.DefaultOwnerID
	}
	if strings.TrimSpace(profile.DisplayName) == "" {
		profile.DisplayName = "Owner"
	}
	if profile.Preferences == nil {
		profile.Preferences = map[string]string{}
	} else {
		profile.Preferences = cloneStringMap(profile.Preferences)
	}
	if profile.CreatedAt.IsZero() {
		profile.CreatedAt = now
	}
	if profile.UpdatedAt.IsZero() {
		profile.UpdatedAt = profile.CreatedAt
	}
	return profile
}

func cloneOwnerProfile(profile app.OwnerProfile) app.OwnerProfile {
	profile.Preferences = cloneStringMap(profile.Preferences)
	return profile
}

func cloneStringMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range in {
		out[key] = value
	}
	return out
}
