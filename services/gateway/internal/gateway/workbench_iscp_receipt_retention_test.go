package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func receiptRetentionFixture(t *testing.T) *iscpDomainReceipts {
	t.Helper()
	j, err := newISCPDomainReceipts(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { j.mu.Lock(); defer j.mu.Unlock(); j.closeScanLocked() })
	return j
}

func completedRetentionReceipt(now time.Time) iscpDomainReceipt {
	return iscpDomainReceipt{Version: 1, Digest: execution.Digest([]byte("original input")), Operation: iscpworkbench.OperationSettingsOwnerPatch, Complete: true, Status: 200, Body: json.RawMessage(`{"private":"retention canary"}`), CompletedAt: now}
}

func TestISCPReceiptRetentionRemovesOnlyExpiredResults(t *testing.T) {
	j := receiptRetentionFixture(t)
	now := time.Now().UTC()
	old := now.Add(-domainReceiptRetention)
	ids := map[string]string{}
	for _, name := range []string{"expired", "fresh", "future", "unknown", "legacy", "state", "speech"} {
		ids[name] = domainTestRequest("id", nil).ID
		receipt := completedRetentionReceipt(old)
		switch name {
		case "fresh":
			receipt.CompletedAt = old.Add(time.Second)
		case "future":
			receipt.CompletedAt = now.Add(time.Hour)
		case "unknown":
			receipt.Complete = false
			receipt.Body = nil
		case "state":
			receipt.Operation = iscpworkbench.OperationEventsPull
			receipt.CompletedAt = time.Time{}
		case "speech":
			receipt.Operation = iscpworkbench.OperationSpeechSessionEvents
			receipt.CompletedAt = time.Time{}
		}
		if name == "legacy" {
			// Reproduce the previous ciphertext format exactly, including AAD.
			receipt.CompletedAt = time.Time{}
			raw, _ := json.Marshal(receipt)
			nonce := make([]byte, j.aead.NonceSize())
			_, _ = rand.Read(nonce)
			sealed := j.aead.Seal(nonce, nonce, raw, []byte("scope\x00"+ids[name]))
			if err := os.WriteFile(j.path("scope", ids[name]), sealed, 0600); err != nil {
				t.Fatal(err)
			}
		} else if err := j.save("scope", ids[name], receipt); err != nil {
			t.Fatal(err)
		}
		// Ancient mtimes are not an expiry signal for fresh/unknown/legacy data.
		_ = os.Chtimes(j.path("scope", ids[name]), old.Add(-time.Hour), old.Add(-time.Hour))
	}
	result, err := j.sweep(t.Context(), now)
	if err != nil || result.Expired != 1 {
		t.Fatalf("sweep = %+v, %v", result, err)
	}
	for name, id := range ids {
		receipt, found, err := j.load("scope", id)
		if err != nil || !found {
			t.Fatalf("%s lost its fence: %v", name, err)
		}
		if name == "expired" {
			if receipt.Complete || len(receipt.Body) != 0 || receipt.Digest != execution.Digest([]byte("original input")) {
				t.Fatal("expired result did not become a compact fence")
			}
			if _, err := os.Stat(j.path("scope", id)); !os.IsNotExist(err) {
				t.Fatal("expired ciphertext remains")
			}
		} else if _, err := os.Stat(j.path("scope", id)); err != nil {
			t.Fatalf("%s was removed", name)
		}
	}
	if _, found, err := j.load("other scope", ids["expired"]); err != nil || found {
		t.Fatal("retired receipt crossed identity scopes", err)
	}
	_ = filepath.Walk(j.retiredRoot(), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			raw, _ := os.ReadFile(path)
			if bytes.Contains(raw, []byte("retention canary")) || bytes.Contains(raw, []byte("settings.owner.patch")) {
				t.Fatal("plaintext result or operation in retired storage")
			}
		}
		return err
	})
}

