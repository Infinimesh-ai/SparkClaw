package jingsiruntime

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxResponseBytes = 131072

var (
	tokenPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)
	terminalStates   = []string{"succeeded", "failed", "canceled", "timed_out"}
	executionStates  = []string{"accepted", "queued", "running", "approval_required", "succeeded", "failed", "canceled", "timed_out"}
	approvalPolicies = []string{"deny", "ask", "allow_within_scope"}
)

type Executor interface {
	Execute(context.Context, ExecutionInput) (ExecutionOutput, error)
}

type Config struct {
	StateDir      string
	BearerToken   string
	CallerID      string
	MaxConcurrent int
	Now           func() time.Time
}

type Provider struct {
	store         *fileStore
	executor      Executor
	token         []byte
	callerID      string
	maxConcurrent int
	now           func() time.Time

	lifecycleMu sync.RWMutex
	lifecycle   context.Context
	started     bool
	startOnce   sync.Once
	sem         chan struct{}
	cancelMu    sync.Mutex
	cancels     map[string]context.CancelFunc
	wg          sync.WaitGroup
}

func New(config Config, executor Executor) (*Provider, error) {
	if executor == nil {
		return nil, errors.New("JingSi Runtime executor is required")
	}
	config.BearerToken = strings.TrimSpace(config.BearerToken)
	if len(config.BearerToken) < 16 {
		return nil, errors.New("JingSi Runtime bearer token must contain at least 16 characters")
	}
	config.CallerID = strings.TrimSpace(config.CallerID)
	if config.CallerID == "" {
		config.CallerID = "jingsi-service-v1"
	}
	if err := validateToken("caller id", config.CallerID, 128); err != nil {
		return nil, err
	}
	if config.MaxConcurrent <= 0 || config.MaxConcurrent > 64 {
		return nil, errors.New("JingSi Runtime max concurrency must be between 1 and 64")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	store, err := newFileStore(config.StateDir)
	if err != nil {
		return nil, err
	}
	return &Provider{
		store: store, executor: executor, token: []byte(config.BearerToken), callerID: config.CallerID,
		maxConcurrent: config.MaxConcurrent, now: config.Now, lifecycle: context.Background(),
		sem: make(chan struct{}, config.MaxConcurrent), cancels: map[string]context.CancelFunc{},
	}, nil
}

func (p *Provider) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.startOnce.Do(func() {
		p.lifecycleMu.Lock()
		p.lifecycle = ctx
		p.started = true
		p.lifecycleMu.Unlock()
		p.store.mu.Lock()
		pending := make([]string, 0)
		for _, value := range p.store.byExecution {
			if value.Kind == recordBound && !isTerminal(value.State) && value.State != "approval_required" {
				pending = append(pending, value.ExecutionID)
			}
		}
		p.store.mu.Unlock()
		slices.Sort(pending)
		for _, executionID := range pending {
			p.enqueue(executionID)
		}
	})
}

func (p *Provider) Wait(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Provider) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeProblem(w, http.StatusNotFound, "request_route", "not_found", false, 0)
		return
	}
	presented := bearerCredential(request.Header.Get("Authorization"))
	if presented == "" || subtle.ConstantTimeCompare([]byte(presented), p.token) != 1 {
		writeProblem(w, http.StatusUnauthorized, "request_unauthenticated", "unauthenticated", false, 0)
		return
	}
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != MediaType {
		writeProblem(w, http.StatusUnsupportedMediaType, "request_media_type", "invalid_request", false, 0)
		return
	}
	switch request.URL.Path {
	case "/v1/executions:submit":
		p.submit(w, request)
	case "/v1/executions:lookup":
		p.lookup(w, request)
	case "/v1/executions:status":
		p.status(w, request)
	case "/v1/executions:cancel":
		p.cancel(w, request)
	case "/v1/execution-events:list":
		p.events(w, request)
	default:
		writeProblem(w, http.StatusNotFound, "request_route", "not_found", false, 0)
	}
}

