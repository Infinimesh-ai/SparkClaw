package gateway

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"slices"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type iscpEventCursor struct {
	ID       string    `json:"id"`
	Scope    string    `json:"scope"`
	Revision uint64    `json:"revision"`
	Expires  time.Time `json:"expires"`
}
type iscpEventState struct {
	ID           string          `json:"id"`
	Revision     uint64          `json:"revision"`
	Acknowledged uint64          `json:"acknowledged"`
	Epoch        string          `json:"epoch"`
	LastEvent    string          `json:"last_event"`
	Requests     []string        `json:"requests"`
	Categories   []string        `json:"categories"`
	Snapshot     json.RawMessage `json:"snapshot"`
	Packet       json.RawMessage `json:"packet"`
	Expires      time.Time       `json:"expires"`
}

func (a *iscpDomainAdapter) sealEventCursor(cursor iscpEventCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, a.receipts.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := a.receipts.aead.Seal(nonce, nonce, raw, []byte("iscp-event-cursor-v1"))
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}
func (a *iscpDomainAdapter) readEventCursor(raw string) (iscpEventCursor, error) {
	var cursor iscpEventCursor
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	n := a.receipts.aead.NonceSize()
	if err != nil || len(sealed) <= n || len(sealed) > 4096 {
		return cursor, execution.ErrConflict
	}
	plain, err := a.receipts.aead.Open(nil, sealed[:n], sealed[n:], []byte("iscp-event-cursor-v1"))
	if err != nil {
		return cursor, err
	}
	err = json.Unmarshal(plain, &cursor)
	return cursor, err
}
func (a *iscpDomainAdapter) eventSnapshot(ctx context.Context, principal requestPrincipal, requests []string, categories []string) (json.RawMessage, error) {
	service, err := a.server.executionService()
	if err != nil {
		return nil, err
	}
	statuses := []execution.Status{}
	if slices.Contains(categories, "tasks") || slices.Contains(categories, "approvals") {
		for _, id := range requests {
			status, err := service.Lookup(principal.OwnerID, principal.ClientID, id)
			if err != nil {
				return nil, err
			}
			status.Result = nil
			status.PendingApprovals = nil
			statuses = append(statuses, status)
		}
	}
	snapshot := map[string]any{"executions": statuses}
	if slices.Contains(categories, "notifications") {
		revision, err := a.server.store.PassiveNotificationRevision(ctx, principal.OwnerID)
		if err != nil {
			return nil, err
		}
		snapshot["notification_revision"] = revision
	}
	return json.Marshal(snapshot)
}
func (a *iscpDomainAdapter) events(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	session, authenticated := iscpworkbench.SessionFromContext(ctx)
	if !authenticated {
		return domainError(403, "authenticated_session_required")
	}
	return a.eventsWithSession(ctx, request, session)
}
func (a *iscpDomainAdapter) eventsWithSession(ctx context.Context, request iscpworkbench.Request, session iscpworkbench.SessionInfo) iscpDomainResult {
	principal, _, err := a.executionIdentity(ctx, request)
	if err != nil {
		return domainError(403, "installation_required")
	}
	reader, ok := a.server.store.(store.EventWindowReader)
	if !ok {
		return domainError(503, "events_unavailable")
	}
	scope := a.transientScope(principal, request.InstallationID, session)
	epoch := a.server.started.UTC().Format(time.RFC3339Nano)
	a.eventMu.Lock()
	defer a.eventMu.Unlock()
	if request.Operation == iscpworkbench.OperationEventsSnapshot || request.Operation == iscpworkbench.OperationStateSnapshot || request.Operation == iscpworkbench.OperationEventsSubscribe {
		var input struct {
			RequestIDs []string `json:"request_ids"`
			Categories []string `json:"categories,omitempty"`
		}
		if domainDecode(request.Body, &input) != nil || len(input.RequestIDs) > 32 {
			return domainError(400, "invalid_input")
		}
		for _, id := range input.RequestIDs {
			if !execution.UUID(id) {
				return domainError(400, "invalid_input")
			}
		}
		permissions := iscpEventCategoryScopes()
		if len(input.Categories) == 0 {
			for category, permission := range permissions {
				if slices.Contains(session.Scopes, permission) {
					input.Categories = append(input.Categories, category)
				}
			}
			slices.Sort(input.Categories)
		}
		for _, category := range input.Categories {
			permission, known := permissions[category]
			if !known || !slices.Contains(session.Scopes, permission) {
				return domainError(403, "permission_denied")
			}
		}
		// Read durable event boundary first; a concurrent post-snapshot event may be
		// repeated, but can never disappear behind the snapshot boundary.
		window, err := reader.ReadEventWindow(ctx, "", store.EventWindowLimit, true)
		if err != nil {
			return domainError(503, "events_unavailable")
		}
		last := window.Cursor
		snapshot, err := a.eventSnapshot(ctx, principal, input.RequestIDs, input.Categories)
		if err != nil {
			return domainExecutionError(err)
		}
		// One bounded subscription per authenticated installation. A new snapshot
		// replaces the previous generation; old cursors cannot ACK the new state.
		if _, found, err := a.receipts.load(scope, "event-subscription"); err != nil {
			return domainError(503, "events_unavailable")
		} else if !found {
			entries, err := os.ReadDir(a.receipts.root)
			if err != nil || len(entries) >= 65536 {
				return domainError(507, "event_capacity")
			}
		}
		state := iscpEventState{ID: app.NewID("iscp_events"), Revision: 1, Epoch: epoch, LastEvent: last, Requests: input.RequestIDs, Categories: input.Categories, Snapshot: snapshot, Expires: domainNow().Add(24 * time.Hour)}
		cursor, err := a.sealEventCursor(iscpEventCursor{state.ID, scope, state.Revision, state.Expires})
		if err != nil {
			return domainError(503, "events_unavailable")
		}
		state.Packet, _ = json.Marshal(map[string]any{"cursor": cursor, "revision": state.Revision, "snapshot": json.RawMessage(snapshot), "events": []any{}, "epoch": epoch, "reset": true})
		if err = a.saveEventState(scope, state); err != nil {
			return domainError(503, "events_unavailable")
		}
		return iscpDomainResult{status: 200, body: state.Packet}
	}
	var input struct {
		Cursor string `json:"cursor"`
		Limit  int    `json:"limit,omitempty"`
	}
	if domainDecode(request.Body, &input) != nil || input.Limit < 0 || input.Limit > 100 {
		return domainError(400, "invalid_input")
	}
	cursor, err := a.readEventCursor(input.Cursor)
	if err != nil || cursor.Scope != scope || !cursor.Expires.After(domainNow()) {
		return domainError(409, "cursor_gap")
	}
	receipt, found, err := a.receipts.load(scope, "event-subscription")
	var state iscpEventState
	if err != nil || !found || json.Unmarshal(receipt.Body, &state) != nil || state.ID != cursor.ID || state.Epoch != epoch || state.Revision < cursor.Revision || (cursor.Revision < state.Acknowledged && request.Operation != iscpworkbench.OperationEventsAck) || !state.Expires.After(domainNow()) {
		return domainError(409, "cursor_gap")
	}
	permissions := iscpEventCategoryScopes()
	for _, category := range state.Categories {
		if !slices.Contains(session.Scopes, permissions[category]) {
			return domainError(403, "permission_denied")
		}
	}
	switch request.Operation {
	case iscpworkbench.OperationEventsAck:
		if cursor.Revision <= state.Acknowledged {
			return domainJSON(200, map[string]any{"acknowledged": true, "revision": cursor.Revision})
		}
		if cursor.Revision != state.Revision {
			return domainError(409, "cursor_gap")
		}
		state.Acknowledged = cursor.Revision
		if err = a.saveEventState(scope, state); err != nil {
			return domainError(503, "events_unavailable")
		}
		return domainJSON(200, map[string]any{"acknowledged": true, "revision": state.Revision})
	case iscpworkbench.OperationEventsUnsubscribe:
		state.Expires = domainNow()
		if err = a.saveEventState(scope, state); err != nil {
			return domainError(503, "events_unavailable")
		}
		return domainJSON(200, map[string]bool{"closed": true})
	}
	if state.Acknowledged < state.Revision {
		return iscpDomainResult{status: 200, body: state.Packet}
	}
	window, err := reader.ReadEventWindow(ctx, state.LastEvent, store.EventWindowLimit, false)
	if err != nil {
		return domainError(503, "events_unavailable")
	}
	if !window.CursorFound || window.More {
		return domainError(409, "cursor_gap")
	}
	snapshot, err := a.eventSnapshot(ctx, principal, state.Requests, state.Categories)
	if err != nil {
		return domainExecutionError(err)
	}
	events := []map[string]any{}
	limit := input.Limit
	if limit == 0 {
		limit = 100
	}
	for _, row := range window.Events {
		if len(events) >= limit {
			break
		}
		state.LastEvent = row.ID
		category := workbenchEventCategory(row.Type)
		if workbenchEventOwner(row) == principal.OwnerID && slices.Contains(state.Categories, category) {
			events = append(events, map[string]any{"sequence": state.Revision + 1, "category": category, "resource_id": workbenchEventResourceID(row), "reason": "changed"})
		}
	}
	if len(events) < limit && string(snapshot) != string(state.Snapshot) {
		events = append(events, map[string]any{"sequence": state.Revision + 1, "category": "state", "reason": "snapshot_changed"})
	}
	if len(events) == 0 {
		if err = a.saveEventState(scope, state); err != nil {
			return domainError(503, "events_unavailable")
		}
		return domainJSON(200, map[string]any{"cursor": input.Cursor, "revision": state.Revision, "events": events, "epoch": epoch})
	}
	for i := range events {
		events[i]["sequence"] = state.Revision + uint64(i) + 1
	}
	state.Revision += uint64(len(events))
	state.Snapshot = snapshot
	next, err := a.sealEventCursor(iscpEventCursor{state.ID, scope, state.Revision, state.Expires})
	if err != nil {
		return domainError(503, "events_unavailable")
	}
	state.Packet, _ = json.Marshal(map[string]any{"cursor": next, "revision": state.Revision, "events": events, "snapshot": json.RawMessage(snapshot), "epoch": epoch})
	if err = a.saveEventState(scope, state); err != nil {
		return domainError(503, "events_unavailable")
	}
	return iscpDomainResult{status: 200, body: state.Packet}
}
func (a *iscpDomainAdapter) saveEventState(scope string, state iscpEventState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return a.receipts.save(scope, "event-subscription", iscpDomainReceipt{Version: 1, Digest: "event-subscription", Operation: iscpworkbench.OperationEventsPull, Complete: true, Status: 200, Body: raw})
}

func iscpEventCategoryScopes() map[string]string {
	operations := map[string]string{"tasks": iscpworkbench.OperationLookup, "approvals": iscpworkbench.OperationApprovalsList, "notifications": iscpworkbench.OperationNotificationsList, "settings": iscpworkbench.OperationSettingsOwnerGet, "email": iscpworkbench.OperationMailSync}
	result := map[string]string{}
	for category, name := range operations {
		if spec, found := iscpworkbench.LookupOperation(name); found {
			result[category] = spec.Scope
		}
	}
	return result
}