func TestISCPReceiptExpirySurvivesRestartAndNeverRepeatsBusinessMutation(t *testing.T) {
	server, repo, cfg, _ := workbenchISCPFixture(t, nil)
	handler, err := server.NewWorkbenchISCPDomainHandler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := domainTestContext(t)
	read := handler(ctx, domainTestRequest(iscpworkbench.OperationSettingsOwnerGet, nil))
	var state struct{ Revision string }
	if err := json.Unmarshal(read.Body, &state); err != nil {
		t.Fatal(err)
	}
	request := domainTestRequest(iscpworkbench.OperationSettingsOwnerPatch, []byte(`{"display_name":"retained owner"}`))
	request.ExpectedRevision = state.Revision
	response := handler(ctx, request)
	if response.Status != 200 {
		t.Fatal(response)
	}
	j := server.domainReceipts
	scope := cfg.Binding.DeploymentID + "\x00iscp-owner\x00iscp-desktop-client\x00" + iscpTestInstallation
	receipt, found, err := j.load(scope, request.OperationID)
	if err != nil || !found || receipt.CompletedAt.IsZero() {
		t.Fatal("completion timestamp missing", err)
	}
	now := receipt.CompletedAt.Add(domainReceiptRetention)
	if result, err := j.sweep(ctx, now.Add(-time.Nanosecond)); err != nil || result.Expired != 0 {
		t.Fatal(result, err)
	}
	if replay := handler(ctx, request); replay.Status != 200 || !bytes.Equal(replay.Body, response.Body) {
		t.Fatal("unexpired response changed", replay)
	}
	before, _ := repo.EventsAfter(ctx, "", "")
	if result, err := j.sweep(ctx, now); err != nil || result.Expired != 1 {
		t.Fatal(result, err)
	}
	// A new journal object reads only disk; there is no in-memory dedupe state.
	restarted, err := newISCPDomainReceipts(server.executionRoot)
	if err != nil {
		t.Fatal(err)
	}
	a := &iscpDomainAdapter{server: server, config: cfg, receipts: restarted}
	if replay := a.handle(ctx, request); replay.Status != 409 || !bytes.Contains(replay.Body, []byte("operation_outcome_unknown")) {
		t.Fatal("expired operation was readmitted", replay)
	}
	request.Body = []byte(`{"display_name":"must never run"}`)
	if changed := a.handle(ctx, request); changed.Status != 409 || !bytes.Contains(changed.Body, []byte("operation_input_conflict")) {
		t.Fatal("input fence lost", changed)
	}
	after, _ := repo.EventsAfter(ctx, "", "")
	if len(before) != len(after) {
		t.Fatal("expiry replay produced another business event")
	}
	owner, _, _ := repo.GetOwnerProfileByID(ctx, "iscp-owner")
	if owner.DisplayName != "retained owner" {
		t.Fatal("cleanup changed owner state")
	}
}

func TestISCPReceiptRetentionCrashOrderAndWriteFailure(t *testing.T) {
	t.Run("both copies after crash", func(t *testing.T) {
		j := receiptRetentionFixture(t)
		id := domainTestRequest("id", nil).ID
		if err := j.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
		path := j.path("scope", id)
		original, _ := os.ReadFile(path)
		if _, err := j.sweep(t.Context(), time.Now()); err != nil {
			t.Fatal(err)
		}
		// Simulate a crash after index commit but before unlink became durable.
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		restarted, err := newISCPDomainReceipts(filepath.Dir(j.root))
		if err != nil {
			t.Fatal(err)
		}
		defer restarted.closeScanLocked()
		if receipt, found, err := restarted.load("scope", id); err != nil || !found || receipt.Complete {
			t.Fatal("old result overrode committed fence", err)
		}
		if result, err := restarted.sweep(t.Context(), time.Now()); err != nil || result.Expired != 1 {
			t.Fatal(result, err)
		}
	})
	t.Run("index commit fails", func(t *testing.T) {
		j := receiptRetentionFixture(t)
		id := domainTestRequest("id", nil).ID
		if err := j.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
		key := filepath.Base(j.path("scope", id))
		// An unsafe shard target must stop reclamation before touching the result.
		if err := os.Mkdir(filepath.Join(j.retiredRoot(), key[:2]+".sealed"), 0700); err != nil {
			t.Fatal(err)
		}
		if result, err := j.sweep(t.Context(), time.Now()); err == nil || result.Expired != 0 {
			t.Fatal(result, err)
		}
		if _, err := os.Stat(j.path("scope", id)); err != nil {
			t.Fatal("failed index write deleted result")
		}
		if _, _, err := j.load("scope", id); err == nil {
			t.Fatal("unsafe shard interpreted as absent")
		}
	})
}

func TestISCPReceiptRetentionRejectsTamperAndMovedCiphertext(t *testing.T) {
	for _, tamper := range []string{"index", "moved result"} {
		t.Run(tamper, func(t *testing.T) {
			j := receiptRetentionFixture(t)
			id := domainTestRequest("id", nil).ID
			if err := j.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
				t.Fatal(err)
			}
			path := j.path("scope", id)
			if tamper == "index" {
				if _, err := j.sweep(t.Context(), time.Now()); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(j.retiredRoot(), filepath.Base(path)[:2]+".sealed")
				raw, _ := os.ReadFile(path)
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, _, err := j.load("scope", id); err == nil {
					t.Fatal("corrupt fence became absence")
				}
			} else {
				other := j.path("scope", domainTestRequest("id", nil).ID)
				raw, _ := os.ReadFile(path)
				if err := os.WriteFile(other, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, _, err := j.readReceipt(other, nil); err == nil {
					t.Fatal("ciphertext accepted under different key")
				}
			}
		})
	}
}

