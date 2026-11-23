package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/agent"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/link"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcddining"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

// linkRunHub holds the in-memory state of runs started through the link
// layer: the streaming preview (which has no seq) and subscribers. On
// completion the preview becomes the stored final message.
type linkRunHub struct {
	mu       sync.Mutex
	sessions map[string]*linkRunState
}

type linkRunState struct {
	// seq numbers this conversation's events; it never resets, so a client
	// orders events across runs and connections.
	seq     int64
	status  contract.RunStatus
	binding execution.WorkbenchBinding
	subs    map[int]func(contract.RunEvent)
	next    int
	active  bool
	done    time.Time
}

func newLinkRunHub() *linkRunHub { return &linkRunHub{sessions: map[string]*linkRunState{}} }

func (h *linkRunHub) state(sessionID string) *linkRunState {
	st := h.sessions[sessionID]
	if st == nil {
		st = &linkRunState{subs: map[int]func(contract.RunEvent){}, status: contract.RunStatus{SessionID: sessionID, State: contract.RunIdle}}
		h.sessions[sessionID] = st
	}
	return st
}

// publish numbers ev and hands it to every subscriber in order. Callers
// hold h.mu, so events of one conversation are numbered and queued in the
// order they happened.
func (h *linkRunHub) publish(st *linkRunState, ev contract.RunEvent) {
	st.seq++
	ev.Seq = st.seq
	st.status.Seq = st.seq
	if ev.Status != nil {
		ev.Status.Seq = st.seq
	}
	for _, fn := range st.subs {
		fn(ev)
	}
}

// linkEventQueue delivers one subscription's events in order from a single
// goroutine. It never blocks the publisher; when the device falls too far
// behind, the oldest events are dropped and the seq gap tells the device
// to read run.status again.
type linkEventQueue struct {
	mu      sync.Mutex
	events  []contract.RunEvent
	wake    chan struct{}
	stopped bool
}

const linkEventQueueLimit = 2048

func newLinkEventQueue(ctx context.Context, emit func(contract.RunEvent) error) *linkEventQueue {
	q := &linkEventQueue{wake: make(chan struct{}, 1)}
	go func() {
		for {
			select {
			case <-ctx.Done():
				q.stop()
				return
			case <-q.wake:
			}
			for {
				q.mu.Lock()
				if q.stopped || len(q.events) == 0 {
					stopped := q.stopped
					q.mu.Unlock()
					if stopped {
						return
					}
					break
				}
				ev := q.events[0]
				q.events = q.events[1:]
				q.mu.Unlock()
				if emit(ev) != nil {
					q.stop()
					return
				}
			}
		}
	}()
	return q
}

func (q *linkEventQueue) push(ev contract.RunEvent) {
	q.mu.Lock()
	if q.stopped {
		q.mu.Unlock()
		return
	}
	q.events = append(q.events, ev)
	if len(q.events) > linkEventQueueLimit {
		q.events = q.events[len(q.events)-linkEventQueueLimit:]
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *linkEventQueue) stop() {
	q.mu.Lock()
	q.stopped = true
	q.events = nil
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func now3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func (h *linkRunHub) begin(sessionID, runID string, binding execution.WorkbenchBinding) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(sessionID)
	st.status = contract.RunStatus{SessionID: sessionID, RunID: runID, State: contract.RunRunning, UpdatedAt: now3339()}
	st.binding = binding
	st.active = true
	snap := st.status
	h.publish(st, contract.RunEvent{SessionID: sessionID, RunID: runID, Kind: "state", Status: &snap})
}

func (h *linkRunHub) delta(sessionID, runID, text string) {
	if text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(sessionID)
	if st.status.RunID != runID {
		return
	}
	st.status.Preview += text
	st.status.UpdatedAt = now3339()
	h.publish(st, contract.RunEvent{SessionID: sessionID, RunID: runID, Kind: "delta", Text: text})
}

func (h *linkRunHub) progress(sessionID, runID, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(sessionID)
	if st.status.RunID != runID || st.status.Progress == text {
		return
	}
	st.status.Progress = text
	st.status.UpdatedAt = now3339()
	h.publish(st, contract.RunEvent{SessionID: sessionID, RunID: runID, Kind: "progress", Text: text})
}

func (h *linkRunHub) finish(sessionID string, status contract.RunStatus) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(sessionID)
	if st.status.RunID != "" && status.RunID != "" && st.status.RunID != status.RunID && st.active {
		return
	}
	status.SessionID = sessionID
	status.UpdatedAt = now3339()
	st.status = status
	st.active = status.State == contract.RunRunning || status.State == contract.RunApprovalPending
	st.done = time.Now()
	snap := st.status
	h.publish(st, contract.RunEvent{SessionID: sessionID, RunID: status.RunID, Kind: "final", Status: &snap})
}

