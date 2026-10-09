package emailmanagement

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type attachmentSinkBrowser struct {
	*composeFixture
	root, endpoint string
	beforeRead     func()
	unknown        bool
	staged         string
}

func (b *attachmentSinkBrowser) SendForOwner(ctx context.Context, owner string, request app.EmailSendRequest) (app.EmailSendResult, error) {
	b.sends++
	if b.beforeRead != nil {
		b.beforeRead()
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	root, err := os.OpenRoot(b.root)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	defer root.Close()
	for _, item := range request.Attachments {
		b.staged = item.StagedPath
		data, err := root.ReadFile(item.StagedPath)
		if err != nil {
			return app.EmailSendResult{}, err
		}
		hash := sha256.Sum256(data)
		if int64(len(data)) != item.SizeBytes || "sha256:"+hex.EncodeToString(hash[:]) != item.SHA256 {
			return app.EmailSendResult{}, errors.New("staged content mismatch")
		}
		part, err := writer.CreateFormFile("attachment", item.Name)
		if err != nil {
			return app.EmailSendResult{}, err
		}
		if _, err = part.Write(data); err != nil {
			return app.EmailSendResult{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return app.EmailSendResult{}, err
	}
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpoint, &body)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	call.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(call)
	if err != nil {
		return app.EmailSendResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return app.EmailSendResult{}, errors.New("sink rejected")
	}
	if b.unknown {
		return app.EmailSendResult{}, errors.New("reply lost after accepted bytes")
	}
	return app.EmailSendResult{Provider: "gmail", Status: "sent"}, nil
}

type attachmentObjectFixture struct {
	objects  map[string]app.EmailAttachmentObject
	contents map[string][]byte
	reads    int
}

func newAttachmentObjects() *attachmentObjectFixture {
	return &attachmentObjectFixture{objects: map[string]app.EmailAttachmentObject{}, contents: map[string][]byte{}}
}
func (f *attachmentObjectFixture) add(id, name string, data []byte) app.EmailSendAttachment {
	sum := sha256.Sum256(data)
	o := app.EmailAttachmentObject{ObjectID: id, Version: 1, Name: name, Purpose: app.EmailSendAttachmentPurpose, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
	f.objects[id], f.contents[id] = o, append([]byte(nil), data...)
	return app.EmailSendAttachment{LocalFileID: id, Object: o}
}
func (f *attachmentObjectFixture) ReadAttachmentObject(_ context.Context, owner string, ref app.EmailAttachmentObject, limit int64) (app.EmailAttachmentObject, []byte, error) {
	f.reads++
	o, ok := f.objects[ref.ObjectID]
	if owner != "owner" || !ok || o != ref || o.Size > limit {
		return app.EmailAttachmentObject{}, nil, ErrAttachmentInvalid
	}
	return o, append([]byte(nil), f.contents[ref.ObjectID]...), nil
}

const firstLocalFile = "11111111-1111-4111-8111-111111111111"
const secondLocalFile = "22222222-2222-4222-8222-222222222222"

func TestComposeAttachmentsFrozenBytesReachSinkOnceAfterReplyLoss(t *testing.T) {
	for _, backend := range []string{"memory", "file"} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			state := filepath.Join(t.TempDir(), "state.json")
			var repo Repository = store.NewMemoryStore()
			if backend == "file" {
				file, err := store.NewFileStore(state)
				if err != nil {
					t.Fatal(err)
				}
				repo = file
			}
			box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
			if err != nil {
				t.Fatal(err)
			}
			original := []byte("reviewed desktop attachment\x00binary\xff")
			// An identically named Gateway file is not the source, even on this owner.
			if err = os.WriteFile(filepath.Join(root, "report.bin"), []byte("GATEWAY DECOY"), 0600); err != nil {
				t.Fatal(err)
			}
			objects := newAttachmentObjects()
			input := objects.add(firstLocalFile, "report.bin", original)
			ctx := WithAttachmentObjects(t.Context(), true, objects)
			deliveries := 0
			sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader, err := r.MultipartReader()
				if err != nil {
					t.Error(err)
					http.Error(w, "invalid", 400)
					return
				}
				part, err := reader.NextPart()
				if err != nil {
					t.Error(err)
					http.Error(w, "missing", 400)
					return
				}
				received, err := io.ReadAll(part)
				if err != nil || !bytes.Equal(received, original) || part.FileName() != "report.bin" {
					t.Errorf("received=%q name=%s err=%v", received, part.FileName(), err)
					http.Error(w, "wrong bytes", 400)
					return
				}
				if _, err = reader.NextPart(); err != io.EOF {
					t.Error("unexpected multipart")
				}
				deliveries++
				w.WriteHeader(http.StatusNoContent)
			}))
			defer sink.Close()
			browser := &attachmentSinkBrowser{composeFixture: &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test"}, root: root, endpoint: sink.URL, unknown: true}
			browser.beforeRead = func() { delete(objects.objects, input.Object.ObjectID) }
			service := &Service{repository: repo, browser: browser, opts: Options{WorkspaceRoot: root}}
			draft, err := service.SaveDraft(ctx, "owner", store.EmailDraft{ID: "attachment", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "Review", Body: "Confirmed", Attachments: []app.EmailSendAttachment{input}}, 0)
			if err != nil || len(draft.Attachments) != 1 || draft.Attachments[0].Name != "report.bin" || draft.Attachments[0].SizeBytes != int64(len(original)) {
				t.Fatalf("saved=%+v err=%v", draft, err)
			}
			sent, err := service.SendDraft(ctx, "owner", draft.ID, draft.Version, "confirmed-click")
			if err != nil || sent.State != "unknown" || deliveries != 1 || len(sent.Snapshot.Attachments) != 1 || sent.Snapshot.Attachments[0] != draft.Attachments[0] {
				t.Fatalf("sent=%+v deliveries=%d err=%v", sent, deliveries, err)
			}
			if _, err = os.Stat(filepath.Join(root, browser.staged)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("staged file survived: %v", err)
			}
			if backend == "file" {
				reopened, err := store.NewFileStore(state)
				if err != nil {
					t.Fatal(err)
				}
				service.repository = reopened
			}
			reads := objects.reads
			replay, err := service.SendDraft(ctx, "owner", draft.ID, draft.Version, "confirmed-click")
			if err != nil || replay.State != "unknown" || browser.sends != 1 || deliveries != 1 || objects.reads != reads {
				t.Fatalf("replay=%+v err=%v", replay, err)
			}
			if _, err = service.SendDraft(ctx, "owner", draft.ID, sent.Version, "new-click"); !errors.Is(err, ErrConflict) {
				t.Fatalf("new-key replay %v", err)
			}
			if _, err = service.ReconcileDraft(t.Context(), "owner", draft.ID); err != nil || objects.reads != reads || browser.sends != 1 {
				t.Fatalf("reconcile re-read or sent: %v", err)
			}
		})
	}
}

