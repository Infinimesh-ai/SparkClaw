// Package latencytrace provides optional private, best-effort timing evidence.
// It never records payloads, endpoints, credentials or error messages. Set
// SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR to an existing absolute 0700 directory
// before starting the process. Invalid configuration and evidence loss are reported
// separately from business outcomes through bounded content-free health events.
package latencytrace

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const Environment = "SPARKCLAW_PRIVATE_LATENCY_TRACE_DIR"

type Fields struct {
	Role      string `json:"role,omitempty"`
	RPCID     string `json:"rpc_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Operation string `json:"operation,omitempty"`
}

type event struct {
	SchemaVersion int       `json:"schema_version"`
	Time          time.Time `json:"time"`
	Stage         string    `json:"stage"`
	Fields
	DurationMS float64 `json:"duration_ms"`
	Status     string  `json:"status"`
	Count      uint64  `json:"count,omitempty"`
}

// Health describes this optional sink, never whether a business operation ran.
// Even an enabled sink covers only instrumented boundaries, not whole execution.
type Health struct {
	SchemaVersion    int    `json:"schema_version"`
	Component        string `json:"component"`
	Enabled          bool   `json:"enabled"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	Coverage         string `json:"coverage"`
	Evidence         string `json:"evidence"`
	Attempted        uint64 `json:"attempted"`
	Written          uint64 `json:"written"`
	Dropped          uint64 `json:"dropped"`
	Lost             uint64 `json:"lost"`
	MetadataRejected uint64 `json:"metadata_rejected"`
	FirstLossReason  string `json:"first_loss_reason,omitempty"`
	LastWrittenAt    string `json:"last_written_at,omitempty"`
}

const traceByteLimit = 64 << 20
const (
	lossQueueFull uint32 = 1 << iota
	lossMetadata
	lossLimit
	lossWrite
)

type recorder struct {
	file             *os.File
	queue            chan event
	done             chan struct{}
	wake             chan struct{}
	dropped          atomic.Uint64
	attempted        atomic.Uint64
	written          atomic.Uint64
	lost             atomic.Uint64
	metadataRejected atomic.Uint64
	lastWritten      atomic.Int64
	losses           atomic.Uint32
	firstLoss        atomic.Uint32
	stopped          atomic.Bool
	observer         *slog.Logger
}

type traceSession struct {
	recorder *recorder
	initial  Health
}

