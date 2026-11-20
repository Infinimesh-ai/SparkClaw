package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func TestLinkReceiptsGiveThreeAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receipts.json")
	r, err := openLinkReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	fenced := linkReceiptOptions{Fence: true}
	runs := 0
	send := func() (any, error) { runs++; return map[string]int{"sent": runs}, nil }
	first, err := r.Do("k1", "d1", fenced, send)
	if err != nil || runs != 1 {
		t.Fatal(err)
	}
	// Completed: the original result, without running again.
	again, err := r.Do("k1", "d1", fenced, send)
	if err != nil || runs != 1 || string(again) != string(first) {
		t.Fatalf("replay %s %v", again, err)
	}
	if replay, err := r.Do("k1", "d1", linkReceiptOptions{Fence: true, ReplayOnly: true}, send); err != nil || string(replay) != string(first) {
		t.Fatalf("replay_only %s %v", replay, err)
	}
	// Input conflict.
	if _, err := r.Do("k1", "other", fenced, send); contract.AsError(err).Code != contract.CodeConflict {
		t.Fatal(err)
	}
	// Never ran: replay_only answers not_found and runs nothing.
	if _, err := r.Do("k2", "d2", linkReceiptOptions{Fence: true, ReplayOnly: true}, send); contract.AsError(err).Code != contract.CodeNotFound || !contract.AsError(err).NotSent || runs != 1 {
		t.Fatal(err)
	}
	// An attempt cut off midway leaves its fence: outcome_unknown, and it
	// is never run twice, also after a restart.
	if _, err := r.Do("k3", "d3", fenced, func() (any, error) { return nil, context.Canceled }); err == nil {
		t.Fatal("cut-off attempt succeeded")
	}
	reopened, err := openLinkReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Do("k3", "d3", fenced, send); contract.AsError(err).Code != contract.CodeOutcomeUnknown || runs != 1 {
		t.Fatalf("after restart: %v (runs %d)", err, runs)
	}
	// A refusal before any effect frees the key.
	if _, err := reopened.Do("k4", "d4", fenced, func() (any, error) {
		return nil, &contract.Error{Code: contract.CodeBackendUnavailable, NotSent: true}
	}); err == nil {
		t.Fatal("refusal succeeded")
	}
	if _, err := reopened.Do("k4", "d4", fenced, send); err != nil || runs != 2 {
		t.Fatalf("freed key: %v", err)
	}
	// A definitive answer is replayed.
	if _, err := reopened.Do("k5", "d5", fenced, func() (any, error) { return nil, &contract.Error{Code: contract.CodeInvalidRequest, Message: "bad"} }); contract.AsError(err).Code != contract.CodeInvalidRequest {
		t.Fatal(err)
	}
	if _, err := reopened.Do("k5", "d5", fenced, send); contract.AsError(err).Code != contract.CodeInvalidRequest || runs != 2 {
		t.Fatal(err)
	}
	// Without a fence an uncertain failure simply runs again.
	if _, err := reopened.Do("k6", "d6", linkReceiptOptions{}, func() (any, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("failure succeeded")
	}
	if _, err := reopened.Do("k6", "d6", linkReceiptOptions{}, send); err != nil || runs != 3 {
		t.Fatal(err)
	}
}

