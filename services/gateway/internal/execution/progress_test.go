package execution

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

type progressFixture struct {
	s      *Service
	e      Envelope
	digest string
	ctx    context.Context
	finish func()
}

func newProgressFixture(t *testing.T) progressFixture {
	t.Helper()
	started := make(chan context.Context, 1)
	end := make(chan struct{})
	var once sync.Once
	s, e, digest := setup(t, func(ctx context.Context, _ Envelope, _ map[string][]byte) (Output, error) {
		started <- ctx
		select {
		case <-end:
			return Output{Content: "complete final answer"}, nil
		case <-ctx.Done():
			return Output{}, ctx.Err()
		}
	})
	finish := func() { once.Do(func() { close(end) }) }
	t.Cleanup(finish)
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal(err)
	}
	return progressFixture{s, e, digest, <-started, finish}
}
func (f progressFixture) read(ctx context.Context, after uint64, wait int) (Progress, error) {
	return f.s.ReadProgress(ctx, f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, ProgressInput{f.digest, after, wait})
}
func (f progressFixture) mustRead(t *testing.T, after uint64, wait int) Progress {
	t.Helper()
	p, err := f.read(t.Context(), after, wait)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProgressSnapshotsWakeOnTextAndTerminalWithoutDelivering(t *testing.T) {
	f := newProgressFixture(t)
	p := f.mustRead(t, 0, 0)
	if p.Revision != 1 || !UUID(p.Epoch) || p.Content != "" || p.Digest != Digest(nil) || p.State != "running" {
		t.Fatalf("initial=%+v", p)
	}
	result := make(chan Progress, 1)
	go func() { value, _ := f.read(t.Context(), p.Revision, 10000); result <- value }()
	PublishProgress(f.ctx, "你好，")
	select {
	case p = <-result:
	case <-time.After(time.Second):
		t.Fatal("text did not wake progress")
	}
	if p.Content != "你好，" || p.Revision != 2 || p.Digest != Digest([]byte(p.Content)) {
		t.Fatalf("delta=%+v", p)
	}
	PublishProgress(f.ctx, "世界")
	p = f.mustRead(t, p.Revision, 0)
	if p.Content != "你好，世界" || p.Revision != 3 {
		t.Fatalf("cumulative=%+v", p)
	}
	if repeated := f.mustRead(t, p.Revision, 5); repeated != p {
		t.Fatalf("timeout changed same revision: %+v", repeated)
	}
	if high := f.mustRead(t, p.Revision+50, 10000); high != p {
		t.Fatal("future revision must return current immediately")
	}
	if state, err := f.s.Lookup(f.e.OwnerID, f.e.ClientID, f.e.RequestID); err != nil || state.Result != nil || state.State != "running" {
		t.Fatal("preview became result")
	}
	go func() { value, _ := f.read(t.Context(), p.Revision, 10000); result <- value }()
	f.finish()
	select {
	case p = <-result:
	case <-time.After(time.Second):
		t.Fatal("terminal did not wake progress")
	}
	if p.State != "completed" || p.Content != "你好，世界" || p.Revision != 4 {
		t.Fatalf("terminal=%+v", p)
	}
	status := completed(t, f.s, f.e)
	if err := f.s.Ack(f.e.OwnerID, f.e.ClientID, f.e.RequestID, 1, p.Digest, true); err != ErrConflict {
		t.Fatal("preview digest acknowledged final result")
	}
	if err := f.s.Ack(f.e.OwnerID, f.e.ClientID, f.e.RequestID, 1, status.Result.Digest, true); err != nil {
		t.Fatal(err)
	}
	acked := f.mustRead(t, p.Revision, 10000)
	if acked.State != "delivered" || acked.Epoch != p.Epoch || acked.Revision <= p.Revision || acked.Content != "" || acked.Digest != Digest(nil) {
		t.Fatalf("ack=%+v", acked)
	}
	if f.s.progressBytes != 0 {
		t.Fatal("ack retained preview memory")
	}
	if len(f.s.progress) != 0 {
		t.Fatal("ACK retained progress entry/channel")
	}
	if repeated := f.mustRead(t, 0, 0); repeated != acked || len(f.s.progress) != 0 {
		t.Fatal("terminal cursor regressed or recreated retained preview")
	}
}

func TestProgressUTF8BoundNoPlaintextLedgerAndNoOpWithoutRun(t *testing.T) {
	f := newProgressFixture(t)
	PublishProgress(context.Background(), "not part of any execution")
	PublishProgress(f.ctx, strings.Repeat("中", ProgressBytes))
	p := f.mustRead(t, 0, 0)
	if len(p.Content) > ProgressBytes || !utf8.ValidString(p.Content) || len(p.Content) != 8190 || !p.Truncated || p.Digest != Digest([]byte(p.Content)) {
		t.Fatal("unsafe truncation")
	}
	PublishProgress(f.ctx, "ignored after truncation")
	if next := f.mustRead(t, 0, 0); next != p {
		t.Fatal("truncated snapshot grew")
	}
	f.finish()
	f.s.Wait()
	raw, err := os.ReadFile(filepath.Join(f.s.root, "control.json"))
	if err != nil || strings.Contains(string(raw), "中") || strings.Contains(string(raw), "complete final answer") || strings.Contains(string(raw), p.Epoch) {
		t.Fatal("ephemeral preview entered ledger", err)
	}
}

func TestProgressIdentityDigestBoundsAndNoExecutionReplay(t *testing.T) {
	f := newProgressFixture(t)
	for _, input := range []ProgressInput{{f.digest, 0, -1}, {f.digest, 0, 10001}, {f.digest, 9007199254740992, 0}, {"bad", 0, 0}, {strings.Repeat("a", 64), 0, 0}} {
		if _, err := f.s.ReadProgress(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, input); err != ErrConflict {
			t.Fatalf("invalid input accepted: %+v %v", input, err)
		}
	}
	for _, ids := range [][4]string{{"other", f.e.ClientID, f.e.InstallationID, f.e.RequestID}, {f.e.OwnerID, "other", f.e.InstallationID, f.e.RequestID}, {f.e.OwnerID, f.e.ClientID, newUUID(), f.e.RequestID}, {f.e.OwnerID, f.e.ClientID, f.e.InstallationID, newUUID()}} {
		if _, err := f.s.ReadProgress(t.Context(), ids[0], ids[1], ids[2], ids[3], ProgressInput{f.digest, 0, 0}); err == nil {
			t.Fatal("foreign identity read preview")
		}
	}
	f.finish()
	f.s.Wait()
	for range 5 {
		f.mustRead(t, 0, 10000)
	}
	if len(f.s.control.Fences) != 1 {
		t.Fatal("progress changed execution ledger")
	}
}

func TestProgressCancellationRevocationCloseAndRestart(t *testing.T) {
	for _, action := range []string{"request_cancel", "execution_cancel", "revoke", "close"} {
		t.Run(action, func(t *testing.T) {
			f := newProgressFixture(t)
			PublishProgress(f.ctx, "private preview")
			p := f.mustRead(t, 0, 0)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				next, err := f.read(ctx, p.Revision, 10000)
				if err == nil && next.State != "unknown" {
					err = errors.New("wrong terminal")
				}
				result <- err
			}()
			switch action {
			case "request_cancel":
				cancel()
			case "execution_cancel":
				_ = f.s.Cancel(f.e.OwnerID, f.e.ClientID, f.e.RequestID)
			case "revoke":
				_ = f.s.RevokeWorkbenchClient(f.e.ClientID)
			case "close":
				f.s.Close()
			}
			select {
			case err := <-result:
				if action == "execution_cancel" && err != nil {
					t.Fatal(err)
				}
				if action != "execution_cancel" && err == nil {
					t.Fatal("closed progress succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("progress waiter leaked")
			}
			if action == "request_cancel" {
				if status, _ := f.s.Lookup(f.e.OwnerID, f.e.ClientID, f.e.RequestID); status.State != "running" {
					t.Fatal("read cancellation canceled execution")
				}
			}
		})
	}
	f := newProgressFixture(t)
	PublishProgress(f.ctx, "private before restart")
	p := f.mustRead(t, 0, 0)
	f.s.Close()
	var calls atomic.Int32
	restarted, err := New(f.s.root, func(context.Context, Envelope, map[string][]byte) (Output, error) { calls.Add(1); return Output{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	value, err := restarted.ReadProgress(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, ProgressInput{f.digest, p.Revision, 10000})
	if err != nil || value.Epoch == p.Epoch || value.Content != "" || value.State != "unknown" || calls.Load() != 0 {
		t.Fatalf("restart fabricated recovery: %+v %v", value, err)
	}
}

func TestProgressGlobalBudgetAndInvalidUTF8CannotAffectExecution(t *testing.T) {
	f := newProgressFixture(t)
	PublishProgress(f.ctx, string([]byte{0xff}))
	p := f.mustRead(t, 0, 0)
	if p.Content != "" || p.Revision != 1 {
		t.Fatal("invalid UTF8 published")
	}
	f.s.mu.Lock()
	f.s.progressBytes = progressTotalBytes
	f.s.mu.Unlock()
	PublishProgress(f.ctx, "private over global budget")
	p = f.mustRead(t, 0, 0)
	if p.Content != "" || !p.Truncated {
		t.Fatal("global preview bound exceeded")
	}
	f.finish()
	completed(t, f.s, f.e)
}

func TestProgressExpiryReleasesPreviewAndLatePublishIsIgnored(t *testing.T) {
	f := newProgressFixture(t)
	PublishProgress(f.ctx, "retained until delivery expiry")
	f.finish()
	completed(t, f.s, f.e)
	before := f.mustRead(t, 0, 0)
	PublishProgress(f.ctx, "late text must not appear")
	if got := f.mustRead(t, 0, 0); got != before {
		t.Fatal("finished worker changed preview")
	}
	f.s.mu.Lock()
	f.s.now = func() time.Time { return time.Now().Add(ResultRetention + time.Hour) }
	f.s.mu.Unlock()
	if err := f.s.Sweep(); err != nil {
		t.Fatal(err)
	}
	after := f.mustRead(t, before.Revision, 10000)
	if after.State != "delivery_expired" || after.Content != "" || after.Revision <= before.Revision || after.Epoch != before.Epoch {
		t.Fatalf("expiry=%+v", after)
	}
	if len(f.s.progress) != 0 {
		t.Fatal("expiry retained progress entry")
	}
}

func TestProgressTerminalCursorNeverEntersJSONOrRestarts(t *testing.T) {
	f := newProgressFixture(t)
	PublishProgress(f.ctx, "private partial")
	p := f.mustRead(t, 0, 0)
	if err := f.s.Cancel(f.e.OwnerID, f.e.ClientID, f.e.RequestID); err != nil {
		t.Fatal(err)
	}
	f.s.Wait()
	terminal := f.mustRead(t, p.Revision, 10000)
	if terminal.Revision <= p.Revision || terminal.Content != "" || len(f.s.progress) != 0 {
		t.Fatal("terminal cursor or reclamation incorrect")
	}
	for range 10 {
		if next := f.mustRead(t, 0, 0); next != terminal || len(f.s.progress) != 0 {
			t.Fatal("terminal snapshot changed after eviction")
		}
	}
	key := keyFor(f.e.OwnerID, f.e.ClientID, f.e.RequestID)
	stored := f.s.control.Fences[key]
	raw, err := json.Marshal(stored)
	if err != nil || strings.Contains(string(raw), "progress") || strings.Contains(string(raw), "private partial") {
		t.Fatal("ephemeral cursor serialized")
	}
	var decoded Fence
	if json.Unmarshal(raw, &decoded) != nil || decoded.progressRevision != 0 || decoded.progressState != "" || decoded.progressTruncated {
		t.Fatal("cursor survived reload")
	}
	f.s.Close()
	PublishProgress(f.ctx, "late closed content")
	if _, err = f.read(t.Context(), 0, 0); err == nil || len(f.s.progress) != 0 {
		t.Fatal("closed service recreated progress")
	}
}

func TestProgressRetainedEntryBudgetSuppressesPreviewOnly(t *testing.T) {
	s, e, digest := setup(t, func(context.Context, Envelope, map[string][]byte) (Output, error) {
		return Output{Content: "still executes"}, nil
	})
	for range progressRetainedEntries + 20 {
		request := newUUID()
		key := keyFor(e.OwnerID, e.ClientID, request)
		s.mu.Lock()
		s.control.Fences[key] = Fence{OwnerID: e.OwnerID, ClientID: e.ClientID, InstallationID: e.InstallationID, RequestID: request, InputDigest: digest, State: "running", Deadline: time.Now().Add(time.Hour)}
		s.mu.Unlock()
		s.publishProgress(t.Context(), key, "x")
		s.mu.Lock()
		f := s.control.Fences[key]
		f.State = "completed"
		expires := time.Now().Add(time.Hour)
		f.ExpiresAt = &expires
		s.control.Fences[key] = f
		s.pruneProgressLocked()
		s.mu.Unlock()
	}
	if len(s.progress) > progressRetainedEntries || s.progressBytes > progressTotalBytes {
		t.Fatal("preview entry budget exceeded")
	}
	// The synthetic retained owner reservations are independent of this new owner.
	e.OwnerID = "new-owner"
	if err := s.Bind(e.OwnerID, e.ClientID, e.InstallationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Submit(t.Context(), e, digest); err != nil {
		t.Fatal("preview saturation rejected execution", err)
	}
	completed(t, s, e)
}
