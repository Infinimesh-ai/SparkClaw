package store

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"
)

const readinessFailureThreshold = 3

type BackendKind string

const (
	BackendMemory   BackendKind = "memory"
	BackendFile     BackendKind = "file"
	BackendPostgres BackendKind = "postgres"
)

type RuntimeState string

const (
	RuntimeStateStarting RuntimeState = "starting"
	RuntimeStateReady    RuntimeState = "ready"
	RuntimeStateUnready  RuntimeState = "unready"
	RuntimeStateClosing  RuntimeState = "closing"
	RuntimeStateClosed   RuntimeState = "closed"
)

var ErrRuntimeClosing = errors.New("store runtime is closing")

type RuntimeStatus struct {
	Backend       BackendKind  `json:"backend"`
	State         RuntimeState `json:"state"`
	Ready         bool         `json:"ready"`
	Durable       bool         `json:"durable"`
	ReasonCode    string       `json:"reason_code,omitempty"`
	Active        int          `json:"active_operations"`
	StartedAt     time.Time    `json:"started_at"`
	LastProbeAt   *time.Time   `json:"last_probe_at,omitempty"`
	DegradedAt    *time.Time   `json:"degraded_at,omitempty"`
	LastRecovered *time.Time   `json:"last_recovered_at,omitempty"`
}

type OperationMetric struct {
	Operation       StoreOperation `json:"operation"`
	Repository      string         `json:"repository"`
	Mode            string         `json:"mode"`
	Outcome         string         `json:"outcome"`
	Count           uint64         `json:"count"`
	DurationSeconds float64        `json:"duration_seconds"`
}

type operationMetricKey struct {
	operation StoreOperation
	outcome   string
}

type operationMetricValue struct {
	count    uint64
	duration time.Duration
}

type Supervisor struct {
	mu              sync.Mutex
	backend         BackendKind
	durable         bool
	now             func() time.Time
	state           RuntimeState
	ready           bool
	reasonCode      string
	startedAt       time.Time
	lastProbeAt     *time.Time
	degradedAt      *time.Time
	lastRecoveredAt *time.Time
	closing         bool
	active          int
	idle            chan struct{}
	metrics         map[operationMetricKey]operationMetricValue
	failureStreaks  map[StoreOperation]int
}

func newSupervisor(backend BackendKind, now func() time.Time) *Supervisor {
	if now == nil {
		now = time.Now
	}
	idle := make(chan struct{})
	close(idle)
	return &Supervisor{
		backend: backend, durable: backend != BackendMemory, now: now,
		state: RuntimeStateStarting, startedAt: now().UTC(), idle: idle,
		metrics:        map[operationMetricKey]operationMetricValue{},
		failureStreaks: map[StoreOperation]int{},
	}
}

func (s *Supervisor) begin(operation StoreOperation) (*operationSpan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return nil, false
	}
	if s.active == 0 {
		s.idle = make(chan struct{})
	}
	s.active++
	return &operationSpan{supervisor: s, operation: operation, startedAt: s.now()}, true
}

func (s *Supervisor) finish(span *operationSpan, code StoreErrorCode) {
	now := s.now()
	duration := now.Sub(span.startedAt)
	if duration < 0 {
		duration = 0
	}
	outcome := "success"
	if code != "" {
		outcome = string(code)
	}

	s.mu.Lock()
	key := operationMetricKey{operation: span.operation, outcome: outcome}
	metric := s.metrics[key]
	metric.count++
	metric.duration += duration
	s.metrics[key] = metric
	s.observeHealthLocked(span.operation, code, now.UTC())
	s.active--
	if s.active == 0 {
		close(s.idle)
	}
	s.mu.Unlock()
}

func (s *Supervisor) observeHealthLocked(operation StoreOperation, code StoreErrorCode, now time.Time) {
	// Every registered operation affects readiness; unregistered operations
	// cannot influence health.
	if _, exists := operationSpecs[operation]; !exists || s.closing {
		return
	}
	if code != StoreErrorTimeout && code != StoreErrorUnavailable {
		delete(s.failureStreaks, operation)
	}
	if !s.ready {
		return
	}
	switch code {
	case StoreErrorCorrupt:
		s.degradeLocked(code, now)
	case StoreErrorDurability, StoreErrorUnknownOutcome:
		if s.durable {
			s.degradeLocked(code, now)
		}
	case StoreErrorTimeout, StoreErrorUnavailable:
		s.failureStreaks[operation]++
		if s.failureStreaks[operation] >= readinessFailureThreshold {
			s.degradeLocked(code, now)
		}
	}
}