func (h *linkRunHub) snapshot(sessionID string) (contract.RunStatus, execution.WorkbenchBinding, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.sessions[sessionID]
	if st == nil {
		return contract.RunStatus{SessionID: sessionID, State: contract.RunIdle}, execution.WorkbenchBinding{}, false
	}
	return st.status, st.binding, st.active
}

func (h *linkRunHub) running(sessionID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.sessions[sessionID]
	return st != nil && st.active && st.status.State == contract.RunRunning
}

func (h *linkRunHub) subscribe(sessionID string, fn func(contract.RunEvent)) (contract.RunStatus, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.state(sessionID)
	st.next++
	id := st.next
	st.subs[id] = fn
	return st.status, func() {
		h.mu.Lock()
		delete(st.subs, id)
		h.mu.Unlock()
	}
}

// linkRequestID derives the workbench request UUID from the device and its
// client message id, so a retried send replays the original admission.
func linkRequestID(deviceID, clientMessageID string) string {
	sum := sha256.Sum256([]byte("sparkclaw-link-send-v1\x00" + deviceID + "\x00" + clientMessageID))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func linkRequestPrincipal(req *link.Request) requestPrincipal {
	return requestPrincipal{OwnerID: req.Principal.OwnerID, ActorID: req.Principal.OwnerID, ClientID: req.Principal.ClientID, DeviceID: req.Principal.DeviceID, Authenticated: true}
}

// linkConversationError maps shared conversation and execution errors to
// contract errors.
func linkConversationError(err error) error {
	if err == nil {
		return nil
	}
	var cerr *contract.Error
	if errors.As(err, &cerr) {
		return cerr
	}
	switch {
	case errors.Is(err, execution.ErrConflict):
		return link.Errorf(contract.CodeBusy, "another message is running in this conversation")
	case errors.Is(err, execution.ErrNotFound):
		return link.Errorf(contract.CodeNotFound, "not found")
	case errors.Is(err, execution.ErrUnavailable):
		return &contract.Error{Code: contract.CodeBackendUnavailable, Message: "the backend cannot run this now", Retryable: true}
	case errors.Is(err, errClientConnectionRevoked):
		return link.Errorf(contract.CodeRevoked, "this device was revoked")
	}
	var failure *approvalFailure
	if errors.As(err, &failure) {
		return &contract.Error{Code: linkCodeForStatus(failure.status), Message: publicConversationError(failure.cause).Error()}
	}
	return &contract.Error{Code: contract.CodeInternal, Message: publicConversationError(err).Error()}
}

func linkCodeForStatus(status int) string {
	switch {
	case status == 404:
		return contract.CodeNotFound
	case status == 409:
		return contract.CodeConflict
	case status == 400 || status == 413:
		return contract.CodeInvalidRequest
	case status == 401 || status == 403:
		return contract.CodeUnauthorized
	case status >= 500:
		return contract.CodeBackendUnavailable
	}
	return contract.CodeInternal
}

func (s *Server) linkMessageSend(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.MessageSendParams](req)
	if err != nil {
		return nil, err
	}
	p.ClientMessageID = strings.TrimSpace(p.ClientMessageID)
	if p.ClientMessageID == "" || len(p.ClientMessageID) > 128 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "client_message_id is required")
	}
	principal := linkRequestPrincipal(req)
	session, ok, err := s.sessionForPrincipal(ctx, principal, p.SessionID)
	if err != nil {
		return nil, linkConversationError(err)
	}
	if !ok {
		return nil, link.Errorf(contract.CodeNotFound, "conversation not found")
	}
	if session.Source == "mcp" {
		return nil, link.Errorf(contract.CodeConflict, "MCP conversations receive requirements through their binding")
	}
	if strings.TrimSpace(p.Content) == "" && len(p.Attachments) == 0 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "content or an attachment is required")
	}
	if len(p.Attachments) > 5 {
		return nil, link.Errorf(contract.CodeInvalidRequest, "at most 5 attachments")
	}
	attachments, err := s.linkAttachments(ctx, principal, p.Attachments)
	if err != nil {
		return nil, err
	}
	input := webMessageInput{RequestID: linkRequestID(req.Principal.DeviceID, p.ClientMessageID), Content: p.Content, Attachments: attachments, ClientTimezone: p.ClientTimezone}
	if err := validateWorkbenchInput(input); err != nil {
		return nil, link.Errorf(contract.CodeTooLarge, "%v", err)
	}
	ingress, err := s.webMessageIngressFor(ctx, principal, session, "", p.ClientTimezone)
	if err != nil {
		return nil, linkConversationError(err)
	}
	if p.ReplayOnly {
		return s.linkMessageReplay(ctx, principal, session, input, ingress, p)
	}
	admission, previous, err := s.admitWorkbenchMessageFor(ctx, principal, session, input, ingress, p.ClientMessageID)
	if err != nil {
		return nil, linkConversationError(err)
	}
	if previous != nil {
		msg, found, err := s.store.FindMessageByClientID(ctx, session.ID, p.ClientMessageID)
		if err != nil {
			return nil, linkConversationError(err)
		}
		if !found {
			msg = app.Message{SessionID: session.ID, Role: "user", Content: p.Content, ClientMessageID: p.ClientMessageID}
		}
		return contract.MessageSendResult{Message: linkMessage(msg), RunID: previous.RunID, Replayed: true}, nil
	}
	runID := admission.lease.RunID
	sessionID := session.ID
	releaseMCD := func() {}
	if req.Principal.Conditions.BrowserSurface && admission.runtime != nil {
		// The desktop's built-in browser runs this conversation's browser
		// tasks (design 3.12), as it did through the previous transport.
		if adapter, err := s.linkBrowserAdapter(req.Principal, sessionID, runID); err == nil {
			bound := admission.runtime.WithBrowser(adapter)
			admission.runtime = &bound
		}
		// McDonald's ordering follows the device's own authorization
		// (its conditions and declared reverse operations).
		authorization := s.linkExecutionAuthorization(req.Principal, s.linkDeviceReverse(req))
		var selection *mcddining.Selection
		selection, releaseMCD = s.linkRunMCDSelection(admission.lease.Context, authorization, req.Principal.OwnerID, req.Principal.ClientID, linkInstallationID(req.Principal.DeviceID), input.RequestID)
		if selection != nil {
			bound := admission.runtime.WithMCDSelection(selection)
			admission.runtime = &bound
		}
	}
	s.linkRunBegin(sessionID, runID, admission.binding)
	executionCtx := admission.context()
	if presentation, ok := linkClientPresentation(req.Principal.Principal, p); ok {
		// The desktop shows its local schedules itself, as it did through
		// the previous transport.
		executionCtx = agent.WithClientPresentation(executionCtx, p.ClientMessageID, p.ClientPresentation.LocalConversationID, presentation)
	}
	cleaned := sanitizeMessageAttachments(attachments)
	s.streamWG.Add(1)
	go func() {
		defer s.streamWG.Done()
		defer admission.close()
		defer releaseMCD()
		result, err := s.streamMessage(executionCtx, sessionID, input.Content, cleaned, ingress, func(event agent.StreamEvent) error {
			s.linkRunStream(sessionID, runID, event)
			return nil
		})
		var deliveryErr error
		if err == nil {
			_, deliveryErr = s.deliverAgentResult(executionCtx, result)
		}
		if finishErr := s.finishWorkbenchMessage(admission, result, err, deliveryErr); finishErr != nil && err == nil {
			err = finishErr
		}
		s.linkRunFinish(executionCtx, sessionID, runID, result, err)
	}()
	// The run is already admitted; a failed read only loses the stored
	// form of the input message, which the admission lease describes.
	msg, found, readErr := s.store.FindMessageByClientID(ctx, sessionID, p.ClientMessageID)
	if readErr != nil || !found {
		msg = app.Message{ID: admission.lease.InputMessageID, SessionID: sessionID, Role: "user", Content: p.Content, ClientMessageID: p.ClientMessageID, CreatedAt: time.Now().UTC()}
	}
	return contract.MessageSendResult{Message: linkMessage(msg), RunID: runID}, nil
}