func (p *Provider) submit(w http.ResponseWriter, request *http.Request) {
	var value SubmitRequest
	if err := decodeRequest(request, &value); err != nil {
		writeProblem(w, http.StatusBadRequest, "request_invalid", "invalid_request", false, 0)
		return
	}
	if err := validateSubmitRequest(value); err != nil || request.Header.Get("Idempotency-Key") != value.Payload.RequestKey {
		writeProblem(w, http.StatusBadRequest, safeRequestID(value.RequestID), "invalid_request", false, 0)
		return
	}
	if !value.Authorization.DeadlineAt.After(p.now().UTC()) {
		writeProblem(w, http.StatusForbidden, value.RequestID, "permission_denied", false, 0)
		return
	}
	authorizationHash, err := canonicalAuthorizationHash(value.Authorization)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, value.RequestID, "internal_error", true, 0)
		return
	}
	semanticHash, err := canonicalSubmitHash(p.callerID, value.Authorization, value.Payload)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, value.RequestID, "internal_error", true, 0)
		return
	}
	key := p.store.key(p.callerID, value.Authorization.SpaceID, value.Payload.RequestKey)
	p.store.mu.Lock()
	existing := p.store.byKey[key]
	if existing != nil {
		if existing.Kind != recordBound || existing.AuthorizationHash != authorizationHash || existing.SemanticHash != semanticHash {
			p.store.mu.Unlock()
			writeProblem(w, http.StatusConflict, value.RequestID, "idempotency_conflict", false, 0)
			return
		}
		response := executionRef(existing)
		p.store.mu.Unlock()
		writeJSON(w, http.StatusAccepted, map[string]any{
			"protocol": Protocol, "kind": "execution.submit.accepted", "request_id": value.RequestID,
			"payload": map[string]any{"request_key": value.Payload.RequestKey, "execution": response},
		})
		return
	}
	now := p.now().UTC()
	executionID := deterministicID("execution", p.callerID, value.Authorization.SpaceID, value.Payload.RequestKey)
	traceRef := OpaqueRef{ID: "trace:" + executionID, Version: "v1"}
	accepted := now
	recordValue := &record{
		Version: 1, Kind: recordBound, CallerID: p.callerID, RequestKey: value.Payload.RequestKey,
		Authorization: value.Authorization, AuthorizationHash: authorizationHash, SemanticHash: semanticHash,
		Submit: &value.Payload, ExecutionID: executionID, State: "queued", AcceptedAt: &accepted, UpdatedAt: now,
		Events: []ExecutionEvent{
			{Sequence: 1, EventID: executionID + ":event:1", At: now, Type: "execution.accepted", State: "accepted"},
			{Sequence: 2, EventID: executionID + ":event:2", At: nextTime(now), Type: "execution.queued", State: "queued"},
		},
	}
	recordValue.UpdatedAt = recordValue.Events[1].At
	if err := p.store.persistLocked(recordValue); err != nil {
		p.store.mu.Unlock()
		writeProblem(w, http.StatusServiceUnavailable, value.RequestID, "runtime_unavailable", true, 1000)
		return
	}
	p.store.byKey[key] = recordValue
	p.store.byExecution[executionID] = recordValue
	response := executionRefWithTrace(recordValue, &traceRef)
	p.store.mu.Unlock()
	p.enqueue(executionID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"protocol": Protocol, "kind": "execution.submit.accepted", "request_id": value.RequestID,
		"payload": map[string]any{"request_key": value.Payload.RequestKey, "execution": response},
	})
}

func (p *Provider) lookup(w http.ResponseWriter, request *http.Request) {
	var value LookupRequest
	if err := decodeRequest(request, &value); err != nil || validateCommon(value.Protocol, value.Kind, "execution.lookup.request", value.RequestID, value.Authorization) != nil ||
		validateToken("request key", value.Payload.RequestKey, 256) != nil {
		writeProblem(w, http.StatusBadRequest, safeRequestID(value.RequestID), "invalid_request", false, 0)
		return
	}
	authorizationHash, _ := canonicalAuthorizationHash(value.Authorization)
	key := p.store.key(p.callerID, value.Authorization.SpaceID, value.Payload.RequestKey)
	p.store.mu.Lock()
	existing := p.store.byKey[key]
	if existing != nil {
		if existing.AuthorizationHash != authorizationHash {
			p.store.mu.Unlock()
			writeProblem(w, http.StatusForbidden, value.RequestID, "permission_denied", false, 0)
			return
		}
		if existing.Kind == recordBound {
			response := executionRef(existing)
			p.store.mu.Unlock()
			writeJSON(w, http.StatusOK, map[string]any{
				"protocol": Protocol, "kind": "execution.lookup.result", "request_id": value.RequestID,
				"payload": map[string]any{"request_key": value.Payload.RequestKey, "outcome": "bound", "execution": response},
			})
			return
		}
		fenceID, committedAt := existing.FenceID, *existing.FenceCommittedAt
		p.store.mu.Unlock()
		writeLookupFence(w, value.RequestID, value.Payload.RequestKey, fenceID, committedAt)
		return
	}
	now := p.now().UTC()
	fenceID := deterministicID("negative-fence", p.callerID, value.Authorization.SpaceID, value.Payload.RequestKey)
	fenced := &record{
		Version: 1, Kind: recordFenced, CallerID: p.callerID, RequestKey: value.Payload.RequestKey,
		Authorization: value.Authorization, AuthorizationHash: authorizationHash, UpdatedAt: now,
		FenceID: fenceID, FenceCommittedAt: &now,
	}
	if err := p.store.persistLocked(fenced); err != nil {
		p.store.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"protocol": Protocol, "kind": "execution.lookup.result", "request_id": value.RequestID,
			"payload": map[string]any{"request_key": value.Payload.RequestKey, "outcome": "unresolved", "retry_after_ms": 1000},
		})
		return
	}
	p.store.byKey[key] = fenced
	p.store.mu.Unlock()
	writeLookupFence(w, value.RequestID, value.Payload.RequestKey, fenceID, now)
}