func TestLinkDomainObjectsBelongToTheirDevice(t *testing.T) {
	b := newLinkTestBackend(t)
	conditions := contract.Conditions{Screen: contract.ScreenLarge, Attachments: true}
	desk, _, drec, _ := newLinkTestDevice(t, b, contract.ClassDesktop, conditions)
	phone, _, prec, _ := newLinkTestDevice(t, b, contract.ClassPhone, contract.Conditions{Screen: contract.ScreenSmall, Attachments: true})
	writeLinkDirectory(t, b.stateDir, drec, prec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desk.Start(ctx)
	phone.Start(ctx)
	defer desk.Close()
	defer phone.Close()
	if desk.WaitReady(ctx) != nil || phone.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	content := []byte("attachment bytes for a mail")
	sum := sha256.Sum256(content)
	var state contract.ObjectUploadState
	if err := desk.Request(ctx, contract.OpObjectUploadBegin, contract.ObjectUploadBeginParams{UploadKey: "mail-1", Name: "a.txt", Size: int64(len(content)), ContentType: "text/plain", SHA256: hex.EncodeToString(sum[:]), Purpose: "mail_send_attachment"}, &state); err != nil {
		t.Fatal(err)
	}
	if err := desk.Request(ctx, contract.OpObjectUploadChunk, contract.ObjectUploadChunkParams{UploadID: state.UploadID, Data: content}, &state); err != nil {
		t.Fatal(err)
	}
	if err := desk.Request(ctx, contract.OpObjectUploadCommit, contract.ObjectUploadCommitParams{UploadID: state.UploadID}, &state); err != nil {
		t.Fatal(err)
	}
	if !linkDomainObjectIDPattern.MatchString(state.ObjectID) {
		t.Fatalf("domain object id %q", state.ObjectID)
	}
	var read contract.ObjectReadResult
	if err := desk.Request(ctx, contract.OpObjectRead, contract.ObjectReadParams{ObjectID: state.ObjectID}, &read); err != nil || string(read.Data) != string(content) || !read.EOF {
		t.Fatalf("read %+v %v", read, err)
	}
	if err := phone.Request(ctx, contract.OpObjectRead, contract.ObjectReadParams{ObjectID: state.ObjectID}, &read); contract.AsError(err).Code != contract.CodeNotFound {
		t.Fatalf("another device read the object: %v", err)
	}
	// A domain adapter reads it by reference with its purpose.
	access := linkObjectAccess{objects: b.server.link.domainObj, deviceID: drec.DeviceID, ownerID: app.DefaultOwnerID}
	ref, err := access.describeObject(state.ObjectID, 1)
	if err != nil || ref.Purpose != "mail_send_attachment" || ref.Size != int64(len(content)) || ref.Validate() != nil {
		t.Fatalf("reference %+v %v", ref, err)
	}
	if raw, err := access.readObject(ref, "mail_send_attachment", 1<<20); err != nil || string(raw) != string(content) {
		t.Fatalf("adapter read %v", err)
	}
	if _, err := access.readObject(ref, "speech_recording", 1<<20); err == nil {
		t.Fatal("read under another purpose")
	}
	// A purpose and a conversation exclude each other.
	if err := desk.Request(ctx, contract.OpObjectUploadBegin, contract.ObjectUploadBeginParams{UploadKey: "mail-2", Name: "a.txt", Size: 3, SHA256: hex.EncodeToString(sum[:]), Purpose: "mail_send_attachment", SessionID: "s"}, nil); contract.AsError(err).Code != contract.CodeInvalidRequest {
		t.Fatal(err)
	}
}

func TestLinkBridgeRefusesTheReplacedGroupsAndAnnouncesDomainChanges(t *testing.T) {
	b := newLinkTestBackend(t)
	desk, _, rec, events := newLinkTestDevice(t, b, contract.ClassDesktop, contract.Conditions{Screen: contract.ScreenLarge, Attachments: true, BrowserSurface: true})
	writeLinkDirectory(t, b.stateDir, rec)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	desk.Start(ctx)
	defer desk.Close()
	if desk.WaitReady(ctx) != nil {
		t.Fatal("not ready")
	}
	for _, method := range []string{"events.pull", "events.ack", "events.snapshot", "object.read", "transfer.open", "operations.receipt"} {
		params := contract.DomainCallParams{Domain: method[:len(method)-len(filepath.Ext(method))], Method: method, IdempotencyKey: newLinkUUID()}
		if err := desk.Request(ctx, contract.OpDomainCall, params, nil); contract.AsError(err).Code != contract.CodeUnsupportedCapability {
			t.Fatalf("%s: %v", method, err)
		}
	}
	var described contract.DeviceDescribeResult
	if err := desk.Request(ctx, contract.OpDeviceDescribe, nil, &described); err != nil {
		t.Fatal(err)
	}
	for _, d := range described.Domains {
		if d == "events" || d == "object" || d == "transfer" || d == "operations" {
			t.Fatalf("%s is still a domain", d)
		}
	}
	// A settings change reaches the device as a domain event.
	if _, err := b.server.store.UpdateOwnerProfile(ctx, app.OwnerProfile{ID: app.DefaultOwnerID, DisplayName: "Owner"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(10 * time.Second)
	for {
		select {
		case f := <-events:
			if f.Topic != contract.TopicDomain {
				continue
			}
			var ev contract.DomainEvent
			if json.Unmarshal(f.Data, &ev) != nil || len(ev.Domains) == 0 {
				t.Fatalf("domain event %s", f.Data)
			}
			for _, d := range ev.Domains {
				if d == "settings" {
					return
				}
			}
		case <-deadline:
			t.Fatal("no domain event for the settings change")
		}
	}
}