// linkMessageReplay answers a send retried with replay_only: the original
// admission of this client_message_id, or not_found when it never arrived.
// Nothing is admitted.
func (s *Server) linkMessageReplay(ctx context.Context, principal requestPrincipal, session app.Session, input webMessageInput, ingress app.MessageIngressContext, p contract.MessageSendParams) (any, error) {
	binding, err := workbenchAdmissionBinding(principal, session, input, ingress)
	if err != nil {
		return nil, link.Errorf(contract.CodeInvalidRequest, "%v", err)
	}
	service, err := s.executionService()
	if err != nil {
		return nil, linkConversationError(err)
	}
	status, err := service.LookupWorkbenchClaim(binding)
	if errors.Is(err, execution.ErrNotFound) {
		return nil, &contract.Error{Code: contract.CodeNotFound, Message: "this message never reached the backend", NotSent: true}
	}
	if err != nil {
		return nil, linkConversationError(err)
	}
	if status.InputDigest != binding.InputDigest {
		return nil, link.Errorf(contract.CodeConflict, "client_message_id reused with different content")
	}
	msg, found, err := s.store.FindMessageByClientID(ctx, session.ID, p.ClientMessageID)
	if err != nil {
		return nil, linkConversationError(err)
	}
	if !found {
		msg = app.Message{SessionID: session.ID, Role: "user", Content: p.Content, ClientMessageID: p.ClientMessageID}
	}
	return contract.MessageSendResult{Message: linkMessage(msg), RunID: status.RunID, Replayed: true}, nil
}