func (p *Provider) status(w http.ResponseWriter, request *http.Request) {
	var value ExecutionRequest
	if err := decodeRequest(request, &value); err != nil || validateCommon(value.Protocol, value.Kind, "execution.status.request", value.RequestID, value.Authorization) != nil ||
		validateToken("execution id", value.Payload.ExecutionID, 256) != nil {
		writeProblem(w, http.StatusBadRequest, safeRequestID(value.RequestID), "invalid_request", false, 0)
		return
	}
	p.store.mu.Lock()
	recordValue, problem := p.authorizedExecutionLocked(value.Payload.ExecutionID, value.Authorization)
	if problem != "" {
		p.store.mu.Unlock()
		writeProblem(w, problemStatus(problem), value.RequestID, problem, false, 0)
		return
	}
	ref := executionRef(recordValue)
	payload := map[string]any{"execution": ref, "updated_at": recordValue.UpdatedAt}
	if recordValue.Result != nil {
		payload["result"] = *recordValue.Result
	}
	p.store.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": Protocol, "kind": "execution.status.result", "request_id": value.RequestID, "payload": payload,
	})
}

func (p *Provider) events(w http.ResponseWriter, request *http.Request) {
	var value EventsRequest
	if err := decodeRequest(request, &value); err != nil || validateCommon(value.Protocol, value.Kind, "execution.events.request", value.RequestID, value.Authorization) != nil ||
		validateToken("execution id", value.Payload.ExecutionID, 256) != nil || value.Payload.Limit < 1 || value.Payload.Limit > 100 {
		writeProblem(w, http.StatusBadRequest, safeRequestID(value.RequestID), "invalid_request", false, 0)
		return
	}
	p.store.mu.Lock()
	recordValue, problem := p.authorizedExecutionLocked(value.Payload.ExecutionID, value.Authorization)
	if problem != "" {
		p.store.mu.Unlock()
		writeProblem(w, problemStatus(problem), value.RequestID, problem, false, 0)
		return
	}
	events := make([]ExecutionEvent, 0, value.Payload.Limit)
	next := value.Payload.AfterSequence
	for _, event := range recordValue.Events {
		if event.Sequence <= value.Payload.AfterSequence {
			continue
		}
		if len(events) == value.Payload.Limit {
			break
		}
		events = append(events, event)
		next = event.Sequence
	}
	terminal := isTerminal(recordValue.State)
	p.store.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": Protocol, "kind": "execution.events.page", "request_id": value.RequestID,
		"payload": map[string]any{"execution_id": value.Payload.ExecutionID, "events": events, "next_sequence": next, "terminal": terminal},
	})
}

