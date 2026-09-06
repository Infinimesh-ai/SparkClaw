package toolhub

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestDiscardSealedPPTXCandidateRemovesBothObjectsAfterPublication(t *testing.T) {
	hub, args, binding, manifest, _, _ := preparePPTXSealedCandidateTest(t)
	sealedArgs := AttachPPTXSealedCandidate(args, binding)
	if _, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run"); err != nil {
		t.Fatal(err)
	}
	if err := hub.DiscardSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run"); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{manifest.CandidateKey, binding.ManifestKey} {
		if _, err := hub.artifacts.Get(t.Context(), key); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sealed object %s survived discard: %v", key, err)
		}
	}
	if _, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run"); err == nil || !strings.Contains(err.Error(), "read PPTX sealed candidate manifest") {
		t.Fatalf("discarded candidate was still publishable: %v", err)
	}
	if err := hub.DiscardSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run"); err != nil {
		t.Fatalf("repeated discard was not idempotent: %v", err)
	}
	events := mustToolHubListAudit(t, hub.store.(*store.MemoryStore), "")
	found := false
	for _, event := range events {
		if event.Type != "document.pptx.candidate_discarded" {
			continue
		}
		found = true
		if event.SessionID != "session" || event.RunID != "run" || event.Fields["reason"] != "published" ||
			event.Fields["candidate_sha256"] != binding.CandidateSHA256 || event.Fields["manifest_key"] != binding.ManifestKey ||
			event.Fields["candidate_key"] != manifest.CandidateKey {
			t.Fatalf("discard audit omitted its context: %#v", event)
		}
	}
	if !found {
		t.Fatalf("discard was not audited: %#v", events)
	}
	if err := hub.DiscardSealedPPTXCandidate(t.Context(), "pptx.update_slide", args, "session", "run"); err == nil {
		t.Fatal("discard without a sealed binding was accepted")
	}
}

func ageSealedPPTXObjects(t *testing.T, hub *ToolHub, keys ...string) {
	t.Helper()
	files, ok := hub.artifacts.(artifact.FileStore)
	if !ok {
		t.Fatalf("sealed candidate test expects a FileStore, got %T", hub.artifacts)
	}
	old := time.Now().Add(-pptxSealedCandidateTTL - time.Hour)
	for _, key := range keys {
		if err := os.Chtimes(filepath.Join(files.Root, files.Bucket, filepath.FromSlash(key)), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSweepExpiredPPTXSealedCandidatesRemovesOnlyAgedObjects(t *testing.T) {
	hub, args, binding, manifest, _, _ := preparePPTXSealedCandidateTest(t)
	// A second candidate that was never approved (rejected or abandoned)
	// only differs from the fresh one by having crossed the TTL.
	abandonedBinding, err := hub.PreparePPTXCandidate(t.Context(), "pptx.update_slide", cloneTestMap(args), "session", "run-rejected")
	if err != nil {
		t.Fatal(err)
	}
	abandonedCandidateKey := pptxSealedCandidateKey(filepath.Base(filepath.Dir(abandonedBinding.ManifestKey)), abandonedBinding.CandidateSHA256)
	ageSealedPPTXObjects(t, hub, abandonedBinding.ManifestKey, abandonedCandidateKey)

	result, err := hub.SweepExpiredPPTXSealedCandidates(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 4 || result.Deleted != 2 || result.NextCursor != "" {
		t.Fatalf("sweep result = %#v, want 4 scanned, 2 deleted, exhausted", result)
	}
	for _, key := range []string{abandonedBinding.ManifestKey, abandonedCandidateKey} {
		if _, err := hub.artifacts.Get(t.Context(), key); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expired sealed object %s survived the sweep: %v", key, err)
		}
	}
	if _, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, abandonedBinding), "session", "run-rejected"); err == nil {
		t.Fatal("swept candidate was still publishable")
	}
	if _, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run"); err != nil {
		t.Fatalf("fresh candidate was damaged by the sweep: %v", err)
	}
	events := mustToolHubListAudit(t, hub.store.(*store.MemoryStore), "")
	found := false
	for _, event := range events {
		if event.Type != "document.pptx.candidate_expired" {
			continue
		}
		found = true
		deletedKeys, _ := event.Fields["deleted_keys"].([]string)
		if event.Fields["deleted"] != 2 || event.Fields["scanned"] != 4 || len(deletedKeys) != 2 ||
			event.Fields["ttl"] != pptxSealedCandidateTTL.String() || event.SessionID != "" {
			t.Fatalf("expiry audit omitted its context: %#v", event)
		}
	}
	if !found {
		t.Fatalf("expiry sweep was not audited: %#v", events)
	}
	if manifest.CandidateKey == abandonedCandidateKey {
		t.Fatal("test candidates must live in different scopes")
	}
}

func TestSweepExpiredPPTXSealedCandidatesIsBoundedAndCursorContinuable(t *testing.T) {
	hub, _, _, _, _, _ := preparePPTXSealedCandidateTest(t)
	keys := make([]string, 0, 5)
	for _, name := range []string{"a.pptx", "a.json", "b.pptx", "b.json", "c.json"} {
		key := pptxSealedCandidateNamespace + "/stale/" + name
		if _, err := hub.artifacts.Put(t.Context(), key, "application/octet-stream", []byte(name)); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
	}
	ageSealedPPTXObjects(t, hub, keys...)

	var totalDeleted int
	cursor := ""
	for round := 0; ; round++ {
		result, err := hub.SweepExpiredPPTXSealedCandidates(t.Context(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if result.Scanned > 2 || result.Deleted > 2 {
			t.Fatalf("sweep exceeded its bound: %#v", result)
		}
		totalDeleted += result.Deleted
		if result.NextCursor == "" {
			break
		}
		if round > 5 {
			t.Fatalf("sweep never exhausted the namespace: %#v", result)
		}
		cursor = result.NextCursor
	}
	if totalDeleted != 5 {
		t.Fatalf("bounded sweeps deleted %d stale objects, want 5", totalDeleted)
	}
	remaining, err := hub.artifacts.List(t.Context(), pptxSealedCandidateNamespace+"/stale/", "", 10)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("stale objects remained after cursor-driven sweeps: %#v err=%v", remaining, err)
	}
	if fresh, err := hub.artifacts.List(t.Context(), pptxSealedCandidateNamespace+"/", "", 10); err != nil || len(fresh) != 2 {
		t.Fatalf("the fresh sealed candidate pair was not preserved: %#v err=%v", fresh, err)
	}
}

func TestSweepExpiredPPTXSealedCandidatesFailsExplicitlyWithoutListing(t *testing.T) {
	hub, _, _, _, _, _ := preparePPTXSealedCandidateTest(t)
	hub.artifacts = artifact.NotImplementedStore{Backend: "gcs"}
	if _, err := hub.SweepExpiredPPTXSealedCandidates(t.Context(), "", 0); err == nil || !strings.Contains(err.Error(), "not implemented: gcs") {
		t.Fatalf("unsupported backend did not fail explicitly: %v", err)
	}
	hub.artifacts = nil
	if _, err := hub.SweepExpiredPPTXSealedCandidates(t.Context(), "", 0); err == nil {
		t.Fatal("missing artifact store was accepted")
	}
}