// linkRunBegin, linkRunStream and linkRunFinish let link devices follow a
// run whichever entry admitted it (a link device or the host WebChat).
func (s *Server) linkRunBegin(sessionID, runID string, binding execution.WorkbenchBinding) {
	if s.link != nil {
		s.link.runs.begin(sessionID, runID, binding)
	}
}

func (s *Server) linkRunStream(sessionID, runID string, event agent.StreamEvent) {
	if s.link == nil {
		return
	}
	switch event.Type {
	case "text_delta":
		s.link.runs.delta(sessionID, runID, event.Text)
	case "tool_call.started", "tool_call.delta", "tool_call":
		if event.ToolName != "" {
			s.link.runs.progress(sessionID, runID, event.ToolName)
		}
	}
}

func (s *Server) linkRunFinish(ctx context.Context, sessionID, runID string, result agent.Result, err error) {
	if s.link != nil {
		s.link.runs.finish(sessionID, s.linkRunStatusFromResult(context.WithoutCancel(ctx), sessionID, runID, result, err))
	}
}

// linkStoredRunStatus answers run.status for a conversation whose latest
// run this process did not follow (a schedule, a messaging channel, or a
// run admitted before a restart): its persisted state decides.
func (s *Server) linkStoredRunStatus(ctx context.Context, sessionID string) (contract.RunStatus, bool) {
	runs, err := s.store.ListRuns(ctx, sessionID)
	if err != nil || len(runs) == 0 {
		return contract.RunStatus{}, false
	}
	latest := runs[0]
	status := contract.RunStatus{SessionID: sessionID, RunID: latest.ID, UpdatedAt: linkTime(latest.StartedAt)}
	if latest.CompletedAt != nil {
		status.UpdatedAt = linkTime(*latest.CompletedAt)
	}
	switch latest.State {
	case "running", "queued", "accepted":
		status.State = contract.RunRunning
	case "approval_pending", "browser_login_blocked":
		status.State = contract.RunApprovalPending
	case "completed":
		status.State = contract.RunCompleted
	case "failed":
		status.State = contract.RunFailed
	case "cancelled", "canceled":
		status.State = contract.RunCancelled
	case "blocked":
		status.State = contract.RunBlocked
	default:
		return contract.RunStatus{}, false
	}
	return status, true
}