func (p *Provider) cancel(w http.ResponseWriter, request *http.Request) {
	var value CancelRequest
	if err := decodeRequest(request, &value); err != nil || validateCommon(value.Protocol, value.Kind, "execution.cancel.request", value.RequestID, value.Authorization) != nil ||
		validateToken("execution id", value.Payload.ExecutionID, 256) != nil || validateToken("reason code", value.Payload.ReasonCode, 128) != nil {
		writeProblem(w, http.StatusBadRequest, safeRequestID(value.RequestID), "invalid_request", false, 0)
		return
	}
	p.store.mu.Lock()
	recordValue, problem := p.authorizedExecutionLocked(value.Payload.ExecutionID, value.Authorization)
	if problem != "" {
		p.store.mu.Unlock()
		writeProblem(w, problemStatus(problem), value.RequestID, problem, false, 0)
		return
	}
	state := recordValue.State
	if !isTerminal(state) {
		recordValue.CancelRequested = true
		recordValue.UpdatedAt = nextTime(recordValue.UpdatedAt)
		if state == "approval_required" {
			p.finishLocked(recordValue, "canceled", "")
			state = "canceled"
		} else {
			if err := p.store.persistLocked(recordValue); err != nil {
				p.store.mu.Unlock()
				writeProblem(w, http.StatusServiceUnavailable, value.RequestID, "runtime_unavailable", true, 1000)
				return
			}
			state = "cancel_requested"
		}
	}
	updatedAt := recordValue.UpdatedAt
	p.store.mu.Unlock()
	if state == "cancel_requested" {
		p.cancelMu.Lock()
		cancel := p.cancels[value.Payload.ExecutionID]
		p.cancelMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": Protocol, "kind": "execution.cancel.result", "request_id": value.RequestID,
		"payload": map[string]any{"execution_id": value.Payload.ExecutionID, "state": state, "idempotent": true, "updated_at": updatedAt},
	})
}

func (p *Provider) authorizedExecutionLocked(executionID string, authorization Authorization) (*record, string) {
	value := p.store.byExecution[executionID]
	if value == nil {
		return nil, "not_found"
	}
	hash, err := canonicalAuthorizationHash(authorization)
	if err != nil || hash != value.AuthorizationHash {
		return nil, "not_found"
	}
	return value, ""
}

func (p *Provider) enqueue(executionID string) {
	p.lifecycleMu.RLock()
	ctx := p.lifecycle
	p.lifecycleMu.RUnlock()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return
		}
		p.execute(ctx, executionID)
	}()
}

func (p *Provider) execute(lifecycle context.Context, executionID string) {
	p.store.mu.Lock()
	value := p.store.byExecution[executionID]
	if value == nil || isTerminal(value.State) || value.State == "approval_required" {
		p.store.mu.Unlock()
		return
	}
	if value.CancelRequested {
		p.finishLocked(value, "canceled", "")
		p.store.mu.Unlock()
		return
	}
	value.State = "running"
	value.UpdatedAt = nextTime(value.UpdatedAt)
	p.appendEventLocked(value, "execution.running", "running", "")
	if err := p.store.persistLocked(value); err != nil {
		// The in-memory record already says running; without a terminal
		// outcome status polls would report it as running forever.
		p.finishLocked(value, "failed", "runtime state could not be persisted")
		p.store.mu.Unlock()
		return
	}
	input := ExecutionInput{
		ExecutionID: value.ExecutionID, Authorization: value.Authorization, Goal: value.Submit.Goal,
		Memory: value.Submit.MemoryContext, Budget: value.Submit.Budget,
	}
	acceptedAt := *value.AcceptedAt
	deadline := value.Authorization.DeadlineAt
	budgetDeadline := acceptedAt.Add(time.Duration(value.Submit.Budget.MaxRuntimeMS) * time.Millisecond)
	if budgetDeadline.Before(deadline) {
		deadline = budgetDeadline
	}
	p.store.mu.Unlock()

	executionCtx, cancel := context.WithDeadline(lifecycle, deadline)
	p.cancelMu.Lock()
	p.cancels[executionID] = cancel
	p.cancelMu.Unlock()
	output, executionErr := p.executor.Execute(executionCtx, input)
	executionContextErr := executionCtx.Err()
	cancel()
	p.cancelMu.Lock()
	delete(p.cancels, executionID)
	p.cancelMu.Unlock()

	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	value = p.store.byExecution[executionID]
	if value == nil || isTerminal(value.State) {
		return
	}
	state := output.State
	if value.CancelRequested || errors.Is(executionContextErr, context.Canceled) {
		state = "canceled"
	} else if errors.Is(executionContextErr, context.DeadlineExceeded) {
		state = "timed_out"
	} else if executionErr != nil {
		state = "failed"
	}
	if !slices.Contains(executionStates, state) || state == "accepted" || state == "queued" || state == "running" {
		state = "failed"
	}
	if state == "approval_required" {
		value.State = state
		value.UpdatedAt = nextTime(value.UpdatedAt)
		p.appendEventLocked(value, "execution.approval_required", state, "approval_required")
		_ = p.store.persistLocked(value)
		return
	}
	traceRef := output.TraceRef
	if traceRef.ID == "" {
		traceRef = OpaqueRef{ID: "trace:" + value.ExecutionID, Version: "v1"}
	}
	value.Result = &ExecutionResult{
		Outcome: state, CompletedAt: nextTime(value.UpdatedAt), Summary: boundedSummary(output.Summary, value.Submit.Budget.MaxOutputBytes),
		ArtifactRefs: append([]ArtifactRef(nil), output.ArtifactRefs...), TraceRef: traceRef,
	}
	p.finishLocked(value, state, output.Summary)
}

