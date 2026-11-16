package latencytrace

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type healthBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *healthBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}
func (b *healthBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }
func (b *healthBuffer) Reset()         { b.mu.Lock(); defer b.mu.Unlock(); b.buffer.Reset() }

func captureHealth(t *testing.T) *healthBuffer {
	t.Helper()
	var output healthBuffer
	before := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(before) })
	return &output
}

func requireHealthReason(t *testing.T, output *healthBuffer, reason string) map[string]any {
	t.Helper()
	until := time.Now().Add(time.Second)
	for {
		for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			var value map[string]any
			if json.Unmarshal([]byte(line), &value) == nil && value["event"] == "latency_trace_health" && value["reason"] == reason {
				return value
			}
		}
		if time.Now().After(until) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no %s health transition: %s", reason, output.String())
	return nil
}

func TestTraceInitializationHealthDoesNotSilentlyDisableEvidence(t *testing.T) {
	output := captureHealth(t)
	if r := testRecorder(""); r != nil {
		t.Fatal("disabled tracing created a sink")
	}
	disabled := requireHealthReason(t, output, "not_enabled")
	if disabled["state"] != "disabled" || disabled["evidence"] != "incomplete" {
		t.Fatal(disabled)
	}
	output.Reset()
	if r := testRecorder(filepath.Join(t.TempDir(), "private-secret-missing")); r != nil {
		t.Fatal("invalid directory created a sink")
	}
	degraded := requireHealthReason(t, output, "initialization_failed")
	if degraded["state"] != "degraded" || strings.Contains(output.String(), "private-secret") {
		t.Fatal(output.String())
	}
}

func TestTraceWriteFailureIsObservableWithoutRawErrorOrPath(t *testing.T) {
	output := captureHealth(t)
	r := testRecorder(privateTestDirectory(t))
	if r == nil {
		t.Fatal("recorder unavailable")
	}
	_ = r.file.Close()
	for range 10 {
		r.emit(event{Stage: "rpc.call"})
	}
	close(r.queue)
	<-r.done
	failed := requireHealthReason(t, output, "write_failed")
	if failed["state"] != "degraded" || strings.Contains(output.String(), r.file.Name()) {
		t.Fatal(output.String())
	}
	if strings.Count(output.String(), `"reason":"write_failed"`) != 1 {
		t.Fatalf("failure logged per event: %s", output.String())
	}
}

func TestTraceQueueLossIsObservableAndProducerRemainsNonblocking(t *testing.T) {
	output := captureHealth(t)
	file, err := os.CreateTemp(privateTestDirectory(t), "trace-*.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{file: file, queue: make(chan event, 1), done: make(chan struct{})}
	for range 10 {
		r.emit(event{Stage: "rpc.call"})
	}
	go r.write()
	close(r.queue)
	<-r.done
	loss := requireHealthReason(t, output, "queue_full")
	if loss["state"] != "degraded" || loss["evidence"] != "incomplete" {
		t.Fatal(loss)
	}
}

func TestTraceWithFieldsRejectsMetadataOnProductionPath(t *testing.T) {
	if os.Getenv("SPARKCLAW_TEST_TRACE_CHILD") == "1" {
		ctx := WithFields(context.Background(), Fields{Role: "gateway", RPCID: "11111111-1111-4111-8111-111111111111", Operation: "execution.lookup"})
		ctx = WithFields(ctx, Fields{RPCID: "private@example.test", Operation: "https://private.example/payload"})
		Record(ctx, "rpc.call", time.Time{}, nil, 0)
		r := processSession().recorder
		close(r.queue)
		<-r.done
		if got := Status(); got.State != "degraded" || got.Reason != "invalid_metadata" || got.MetadataRejected != 1 || got.Written != 1 || got.Lost != 0 {
			t.Fatalf("production metadata rejection was hidden: %+v", got)
		}
		raw, err := os.ReadFile(r.file.Name())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "private") || !strings.Contains(string(raw), "11111111-1111-4111-8111-111111111111") || !strings.Contains(string(raw), "execution.lookup") {
			t.Fatalf("inheritance/privacy changed: %s", raw)
		}
		return
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestTraceWithFieldsRejectsMetadataOnProductionPath$", "-test.count=1")
	command.Env = append(os.Environ(), "SPARKCLAW_TEST_TRACE_CHILD=1", Environment+"="+privateTestDirectory(t))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("production trace child: %v\n%s", err, output)
	}
}

func TestTraceRotatesAndContinuesWithinBudget(t *testing.T) {
	directory := privateTestDirectory(t)
	const budget = 1024
	r := startSessionWithLimit(directory, budget).recorder
	value := event{SchemaVersion: 1, Time: time.Now().UTC(), Stage: "rpc.call", Status: "ok", Fields: Fields{Role: "gateway",
		RPCID: "11111111-1111-4111-8111-111111111111", RequestID: "22222222-2222-4222-8222-222222222222", Operation: "execution.lookup"}}
	for i := range 20 {
		r.attempted.Add(1)
		value.Count = uint64(i + 1)
		r.queue <- value
	}
	close(r.queue)
	<-r.done
	if got := r.health(); got.Written != 20 || got.Lost != 0 || got.State != "enabled" {
		t.Fatal(got)
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.ndjson"))
	if err != nil || len(files) != traceSegments {
		t.Fatalf("expected three retained segments: %v %v", files, err)
	}
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil || info.Size() > budget || info.Mode().Perm() != 0600 {
			t.Fatalf("trace budget or permissions violated: info=%v err=%v", info, err)
		}
	}
	raw, err := os.ReadFile(r.file.Name())
	if err != nil || !strings.Contains(string(raw), `"count":20`) {
		t.Fatalf("newest event missing after rotation: %v", err)
	}
}

type blockedHealthHandler struct{ release <-chan struct{} }

func (h blockedHealthHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (h blockedHealthHandler) Handle(context.Context, slog.Record) error { <-h.release; return nil }
func (h blockedHealthHandler) WithAttrs([]slog.Attr) slog.Handler        { return h }
func (h blockedHealthHandler) WithGroup(string) slog.Handler             { return h }

func TestTraceHealthObserverCannotBlockProducer(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	before := slog.Default()
	slog.SetDefault(slog.New(blockedHealthHandler{release: release}))
	t.Cleanup(func() { slog.SetDefault(before) })
	directory := privateTestDirectory(t)
	created := make(chan *recorder, 1)
	go func() { created <- testRecorder(directory) }()
	var r *recorder
	select {
	case r = <-created:
	case <-time.After(time.Second):
		t.Fatal("health observer blocked sink initialization")
	}
	if r == nil {
		t.Fatal("recorder unavailable")
	}
	emitted := make(chan struct{})
	go func() {
		for range 10000 {
			r.emit(event{Stage: "rpc.call"})
		}
		close(emitted)
	}()
	select {
	case <-emitted:
	case <-time.After(time.Second):
		t.Fatal("health observer blocked producer")
	}
	if r.dropped.Load() == 0 {
		t.Fatal("fixture did not exercise pressure")
	}
	unblock()
	close(r.queue)
	<-r.done
}