func (s *Server) linkRunStatusFromResult(ctx context.Context, sessionID, runID string, result agent.Result, runErr error) contract.RunStatus {
	status := contract.RunStatus{SessionID: sessionID, RunID: runID}
	if runErr != nil {
		status.State = contract.RunFailed
		if ctx.Err() != nil || errors.Is(runErr, context.Canceled) {
			status.State = contract.RunCancelled
		}
		status.Error = &contract.Error{Code: contract.CodeInternal, Message: publicConversationError(runErr).Error()}
		return status
	}
	state := result.Run.State
	switch state {
	case "completed":
		status.State = contract.RunCompleted
	case "approval_pending", "browser_login_blocked":
		status.State = contract.RunApprovalPending
	case "failed":
		status.State = contract.RunFailed
	case "cancelled":
		status.State = contract.RunCancelled
	case "blocked":
		status.State = contract.RunBlocked
	default:
		status.State = contract.RunCompleted
	}
	if result.Message.ID != "" {
		stored, _, readErr := s.store.ListMessagePage(ctx, sessionID, 0, 0, 5)
		if readErr != nil {
			stored = nil
		}
		var final *app.Message
		for i := range stored {
			if stored[i].ID == result.Message.ID {
				final = &stored[i]
			}
		}
		if final == nil {
			m := result.Message
			final = &m
		}
		lm := linkMessage(*final)
		if result.ClientAction != nil {
			lm.Extra = linkMergeExtra(lm.Extra, "client_action", result.ClientAction)
		}
		status.FinalMessage = &lm
	}
	status.PendingApprovals = s.linkPendingApprovals(ctx, sessionID)
	return status
}

func (s *Server) linkPendingApprovals(ctx context.Context, sessionID string) []contract.Approval {
	approvals, err := s.store.ListApprovals(ctx, app.ApprovalStatusPending)
	if err != nil {
		return nil
	}
	var out []contract.Approval
	for _, a := range approvals {
		if sessionID != "" && a.SessionID != sessionID {
			continue
		}
		out = append(out, linkApproval(a))
	}
	return out
}

func (s *Server) linkRunStatus(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.RunRef](req)
	if err != nil {
		return nil, err
	}
	if _, ok, err := s.sessionForPrincipal(ctx, linkRequestPrincipal(req), p.SessionID); err != nil || !ok {
		return nil, link.Errorf(contract.CodeNotFound, "conversation not found")
	}
	status, _, tracked := s.link.runs.snapshot(p.SessionID)
	if (!tracked && status.State == contract.RunIdle) || (p.RunID != "" && status.RunID != p.RunID) {
		if stored, ok := s.linkStoredRunStatus(ctx, p.SessionID); ok && (p.RunID == "" || stored.RunID == p.RunID || status.RunID == "") {
			stored.Preview = ""
			if stored.RunID == status.RunID {
				stored.Seq = status.Seq
			}
			status = stored
		}
	}
	if status.State != contract.RunRunning {
		status.PendingApprovals = s.linkPendingApprovals(ctx, p.SessionID)
		if len(status.PendingApprovals) > 0 && status.State == contract.RunIdle {
			status.State = contract.RunApprovalPending
		}
	}
	return status, nil
}

