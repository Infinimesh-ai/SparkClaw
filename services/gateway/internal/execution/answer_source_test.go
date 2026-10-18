package execution

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
)

func answerSubscribe(t *testing.T, f progressFixture, cursor *AnswerCursor) *AnswerSubscription {
	t.Helper()
	sub, err := f.s.SubscribeAnswer(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func nextAnswer(t *testing.T, sub *AnswerSubscription) AnswerEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	event, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ParseAnswerEvent(raw); err != nil {
		t.Fatalf("source produced invalid event: %s %v", raw, err)
	}
	return event
}

func snapshotAnswer(t *testing.T, sub *AnswerSubscription) (string, AnswerCursor) {
	t.Helper()
	begin := nextAnswer(t, sub)
	if begin.Kind != "snapshot_begin" {
		t.Fatalf("want snapshot, got %+v", begin)
	}
	text := ""
	for {
		e := nextAnswer(t, sub)
		if e.SnapshotID != begin.SnapshotID || e.Revision != begin.Revision {
			t.Fatal("snapshot baseline changed")
		}
		if e.Kind == "snapshot_end" {
			if uint64(len(text)) != e.TotalBytes || Digest([]byte(text)) != e.ContentDigest {
				t.Fatal("incomplete snapshot")
			}
			return text, AnswerCursor{Epoch: e.Epoch, Generation: e.Generation, Revision: e.Revision, Offset: e.TotalBytes, ContentDigest: e.ContentDigest}
		}
		if e.Kind != "snapshot_chunk" || e.Offset != uint64(len(text)) {
			t.Fatal("out of order snapshot")
		}
		text += e.Text
	}
}

func TestAnswerSourceAtomicSnapshotReplayAndOriginalResult(t *testing.T) {
	f := newProgressFixture(t)
	text := "```go\n{" + strings.Repeat("中文😀", 1600)
	PublishAnswer(f.ctx, text)
	sub := answerSubscribe(t, f, nil)
	PublishAnswer(f.ctx, "\n后续") // Must follow the captured baseline, never disappear.
	got, cursor := snapshotAnswer(t, sub)
	if got != text || len(got) <= ProgressBytes {
		t.Fatal("source inherited legacy preview limit")
	}
	delta := nextAnswer(t, sub)
	if delta.Kind != "delta" || delta.FromOffset != uint64(len(text)) || delta.Text != "\n后续" || delta.ContentDigest != Digest([]byte(text+delta.Text)) {
		t.Fatalf("bad live delta %+v", delta)
	}
	reconnected := answerSubscribe(t, f, &cursor)
	if replay := nextAnswer(t, reconnected); replay != delta {
		t.Fatal("warm reconnect did not replay the same event")
	}
	f.finish()
	final := completed(t, f.s, f.e)
	if nextAnswer(t, sub).State != "completed" {
		t.Fatal("business completion not observed")
	}
	if _, err := sub.Next(t.Context()); err != io.EOF {
		t.Fatal("completed subscription remains open")
	}
	if err := f.s.Ack(f.e.OwnerID, f.e.ClientID, f.e.RequestID, 1, delta.ContentDigest, true); err != ErrConflict {
		t.Fatal("preview acknowledged authoritative result")
	}
	if err := f.s.Ack(f.e.OwnerID, f.e.ClientID, f.e.RequestID, 1, final.Result.Digest, true); err != nil {
		t.Fatal(err)
	}
	if e := nextAnswer(t, reconnected); e.Kind != "invalidate" || e.Reason != "delivered" {
		t.Fatal("ACK did not invalidate remaining observer", e)
	}
	if len(f.s.answers) != 0 {
		t.Fatal("ACK retained projection")
	}
}

func TestAnswerSourceStaleCursorSnapshotAndNoMemoryAfterRestart(t *testing.T) {
	f := newProgressFixture(t)
	sub := answerSubscribe(t, f, nil)
	_, old := snapshotAnswer(t, sub)
	PublishAnswer(f.ctx, "first")
	f.s.mu.Lock()
	f.s.answers[keyFor(f.e.OwnerID, f.e.ClientID, f.e.RequestID)].replay[0].at = time.Now().Add(-AnswerReplayRetention - time.Second)
	f.s.mu.Unlock()
	got, cursor := snapshotAnswer(t, answerSubscribe(t, f, &old))
	if got != "first" {
		t.Fatal("old cursor did not receive fresh snapshot")
	}
	for _, mutate := range []func(*AnswerCursor){func(c *AnswerCursor) { c.ContentDigest = strings.Repeat("f", 64) }, func(c *AnswerCursor) { c.Offset++ }} {
		bad := cursor
		mutate(&bad)
		if _, err := f.s.SubscribeAnswer(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest, &bad); err != ErrConflict {
			t.Fatal("forged cursor accepted", err)
		}
	}
	f.s.Close()
	restarted, err := New(f.s.Root(), func(context.Context, Envelope, map[string][]byte) (Output, error) {
		t.Error("subscription reran task")
		return Output{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, err = restarted.SubscribeAnswer(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest, &cursor); err != ErrAnswerUnavailable {
		t.Fatal("restart fabricated projection", err)
	}
	raw, _ := os.ReadFile(filepath.Join(f.s.Root(), "control.json"))
	if strings.Contains(string(raw), cursor.Generation) || strings.Contains(string(raw), "first") {
		t.Fatal("ephemeral answer entered ledger")
	}
}

func TestAnswerSourceInvalidatesCandidateOnResetRevokeAndCancel(t *testing.T) {
	for _, action := range []string{"reset", "revoke", "cancel", "observer_cancel"} {
		t.Run(action, func(t *testing.T) {
			f := newProgressFixture(t)
			PublishAnswer(f.ctx, strings.Repeat("文", 10000))
			sub := answerSubscribe(t, f, nil)
			begin := nextAnswer(t, sub)
			switch action {
			case "reset":
				ResetAnswer(f.ctx)
				PublishAnswer(f.ctx, "replacement")
			case "revoke":
				_ = f.s.RevokeWorkbenchClient(f.e.ClientID)
			case "cancel":
				_ = f.s.Cancel(f.e.OwnerID, f.e.ClientID, f.e.RequestID)
			case "observer_cancel":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := sub.Next(ctx); err != context.Canceled {
					t.Fatal(err)
				}
				if status, _ := f.s.Lookup(f.e.OwnerID, f.e.ClientID, f.e.RequestID); status.State != "running" {
					t.Fatal("observation cancellation canceled task")
				}
				return
			}
			e := nextAnswer(t, sub)
			if e.Kind != "invalidate" || e.Generation != begin.Generation || sub.snapshot != "" {
				t.Fatal("candidate survived invalidation", e)
			}
			if action == "reset" {
				got, c := snapshotAnswer(t, answerSubscribe(t, f, nil))
				if got != "replacement" || c.Generation == begin.Generation {
					t.Fatal("generation not fenced")
				}
			}
		})
	}
}

func TestAnswerSourceMemoryBoundAndLagFailWithoutExecutionFailure(t *testing.T) {
	f := newProgressFixture(t)
	sub := answerSubscribe(t, f, nil)
	snapshotAnswer(t, sub)
	PublishAnswer(f.ctx, strings.Repeat("x", ResultBytes))
	f.s.mu.Lock()
	e := f.s.answers[keyFor(f.e.OwnerID, f.e.ClientID, f.e.RequestID)]
	size := e.text.Len() + e.replayBytes
	f.s.mu.Unlock()
	if size > ResultBytes {
		t.Fatal("projection plus replay exceeded 8 MiB")
	}
	if _, err := sub.Next(t.Context()); err != ErrAnswerGap {
		t.Fatal("lag skipped text", err)
	}
	PublishAnswer(f.ctx, "overflow")
	if _, err := f.s.SubscribeAnswer(t.Context(), f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest, nil); err != ErrAnswerUnavailable {
		t.Fatal("overflow remained previewable", err)
	}
	f.finish()
	completed(t, f.s, f.e)
}

func TestAnswerProtocolFixturesAndAmbiguousJSON(t *testing.T) {
	raw, err := os.ReadFile("../../../../packages/protocol/fixtures/execution-stream.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Positive []json.RawMessage `json:"positive"`
		Negative []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"`
		} `json:"negative"`
	}
	if json.Unmarshal(raw, &fixtures) != nil {
		t.Fatal("bad fixtures")
	}
	for _, raw := range fixtures.Positive {
		if _, err := ParseAnswerEvent(raw); err != nil {
			t.Fatal(string(raw), err)
		}
	}
	for _, fixture := range fixtures.Negative {
		if _, err := ParseAnswerEvent(fixture.Value); err == nil {
			t.Fatal("accepted", fixture.Name)
		}
	}
	valid := string(fixtures.Positive[0])
	for _, raw := range []string{strings.Replace(valid, `"text": "你好"`, `"text": "\ud800"`, 1), strings.Replace(valid, `"kind": "delta"`, `"kind": "delta", "kind": "delta"`, 1), strings.Replace(valid, "你好", string([]byte{0xff}), 1)} {
		if _, err := ParseAnswerEvent([]byte(raw)); err == nil {
			t.Fatal("ambiguous JSON accepted")
		}
	}
	frame := appstream.Frame{Type: appstream.Type, SchemaVersion: 1, SubscriptionID: newUUID(), StreamSeq: 1, Body: fixtures.Positive[0]}
	encoded, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = appstream.Decode(encoded); err != nil {
		t.Fatal(err)
	}
}

func TestAnswerSubscriptionRejectsForeignOriginalBinding(t *testing.T) {
	f := newProgressFixture(t)
	for _, ids := range [][5]string{{"other", f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest}, {f.e.OwnerID, f.e.ClientID, newUUID(), f.e.RequestID, f.digest}, {f.e.OwnerID, f.e.ClientID, f.e.InstallationID, newUUID(), f.digest}, {f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, strings.Repeat("f", 64)}} {
		if _, err := f.s.SubscribeAnswer(t.Context(), ids[0], ids[1], ids[2], ids[3], ids[4], nil); err == nil {
			t.Fatal("foreign identity subscribed")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.s.SubscribeAnswer(ctx, f.e.OwnerID, f.e.ClientID, f.e.InstallationID, f.e.RequestID, f.digest, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestAnswerSnapshotEncodedBudgetAndUnicodeBoundaries(t *testing.T) {
	for _, part := range []string{"文😀", "\x01\"\\"} {
		f := newProgressFixture(t)
		text := strings.Repeat(part, 40000)
		PublishAnswer(f.ctx, text)
		sub := answerSubscribe(t, f, nil)
		var got strings.Builder
		chunks := 0
		for {
			e := nextAnswer(t, sub)
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			outer, err := json.Marshal(appstream.Frame{Type: appstream.Type, SchemaVersion: 1, SubscriptionID: newUUID(), StreamSeq: 1, Body: raw})
			if err != nil || len(outer) > appstream.MaxBytes {
				t.Fatal("encoded snapshot frame too large", len(outer), err)
			}
			if e.Kind == "snapshot_chunk" {
				got.WriteString(e.Text)
				chunks++
			}
			if e.Kind == "snapshot_end" {
				break
			}
		}
		if got.String() != text || chunks < 2 {
			t.Fatal("snapshot split damaged text")
		}
	}
}

func TestAnswerStreamOpenStrictFields(t *testing.T) {
	valid := `{"schema_version":1,"input_digest":"` + strings.Repeat("a", 64) + `","latest_open_at":"2026-10-18T12:00:00Z"}`
	if _, err := ParseAnswerStreamInput([]byte(valid)); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1), strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1), strings.Replace(valid, `"latest_open_at":"2026-10-18T12:00:00Z"`, `"latest_open_at":null`, 1), strings.TrimSuffix(valid, "}") + `,"cursor":{"epoch":"11111111-1111-4111-8111-111111111111","generation":"22222222-2222-4222-8222-222222222222","revision":1,"content_digest":"` + Digest(nil) + `"}}`} {
		if _, err := ParseAnswerStreamInput([]byte(raw)); err == nil {
			t.Fatal("invalid open admitted", raw)
		}
	}
}