func TestISCPReceiptSweepIsBoundedAndReclaimsSlots(t *testing.T) {
	j := receiptRetentionFixture(t)
	for i := 0; i < domainReceiptSweepLimit+20; i++ {
		if err := j.save("scope", domainTestRequest("id", nil).ID, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	expired := 0
	for i := 0; i < 20 && expired < domainReceiptSweepLimit+20; i++ {
		result, err := j.sweep(t.Context(), time.Now())
		if err != nil || result.Scanned > domainReceiptSweepLimit {
			t.Fatal(result, err)
		}
		expired += result.Expired
	}
	if expired != domainReceiptSweepLimit+20 {
		t.Fatal("bounded sweeps did not advance", expired)
	}
	entries, _ := os.ReadDir(j.root)
	if len(entries) != 3 || !j.hasCapacity(domainReceiptMaxBytes) {
		t.Fatal("expired files still consume live slots", len(entries))
	}
	shards, _ := os.ReadDir(j.retiredRoot())
	if len(shards) > 256 {
		t.Fatal("retired index file count is unbounded")
	}
	// The previous binary's exact directory rule rejects the new layout.
	oldWriterAccepts := true
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			oldWriterAccepts = false
		}
	}
	if oldWriterAccepts {
		t.Fatal("previous writer can mistake retired IDs for new work")
	}
	// Compact proofs still consume the shared byte quota.
	f, err := os.OpenFile(filepath.Join(j.retiredRoot(), "quota"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(domainReceiptTotalBytes)
	_ = f.Close()
	if err != nil || j.hasCapacity(1) {
		t.Fatal("retired bytes bypass shared quota", err)
	}
}

func TestISCPReceiptRetentionRunsAutomaticallyAndDrainsAtShutdown(t *testing.T) {
	server, _, cfg, _ := workbenchISCPFixture(t, nil)
	if _, err := server.NewWorkbenchISCPDomainHandler(cfg); err != nil {
		t.Fatal(err)
	}
	j := server.domainReceipts
	if _, err := server.NewWorkbenchISCPDomainHandler(cfg); err != nil || server.domainReceipts != j {
		t.Fatal("peer adapters do not share one journal", err)
	}
	id := domainTestRequest("id", nil).ID
	if err := j.save("scope", id, completedRetentionReceipt(time.Now().Add(-8*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	server.StartRetentionSweeps(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(j.path("scope", id)); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup sweep never removed expired result")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wait, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := server.WaitForBackgroundWork(wait); err != nil {
		t.Fatal(err)
	}
	if j.scan != nil {
		t.Fatal("directory scan survived shutdown")
	}
}

func TestISCPReceiptRetentionMissingHistoryNeverBecomesAbsence(t *testing.T) {
	for _, missing := range []string{"shard", "directory", "catalog"} {
		t.Run(missing, func(t *testing.T) {
			j := receiptRetentionFixture(t)
			id := domainTestRequest("id", nil).ID
			r := completedRetentionReceipt(time.Now().Add(-8 * 24 * time.Hour))
			r.Body = nil
			if err := j.save("scope", id, r); err != nil {
				t.Fatal(err)
			}
			if result, err := j.sweep(t.Context(), time.Now()); err != nil || result.Expired != 1 {
				t.Fatal(result, err)
			}
			switch missing {
			case "shard":
				if err := os.Remove(filepath.Join(j.retiredRoot(), filepath.Base(j.path("scope", id))[:2]+".sealed")); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Rename(j.retiredRoot(), j.retiredRoot()+".lost"); err != nil {
					t.Fatal(err)
				}
			case "catalog":
				if err := os.Remove(filepath.Join(j.root, "retired-catalog.sealed")); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := j.load("scope", id); err == nil {
				t.Fatal("missing history was interpreted as absence")
			}
			fresh, err := newISCPDomainReceipts(filepath.Dir(j.root))
			if err == nil {
				if _, _, err := fresh.load("scope", id); err == nil {
					t.Fatal("restart forgot missing history")
				}
			}
		})
	}
}

func TestISCPReceiptRetentionKeepsHistoricalPollFences(t *testing.T) {
	j := receiptRetentionFixture(t)
	now := time.Now().UTC()
	ids := map[string]string{}
	for _, operation := range []string{"settings.bindings.poll"} {
		ids[operation] = domainTestRequest("id", nil).ID
		receipt := completedRetentionReceipt(now.Add(-domainReceiptRetention))
		receipt.Operation = operation
		// save() no longer stamps these operations; write the stamp that the
		// earlier release persisted.
		if err := j.save("scope", ids[operation], receipt); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := j.sweep(t.Context(), now); err != nil || result.Expired != 1 {
		t.Fatalf("historical receipts did not expire: %+v, %v", result, err)
	}
	for operation, id := range ids {
		receipt, found, err := j.load("scope", id)
		if err != nil || !found || receipt.Complete || receipt.Operation != operation {
			t.Fatalf("%s retired fence unreadable: %+v found=%v err=%v", operation, receipt, found, err)
		}
	}
}