func TestComposeAttachmentsUnavailableObjectsPermissionsAndLimits(t *testing.T) {
	root := t.TempDir()
	repo := store.NewMemoryStore()
	box, err := repo.BindEmailMailbox(t.Context(), store.EmailBindCommand{EmailCommand: store.EmailCommand{OwnerID: "owner", CommandKey: "bind"}, Provider: "gmail", Address: "owner@example.test", Boundary: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	browser := &composeFixture{intakeFixture: &intakeFixture{}, address: "owner@example.test"}
	s := &Service{repository: repo, browser: browser, opts: Options{WorkspaceRoot: root}}
	objects := newAttachmentObjects()
	input := objects.add(firstLocalFile, "report.txt", []byte("desktop reviewed"))
	ctx := WithAttachmentObjects(t.Context(), true, objects)
	draft, err := s.SaveDraft(ctx, "owner", store.EmailDraft{ID: "draft", MailboxID: box.ID, To: []string{"recipient@example.test"}, Subject: "s", Body: "b", Attachments: []app.EmailSendAttachment{input}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	delete(objects.objects, input.Object.ObjectID)
	if _, err = s.SendDraft(ctx, "owner", draft.ID, draft.Version, "send"); !errors.Is(err, ErrAttachmentChanged) || browser.sends != 0 {
		t.Fatalf("send=%v effects=%d", err, browser.sends)
	}
	saved, _ := s.Drafts(t.Context(), "owner", draft.ID)
	if saved[0].State != "draft" || saved[0].Version != 1 {
		t.Fatal("precondition mutated draft")
	}
	for _, denied := range []context.Context{t.Context(), WithAttachmentObjects(t.Context(), false, objects), WithAttachmentObjects(t.Context(), true, nil)} {
		if _, _, err := s.readDraftAttachments(denied, "owner", []app.EmailSendAttachment{input}, false); !errors.Is(err, ErrAttachmentPermission) {
			t.Fatalf("missing authority: %v", err)
		}
	}
	input = objects.add(firstLocalFile, "report.txt", []byte("desktop reviewed"))
	for _, input := range [][]app.EmailSendAttachment{make([]app.EmailSendAttachment, 6), {input, input}, {{Path: "report.txt"}}, {{LocalFileID: "../outside", Object: input.Object}}} {
		if _, _, err := s.readDraftAttachments(ctx, "owner", input, false); !errors.Is(err, ErrAttachmentInvalid) {
			t.Fatalf("invalid manifest accepted=%v", err)
		}
	}
	duplicateBytes := input
	duplicateBytes.LocalFileID = secondLocalFile
	if manifest, _, err := s.readDraftAttachments(ctx, "owner", []app.EmailSendAttachment{input, duplicateBytes}, false); err != nil || len(manifest) != 2 {
		t.Fatalf("distinct files with deduplicated bytes rejected: %v", err)
	}
	for _, name := range []string{".env", "../file", "bad:file", "bad\tfile", "bad\x7ffile"} {
		bad := objects.add(secondLocalFile, name, []byte("x"))
		if _, _, err := s.readDraftAttachments(ctx, "owner", []app.EmailSendAttachment{bad}, false); !errors.Is(err, ErrAttachmentInvalid) {
			t.Fatalf("invalid filename accepted=%q %v", name, err)
		}
	}
	big := objects.add(secondLocalFile, "huge.bin", make([]byte, app.EmailSendMaxAttachmentBytes))
	if _, _, err := s.readDraftAttachments(ctx, "owner", []app.EmailSendAttachment{input, big}, false); !errors.Is(err, ErrAttachmentInvalid) {
		t.Fatalf("oversized=%v", err)
	}
	for i := 0; i < 32; i++ {
		if err = os.Mkdir(filepath.Join(root, ".sparkclaw-mail-send-"+strings.Repeat("0", 30)+hex.EncodeToString([]byte{byte(i)})), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err = s.stageAttachments([]app.EmailSendAttachment{{Name: "x"}}, [][]byte{{1}}); !errors.Is(err, ErrAttachmentInvalid) {
		t.Fatalf("unbounded staging=%v", err)
	}
	if err = s.cleanupAttachmentStages(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".sparkclaw-mail-send-") {
			t.Fatal("orphan survived restart cleanup")
		}
	}
}

func TestComposeAttachmentsPreserveDistinctFilesWithSameBasename(t *testing.T) {
	root := t.TempDir()
	service := &Service{repository: store.NewMemoryStore(), opts: Options{WorkspaceRoot: root}}
	objects := newAttachmentObjects()
	first := objects.add(firstLocalFile, "report.txt", []byte("first-reviewed"))
	second := objects.add(secondLocalFile, "report.txt", []byte("second-reviewed"))
	manifest, contents, err := service.readDraftAttachments(WithAttachmentObjects(t.Context(), true, objects), "owner", []app.EmailSendAttachment{first, second}, false)
	if err != nil {
		t.Fatal(err)
	}
	staged, cleanup, err := service.stageAttachments(manifest, contents)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(staged) != 2 || staged[0].StagedPath == staged[1].StagedPath || staged[0].Name != "report.txt" || staged[1].Name != "report.txt" || staged[0].SHA256 == staged[1].SHA256 {
		t.Fatalf("same-name files collapsed %+v", staged)
	}
	for i, item := range staged {
		raw, err := os.ReadFile(filepath.Join(root, item.StagedPath))
		if err != nil || !bytes.Equal(raw, contents[i]) || filepath.Base(item.StagedPath) != item.Name {
			t.Fatalf("staged %d mismatch %v", i, err)
		}
	}
}