var processSession = sync.OnceValue(func() traceSession { return startSession(os.Getenv(Environment)) })
var uuid = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var operationName = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*){1,5}$`)

func baseHealth() Health {
	return Health{SchemaVersion: 1, Component: "go_latencytrace", Coverage: "instrumented_boundaries_only", Evidence: "incomplete"}
}

func startSession(directory string) traceSession {
	h := baseHealth()
	h.State, h.Reason = "disabled", "not_enabled"
	if directory != "" {
		h.State, h.Reason = "degraded", "initialization_failed"
		h.FirstLossReason = h.Reason
	}
	observer := slog.Default()
	failed := func() traceSession { go reportHealth(observer, h); return traceSession{initial: h} }
	if !filepath.IsAbs(directory) {
		return failed()
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return failed()
	}
	// CreateTemp uses O_EXCL and 0600: existing names/symlinks are never opened.
	file, err := os.CreateTemp(directory, "go-"+strconv.Itoa(os.Getpid())+"-"+time.Now().UTC().Format("20060102T150405.000000000")+"-*.ndjson")
	if err != nil {
		return failed()
	}
	r := &recorder{file: file, queue: make(chan event, 1024), done: make(chan struct{}), wake: make(chan struct{}, 1), observer: observer}
	go r.write()
	return traceSession{recorder: r}
}

func lossReason(value uint32) string {
	switch {
	case value&lossWrite != 0:
		return "write_failed"
	case value&lossLimit != 0:
		return "size_limit"
	case value&lossMetadata != 0:
		return "invalid_metadata"
	case value&lossQueueFull != 0:
		return "queue_full"
	default:
		return ""
	}
}

func (r *recorder) health() Health {
	h, _ := r.snapshot()
	return h
}

func (r *recorder) snapshot() (Health, uint32) {
	h := baseHealth()
	h.Enabled, h.State, h.Reason = true, "enabled", "recording"
	losses := r.losses.Load()
	if losses != 0 {
		h.State, h.Reason = "degraded", lossReason(losses)
	}
	h.Attempted, h.Written, h.Dropped, h.Lost = r.attempted.Load(), r.written.Load(), r.dropped.Load(), r.lost.Load()
	h.MetadataRejected = r.metadataRejected.Load()
	h.FirstLossReason = lossReason(r.firstLoss.Load())
	if last := r.lastWritten.Load(); last != 0 {
		h.LastWrittenAt = time.Unix(0, last).UTC().Format(time.RFC3339Nano)
	}
	return h, losses
}

// Status is local diagnostic state. It performs no business reads or mutations.
func Status() Health {
	session := processSession()
	if session.recorder == nil {
		return session.initial
	}
	return session.recorder.health()
}

func reportHealth(observer *slog.Logger, h Health) {
	level := slog.LevelInfo
	if h.State == "degraded" {
		level = slog.LevelWarn
	}
	observer.Log(context.Background(), level, "latency trace health", "event", "latency_trace_health",
		"schema_version", h.SchemaVersion, "component", h.Component, "enabled", h.Enabled,
		"state", h.State, "reason", h.Reason, "coverage", h.Coverage, "evidence", h.Evidence,
		"attempted", h.Attempted, "written", h.Written, "dropped", h.Dropped, "lost", h.Lost, "metadata_rejected", h.MetadataRejected,
		"first_loss_reason", h.FirstLossReason, "last_written_at", h.LastWrittenAt)
}

func (r *recorder) noteLoss(reason uint32) {
	r.firstLoss.CompareAndSwap(0, reason)
	r.losses.Or(reason)
	// The producer never waits for the diagnostic sink or its health observer.
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *recorder) write() {
	defer close(r.done)
	defer r.file.Close()
	observer := r.observer
	if observer == nil {
		observer = slog.Default()
	}
	initial, reportedLosses := r.snapshot()
	reportHealth(observer, initial)
	var bytes int64
	var reportedDrops uint64
	observe := func() {
		if health, losses := r.snapshot(); losses != reportedLosses {
			reportHealth(observer, health)
			reportedLosses = losses
		}
	}
	defer observe()
	write := func(raw []byte) bool {
		n, err := r.file.Write(raw)
		bytes += int64(n)
		if err != nil || n != len(raw) {
			r.stopped.Store(true)
			r.noteLoss(lossWrite)
			return false
		}
		return true
	}
	for {
		observe()
		var value event
		select {
		case <-r.wake:
			continue
		case next, open := <-r.queue:
			if !open {
				return
			}
			value = next
		}
		if r.stopped.Load() {
			r.lost.Add(1)
			continue
		}
		if dropped := r.dropped.Load(); dropped > reportedDrops {
			marker, _ := json.Marshal(event{SchemaVersion: 1, Time: time.Now().UTC(), Stage: "trace.dropped", Status: "incomplete", Count: dropped - reportedDrops})
			if !write(append(marker, '\n')) {
				r.lost.Add(1)
				continue
			}
			reportedDrops = dropped
		}
		raw, err := json.Marshal(value)
		if err != nil {
			r.lost.Add(1)
			r.noteLoss(lossMetadata)
			continue
		}
		raw = append(raw, '\n')
		// Reserve enough bytes for the terminal loss marker, retaining the
		// existing 64 MiB cap even when the last event would cross it.
		if bytes+int64(len(raw))+512 > traceByteLimit {
			r.stopped.Store(true)
			r.lost.Add(1)
			r.noteLoss(lossLimit)
			marker, _ := json.Marshal(event{SchemaVersion: 1, Time: time.Now().UTC(), Stage: "trace.limit", Status: "incomplete"})
			write(append(marker, '\n'))
			continue
		}
		if !write(raw) {
			r.lost.Add(1)
			continue
		}
		r.written.Add(1)
		r.lastWritten.Store(time.Now().UnixNano())
	}
}

func (r *recorder) emit(value event) {
	r.attempted.Add(1)
	if r.stopped.Load() {
		r.lost.Add(1)
		return
	}
	if len(value.Stage) > 96 || !operationName.MatchString(value.Stage) {
		r.lost.Add(1)
		r.metadataRejected.Add(1)
		r.noteLoss(lossMetadata)
		return
	}
	if cleaned := clean(value.Fields); cleaned != value.Fields {
		value.Fields = cleaned
		r.metadataRejected.Add(1)
		r.noteLoss(lossMetadata)
	}
	select {
	case r.queue <- value:
	default:
		r.dropped.Add(1)
		r.lost.Add(1)
		r.noteLoss(lossQueueFull)
	}
}

func Enabled() bool { return Status().Enabled }

type contextKey struct{}

// WithFields accepts only correlation metadata already validated by its caller.
// UUID and operation-name filtering provides a second boundary against logging
// arbitrary input. An empty value inherits the corresponding parent field.
func WithFields(ctx context.Context, fields Fields) context.Context {
	if !Enabled() {
		return ctx
	}
	prior, _ := ctx.Value(contextKey{}).(Fields)
	if cleaned := clean(fields); cleaned != fields {
		fields = cleaned
		r := processSession().recorder
		r.metadataRejected.Add(1)
		r.noteLoss(lossMetadata)
	}
	if fields.Role != "" {
		prior.Role = fields.Role
	}
	if fields.RPCID != "" {
		prior.RPCID = fields.RPCID
	}
	if fields.RequestID != "" {
		prior.RequestID = fields.RequestID
	}
	if fields.Operation != "" {
		prior.Operation = fields.Operation
	}
	return context.WithValue(ctx, contextKey{}, prior)
}

func clean(fields Fields) Fields {
	if fields.Role != "initiator" && fields.Role != "responder" && fields.Role != "gateway" && fields.Role != "relay" {
		fields.Role = ""
	}
	if !uuid.MatchString(fields.RPCID) {
		fields.RPCID = ""
	}
	if !uuid.MatchString(fields.RequestID) {
		fields.RequestID = ""
	}
	if len(fields.Operation) > 96 || !operationName.MatchString(fields.Operation) {
		fields.Operation = ""
	}
	return fields
}

// Clock returns a monotonic timestamp only when tracing is enabled.
func Clock() time.Time {
	if !Enabled() {
		return time.Time{}
	}
	return time.Now()
}

func status(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "error"
	}
}

// Record uses time.Since's monotonic clock for a span. For instantaneous events
// pass a zero start; At can retain the original arrival time after correlation.
func Record(ctx context.Context, stage string, start time.Time, err error, count uint64) {
	r := processSession().recorder
	if r == nil {
		return
	}
	now := time.Now()
	value := event{SchemaVersion: 1, Time: now.UTC(), Stage: stage, Status: status(err), Count: count}
	value.Fields, _ = ctx.Value(contextKey{}).(Fields)
	if !start.IsZero() {
		value.DurationMS = float64(now.Sub(start)) / float64(time.Millisecond)
	}
	r.emit(value)
}

func At(ctx context.Context, stage string, when time.Time) {
	r := processSession().recorder
	if r == nil || when.IsZero() {
		return
	}
	value := event{SchemaVersion: 1, Time: when.UTC(), Stage: stage, Status: "ok"}
	value.Fields, _ = ctx.Value(contextKey{}).(Fields)
	r.emit(value)
}