func (s *Supervisor) degradeLocked(code StoreErrorCode, now time.Time) {
	if !s.ready {
		return
	}
	s.ready = false
	s.state = RuntimeStateUnready
	s.reasonCode = string(code)
	timestamp := now
	s.degradedAt = &timestamp
}

func (s *Supervisor) recordProbe(err error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastProbeAt = cloneRuntimeTime(now)
	if s.closing {
		return
	}
	if err != nil {
		s.ready = false
		s.state = RuntimeStateUnready
		s.reasonCode = probeReasonCode(err)
		if s.degradedAt == nil {
			s.degradedAt = cloneRuntimeTime(now)
		}
		return
	}
	s.ready = true
	s.state = RuntimeStateReady
	s.reasonCode = ""
	s.failureStreaks = map[StoreOperation]int{}
	s.lastRecoveredAt = cloneRuntimeTime(now)
}

func probeReasonCode(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return string(StoreErrorCanceled)
	case errors.Is(err, context.DeadlineExceeded):
		return string(StoreErrorTimeout)
	default:
		return string(StoreErrorUnavailable)
	}
}

func (s *Supervisor) startClose() {
	s.mu.Lock()
	s.closing = true
	s.ready = false
	s.state = RuntimeStateClosing
	s.reasonCode = "closing"
	s.mu.Unlock()
}

func (s *Supervisor) wait(ctx context.Context) error {
	s.mu.Lock()
	idle := s.idle
	s.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Supervisor) finishClose() {
	s.mu.Lock()
	s.state = RuntimeStateClosed
	s.reasonCode = "closed"
	s.mu.Unlock()
}

func (s *Supervisor) Status() RuntimeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return RuntimeStatus{
		Backend: s.backend, State: s.state, Ready: s.ready, Durable: s.durable,
		ReasonCode: s.reasonCode, Active: s.active, StartedAt: s.startedAt,
		LastProbeAt:   cloneRuntimeTimePointer(s.lastProbeAt),
		DegradedAt:    cloneRuntimeTimePointer(s.degradedAt),
		LastRecovered: cloneRuntimeTimePointer(s.lastRecoveredAt),
	}
}

func (s *Supervisor) Metrics() []OperationMetric {
	s.mu.Lock()
	defer s.mu.Unlock()
	metrics := make([]OperationMetric, 0, len(s.metrics))
	for key, value := range s.metrics {
		spec := operationSpecs[key.operation]
		metrics = append(metrics, OperationMetric{
			Operation: key.operation, Repository: spec.Repository, Mode: string(spec.Mode),
			Outcome: key.outcome, Count: value.count, DurationSeconds: value.duration.Seconds(),
		})
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Operation != metrics[j].Operation {
			return metrics[i].Operation < metrics[j].Operation
		}
		return metrics[i].Outcome < metrics[j].Outcome
	})
	return metrics
}

func cloneRuntimeTime(value time.Time) *time.Time {
	copy := value
	return &copy
}

func cloneRuntimeTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	return cloneRuntimeTime(*value)
}

type operationSpan struct {
	mu         sync.Mutex
	supervisor *Supervisor
	operation  StoreOperation
	startedAt  time.Time
	code       StoreErrorCode
	finished   bool
}

func (s *operationSpan) mark(code StoreErrorCode) {
	if code == "" {
		return
	}
	s.mu.Lock()
	if operationOutcomePriority(code) >= operationOutcomePriority(s.code) {
		s.code = code
	}
	s.mu.Unlock()
}

func (s *operationSpan) finish(ctx context.Context) {
	s.mu.Lock()
	if s.finished {
		s.mu.Unlock()
		return
	}
	s.finished = true
	code := s.code
	if code == "" {
		if errors.Is(ctx.Err(), context.Canceled) {
			code = StoreErrorCanceled
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = StoreErrorTimeout
		}
	}
	s.mu.Unlock()
	s.supervisor.finish(s, code)
}

func operationOutcomePriority(code StoreErrorCode) int {
	switch code {
	case StoreErrorDurability, StoreErrorUnknownOutcome, StoreErrorCorrupt:
		return 4
	case StoreErrorTimeout, StoreErrorUnavailable, StoreErrorInternal:
		return 3
	case StoreErrorCanceled:
		return 2
	case StoreErrorNotFound, StoreErrorConflict, StoreErrorInvalid:
		return 1
	default:
		return 0
	}
}

type operationSpanContextKey struct{}
type operationDeniedContextKey struct{}

func markOperationOutcome(ctx context.Context, code StoreErrorCode) {
	span, _ := ctx.Value(operationSpanContextKey{}).(*operationSpan)
	if span != nil {
		span.mark(code)
	}
}
