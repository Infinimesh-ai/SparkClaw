package emailmanagement

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type attachmentBrowser struct {
	*intakeFixture
	captures int
}

func (b *attachmentBrowser) CaptureForOwner(ctx context.Context, owner string, r app.EmailReadRequest) (app.EmailReadResult, error) {
	b.captures++
	result, err := b.intakeFixture.CaptureForOwner(ctx, owner, r)
	if err != nil {
		return result, err
	}
	manifestPath := filepath.Join(b.root, result.Capture.ManifestPath)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return result, err
	}
	var manifest sourceManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return result, err
	}
	relative := "parts/part_0/order.csv"
	content := []byte("quantity,total_usd\n12,240\n")
	filePath := path.Join(path.Dir(result.Capture.ManifestPath), relative)
	if err = os.MkdirAll(filepath.Dir(filepath.Join(b.root, filePath)), 0700); err != nil {
		return result, err
	}
	if err = os.WriteFile(filepath.Join(b.root, filePath), content, 0600); err != nil {
		return result, err
	}
	manifest.Files = append(manifest.Files, sourceFile{Path: filePath, SHA256: sourceHash(content), Bytes: int64(len(content))})
	parts, _ := json.Marshal([]map[string]any{{"part_id": "part_0", "name": "order.csv", "declared_type": "text/csv", "path": relative, "sha256": sourceHash(content), "bytes": len(content), "status": "available"}})
	if err = json.Unmarshal(parts, &manifest.Attachments); err != nil {
		return result, err
	}
	raw, err = json.Marshal(manifest)
	if err != nil {
		return result, err
	}
	if err = os.WriteFile(manifestPath, raw, 0600); err != nil {
		return result, err
	}
	result.Capture.ManifestSHA256 = sourceHash(raw)
	result.Capture.AttachmentsCount = 1
	return result, nil
}

type recoveringExtractor struct {
	fail  bool
	calls int
}

func (e *recoveringExtractor) ExtractCommittedDocument(context.Context, string, int) (document.ReadResult, error) {
	e.calls++
	if e.fail {
		return document.ReadResult{}, errors.New("extractor unavailable")
	}
	return document.ReadResult{Content: "12 lamps; USD 240", Document: document.Representation{Strategy: document.StrategyMetadata{Complete: true}}}, nil
}

func TestProductionParseNeverExtractsAttachmentsAndKeepsDownloads(t *testing.T) {
	repo := store.NewMemoryStore()
	s, browser, _ := newFixtureService(t, repo)
	attached := &attachmentBrowser{intakeFixture: browser}
	s.browser = attached
	extractor := &recoveringExtractor{fail: true}
	s.extractor = extractor
	box, err := s.Configure(t.Context(), "email-owner", app.EmailProviderGmail, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.discover(t.Context(), app.EmailJob{OwnerID: "email-owner", MailboxID: box.ID, BindingGeneration: box.BindingGeneration}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{app.EmailJobCapture, app.EmailJobParse} {
		if worked, err := s.workOne(t.Context(), []string{kind}); err != nil || !worked {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	for i := 0; i < 12; i++ {
		if err = s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobAssignment}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := repo.ListEmailMails(t.Context(), store.EmailQuery{OwnerID: "email-owner"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("missing mail")
	}
	mail := page.Items[0]
	if mail.ParseState != app.EmailParseReady || mail.ConversationID == "" {
		t.Fatalf("initial attachment gap not represented: %+v", mail)
	}
	extractor.fail = false
	if _, err = s.Reanalyze(t.Context(), "email-owner", mail.ID); err != nil {
		t.Fatal(err)
	}
	if worked, err := s.workOne(t.Context(), []string{app.EmailJobParse}); err != nil || worked {
		t.Fatalf("attachment presence scheduled a reparse: %v", err)
	}
	after, _, err := repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ParseState != app.EmailParseReady || after.RepresentationID != mail.RepresentationID || after.CaptureID != mail.CaptureID || after.ConversationID != mail.ConversationID || attached.captures != 1 {
		t.Fatalf("reparse recaptured/moved/failed: %+v captures=%d", after, attached.captures)
	}
	representation, _, err := repo.GetEmailRepresentation(t.Context(), "email-owner", after.RepresentationID)
	if err != nil || len(representation.Attachments) != 1 || representation.Attachments[0].Text != "" || representation.Attachments[0].State != "not_analyzed" || representation.Attachments[0].Path == "" || extractor.calls != 0 {
		t.Fatal("attachment extraction occurred or verified download was lost")
	}
	for i := 0; i < 25; i++ {
		if err = s.plan(t.Context()); err != nil {
			t.Fatal(err)
		}
		if _, err = s.workOne(t.Context(), []string{app.EmailJobClassification, app.EmailJobAssignment}); err != nil {
			t.Fatal(err)
		}
	}
	after, _, err = repo.GetEmailMail(t.Context(), "email-owner", mail.ID)
	if err != nil || after.Summary != nil || after.Classification == nil || after.ConversationID != mail.ConversationID {
		t.Fatal("source-only classification or preserved event unavailable")
	}
}