func (p *Provider) finishLocked(value *record, state, summary string) {
	if isTerminal(value.State) {
		return
	}
	value.State = state
	value.UpdatedAt = nextTime(value.UpdatedAt)
	completed := value.UpdatedAt
	value.CompletedAt = &completed
	if value.Result == nil {
		value.Result = &ExecutionResult{
			Outcome: state, CompletedAt: completed, Summary: boundedSummary(summary, value.Submit.Budget.MaxOutputBytes), ArtifactRefs: []ArtifactRef{},
			TraceRef: OpaqueRef{ID: "trace:" + value.ExecutionID, Version: "v1"},
		}
	} else {
		value.Result.Outcome = state
		value.Result.CompletedAt = completed
		value.Result.Summary = boundedSummary(value.Result.Summary, value.Submit.Budget.MaxOutputBytes)
		if value.Result.ArtifactRefs == nil {
			value.Result.ArtifactRefs = []ArtifactRef{}
		}
	}
	p.appendEventLocked(value, "execution."+state, state, "")
	_ = p.store.persistLocked(value)
}

func (p *Provider) appendEventLocked(value *record, eventType, state, summaryCode string) {
	sequence := uint64(len(value.Events) + 1)
	event := ExecutionEvent{
		Sequence: sequence, EventID: fmt.Sprintf("%s:event:%d", value.ExecutionID, sequence),
		At: value.UpdatedAt, Type: eventType, State: state, SummaryCode: summaryCode,
	}
	if isTerminal(state) {
		event.TraceRef = &OpaqueRef{ID: "trace:" + value.ExecutionID, Version: "v1"}
	}
	value.Events = append(value.Events, event)
}

func decodeRequest(request *http.Request, target any) error {
	defer request.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(request.Body, maxResponseBytes+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return requireJSONEOF(decoder)
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("unexpected trailing JSON value")
	}
	return err
}

func validateSubmitRequest(value SubmitRequest) error {
	if err := validateCommon(value.Protocol, value.Kind, "execution.submit.request", value.RequestID, value.Authorization); err != nil {
		return err
	}
	if err := validateToken("request key", value.Payload.RequestKey, 256); err != nil {
		return err
	}
	if strings.TrimSpace(value.Payload.Goal) == "" || utf8.RuneCountInString(value.Payload.Goal) > 8192 || !utf8.ValidString(value.Payload.Goal) {
		return errors.New("goal is invalid")
	}
	if value.Payload.Target != "sparkclaw" || value.Payload.Budget.MaxRuntimeMS < 1 || value.Payload.Budget.MaxRuntimeMS > 86400000 ||
		value.Payload.Budget.MaxToolCalls < 0 || value.Payload.Budget.MaxToolCalls > 1024 ||
		value.Payload.Budget.MaxOutputBytes < 1 || value.Payload.Budget.MaxOutputBytes > 1048576 {
		return errors.New("submit budget or target is invalid")
	}
	if value.Payload.MemoryContext != nil {
		memory := value.Payload.MemoryContext
		if strings.TrimSpace(memory.Summary) == "" || !utf8.ValidString(memory.Summary) || utf8.RuneCountInString(memory.Summary) > 65536 ||
			math.IsNaN(memory.Confidence) || math.IsInf(memory.Confidence, 0) || memory.Confidence < 0 || memory.Confidence > 1 ||
			len(memory.MemoryRefs) < 1 || len(memory.MemoryRefs) > 64 || len(memory.SourceRefs) > 64 {
			return errors.New("memory context is invalid")
		}
		if err := validateRefs(memory.MemoryRefs); err != nil {
			return err
		}
		if err := validateRefs(memory.SourceRefs); err != nil {
			return err
		}
	}
	return nil
}