func (s *Server) linkRunSubscribe(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.RunSubscribeParams](req)
	if err != nil {
		return nil, err
	}
	if req.Conn == nil {
		return nil, link.Errorf(contract.CodeUnsupportedCapability, "run.subscribe needs a duplex connection")
	}
	if _, ok, err := s.sessionForPrincipal(ctx, linkRequestPrincipal(req), p.SessionID); err != nil || !ok {
		return nil, link.Errorf(contract.CodeNotFound, "conversation not found")
	}
	subID := "run:" + p.SessionID
	conn := req.Conn
	qctx, stopQueue := context.WithCancel(conn.Context())
	queue := newLinkEventQueue(qctx, func(ev contract.RunEvent) error { return conn.Emit(subID, contract.TopicRun, ev) })
	status, unsubscribe := s.link.runs.subscribe(p.SessionID, queue.push)
	cancel := func() { unsubscribe(); stopQueue() }
	conn.OnClose(cancel)
	linkRegisterSubscription(conn, subID, cancel)
	if status.State != contract.RunRunning {
		status.PendingApprovals = s.linkPendingApprovals(ctx, p.SessionID)
	}
	return contract.RunSubscribeResult{Subscription: subID, Status: status}, nil
}

var linkSubscriptions sync.Map // *link.Conn -> *sync.Map(subID -> cancel)

func linkRegisterSubscription(conn *link.Conn, id string, cancel func()) {
	v, _ := linkSubscriptions.LoadOrStore(conn, &sync.Map{})
	m := v.(*sync.Map)
	if old, ok := m.Swap(id, cancel); ok {
		old.(func())()
	}
	conn.OnClose(func() { linkSubscriptions.Delete(conn) })
}

func (s *Server) linkRunUnsubscribe(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.RunSubscribeParams](req)
	if err != nil {
		return nil, err
	}
	if req.Conn != nil {
		if v, ok := linkSubscriptions.Load(req.Conn); ok {
			if c, ok := v.(*sync.Map).LoadAndDelete("run:" + p.SessionID); ok {
				c.(func())()
			}
		}
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) linkRunCancel(ctx context.Context, req *link.Request) (any, error) {
	p, err := link.DecodeParams[contract.RunRef](req)
	if err != nil {
		return nil, err
	}
	principal := linkRequestPrincipal(req)
	session, ok, err := s.sessionForPrincipal(ctx, principal, p.SessionID)
	if err != nil || !ok {
		return nil, link.Errorf(contract.CodeNotFound, "conversation not found")
	}
	status, binding, active := s.link.runs.snapshot(session.ID)
	if !active || status.State != contract.RunRunning || (p.RunID != "" && p.RunID != status.RunID) {
		return status, nil
	}
	service, err := s.executionService()
	if err != nil {
		return nil, linkConversationError(err)
	}
	if _, err := service.CancelWorkbench(binding); err != nil {
		return nil, linkConversationError(err)
	}
	cancelled := contract.RunStatus{SessionID: session.ID, RunID: status.RunID, State: contract.RunCancelled, Preview: status.Preview}
	s.link.runs.finish(session.ID, cancelled)
	return cancelled, nil
}

// linkClientPresentation accepts a desktop's local display capability.
func linkClientPresentation(principal contract.Principal, p contract.MessageSendParams) (app.ClientPresentation, bool) {
	cp := p.ClientPresentation
	if cp == nil || principal.Class != contract.ClassDesktop || strings.TrimSpace(cp.LocalConversationID) == "" || len(cp.LocalConversationID) > 128 {
		return app.ClientPresentation{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, cp.RequestedAt)
	if err != nil {
		return app.ClientPresentation{}, false
	}
	out := app.ClientPresentation{SchemaVersion: cp.SchemaVersion, ScheduleShow: cp.ScheduleShow, Timezone: cp.Timezone, RequestedAt: at.UTC()}
	return out, out.Valid()
}

// linkMergeExtra adds one key to a message's opaque presentation data.
func linkMergeExtra(extra json.RawMessage, key string, value any) json.RawMessage {
	fields := map[string]json.RawMessage{}
	if len(extra) > 0 {
		_ = json.Unmarshal(extra, &fields)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return extra
	}
	fields[key] = raw
	merged, err := json.Marshal(fields)
	if err != nil {
		return extra
	}
	return merged
}