func validateCommon(protocol, kind, expectedKind, requestID string, authorization Authorization) error {
	if protocol != Protocol || kind != expectedKind {
		return errors.New("protocol or kind is invalid")
	}
	if err := validateToken("request id", requestID, 256); err != nil {
		return err
	}
	if err := validateToken("space id", authorization.SpaceID, 256); err != nil {
		return err
	}
	if err := validateToken("task id", authorization.TaskID, 256); err != nil {
		return err
	}
	if err := validateToken("purpose", authorization.Purpose.Name, 128); err != nil {
		return err
	}
	if err := validateRef(authorization.Grant); err != nil {
		return err
	}
	if !slices.Contains(approvalPolicies, authorization.ApprovalPolicy) || authorization.DeadlineAt.IsZero() || authorization.DeadlineAt.Location() != time.UTC {
		return errors.New("authorization policy or deadline is invalid")
	}
	for _, scope := range [][]string{authorization.ToolScope, authorization.DataScope, authorization.NetworkScope} {
		if len(scope) > 64 {
			return errors.New("authorization scope exceeds 64 entries")
		}
		seen := map[string]bool{}
		for _, item := range scope {
			if err := validateToken("authorization scope", item, 128); err != nil || seen[item] {
				return errors.New("authorization scope is invalid")
			}
			seen[item] = true
		}
	}
	return nil
}

func validateRefs(values []OpaqueRef) error {
	for _, value := range values {
		if err := validateRef(value); err != nil {
			return err
		}
	}
	return nil
}

func validateRef(value OpaqueRef) error {
	if err := validateToken("reference id", value.ID, 256); err != nil {
		return err
	}
	return validateToken("reference version", value.Version, 128)
}

func validateToken(name, value string, maximum int) error {
	if value == "" || len(value) > maximum || !tokenPattern.MatchString(value) {
		return fmt.Errorf("%s is invalid", name)
	}
	return nil
}

func executionRef(value *record) ExecutionRef {
	return executionRefWithTrace(value, &OpaqueRef{ID: "trace:" + value.ExecutionID, Version: "v1"})
}

func executionRefWithTrace(value *record, trace *OpaqueRef) ExecutionRef {
	return ExecutionRef{ExecutionID: value.ExecutionID, State: value.State, AcceptedAt: *value.AcceptedAt, TraceRef: trace}
}

func writeLookupFence(w http.ResponseWriter, requestID, requestKey, fenceID string, committedAt time.Time) {
	writeJSON(w, http.StatusOK, map[string]any{
		"protocol": Protocol, "kind": "execution.lookup.result", "request_id": requestID,
		"payload": map[string]any{
			"request_key": requestKey, "outcome": "not_started",
			"negative_fence": map[string]any{"fence_id": fenceID, "committed_at": committedAt, "reason_code": "submission_not_started"},
		},
	})
}

func writeProblem(w http.ResponseWriter, status int, requestID, code string, retryable bool, retryAfterMS int) {
	payload := map[string]any{"code": code, "retryable": retryable, "side_effects": "none"}
	if retryAfterMS > 0 {
		payload["retry_after_ms"] = retryAfterMS
	}
	writeJSON(w, status, map[string]any{"protocol": Protocol, "kind": "problem", "request_id": safeRequestID(requestID), "payload": payload})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > maxResponseBytes {
		raw = []byte(`{"protocol":"jingsi-sparkclaw/v1","kind":"problem","request_id":"request_internal","payload":{"code":"internal_error","retryable":true,"side_effects":"none"}}`)
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", MediaType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

func safeRequestID(value string) string {
	if validateToken("request id", value, 256) == nil {
		return value
	}
	return "request_invalid"
}

func bearerCredential(value string) string {
	const prefix = "Bearer "
	if !strings.HasPrefix(value, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(value, prefix))
}

func deterministicID(prefix string, values ...string) string {
	digest := sha256.New()
	for _, value := range values {
		_, _ = digest.Write([]byte(value))
		_, _ = digest.Write([]byte{0})
	}
	return prefix + ":" + hex.EncodeToString(digest.Sum(nil))[:32]
}

func nextTime(value time.Time) time.Time {
	return value.UTC().Add(time.Microsecond)
}

func isTerminal(state string) bool {
	return slices.Contains(terminalStates, state)
}

func problemStatus(code string) int {
	if code == "permission_denied" {
		return http.StatusForbidden
	}
	return http.StatusNotFound
}

func boundedSummary(value string, maximumBytes int) string {
	if !utf8.ValidString(value) {
		return ""
	}
	if maximumBytes <= 0 || maximumBytes > 65536 {
		maximumBytes = 65536
	}
	if len(value) <= maximumBytes {
		return value
	}
	value = value[:maximumBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
