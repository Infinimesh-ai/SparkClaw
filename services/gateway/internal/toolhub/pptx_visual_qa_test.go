package toolhub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/configtest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

type recordingPPTXVisualQARunner struct {
	request       pptxVisualQARequest
	candidateSeen bool
	result        pptxVisualQAResult
	err           error
}

func (runner *recordingPPTXVisualQARunner) Assess(_ context.Context, request pptxVisualQARequest) (pptxVisualQAResult, error) {
	runner.request = request
	_, statErr := os.Stat(request.CandidatePath)
	runner.candidateSeen = statErr == nil
	if runner.err == nil && statErr == nil {
		raw, readErr := os.ReadFile(request.CandidatePath)
		if readErr == nil {
			runner.result.CandidateSHA256 = pptxBytesSHA256(raw)
		}
	}
	return runner.result, runner.err
}

func TestPPTXVisualQASelectionUsesOnlyChangedSlidesAndShapes(t *testing.T) {
	slides, shapes, changedAll, err := pptxVisualQASelection("update_deck", map[string]any{
		"slide_updates": []any{
			map[string]any{"slide_index": 4, "updates": []any{map[string]any{"shape_index": 7}}},
			map[string]any{"slide_index": 2, "updates": []any{map[string]any{"shape_index": 3}, map[string]any{"shape_index": 3}}},
		},
	}, map[string]any{
		"layout_adjusted_targets": []any{map[string]any{"slide_index": 4, "shape_index": 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(slides, []int{2, 4}) || !slices.Equal(shapes[2], []int{3}) || !slices.Equal(shapes[4], []int{7, 9}) || len(changedAll) != 0 {
		t.Fatalf("unexpected PPTX visual QA selection: slides=%#v shapes=%#v", slides, shapes)
	}
}

func TestPPTXVisualQASelectionUsesMutationResultForReplacementAndInsertion(t *testing.T) {
	slides, shapes, changedAll, err := pptxVisualQASelection("replace_text", nil, map[string]any{
		"slide_indexes": []any{3, 1},
		"changed_shape_indexes": []any{
			map[string]any{"slide_index": 3, "shape_indexes": []any{4}},
			map[string]any{"slide_index": 1, "shape_indexes": []any{2}},
		},
	})
	if err != nil || !slices.Equal(slides, []int{1, 3}) || !slices.Equal(shapes[1], []int{2}) || !slices.Equal(shapes[3], []int{4}) || len(changedAll) != 0 {
		t.Fatalf("unexpected replacement selection: slides=%#v shapes=%#v all=%#v err=%v", slides, shapes, changedAll, err)
	}

	slides, shapes, changedAll, err = pptxVisualQASelection("add_slide", nil, map[string]any{"inserted_slide_index": 2})
	if err != nil || !slices.Equal(slides, []int{2}) || len(shapes[2]) != 0 || !slices.Equal(changedAll, []int{2}) {
		t.Fatalf("unexpected insertion selection: slides=%#v shapes=%#v all=%#v err=%v", slides, shapes, changedAll, err)
	}
}

func TestPPTXMutationToolClassificationUsesInputFormatForSharedAlias(t *testing.T) {
	hub := &ToolHub{}
	for _, test := range []struct {
		name string
		tool string
		path string
		want bool
	}{
		{name: "canonical PPTX tool", tool: "pptx.update_slide", path: "report.docx", want: true},
		{name: "shared alias with PPTX", tool: "office.replace_text", path: "deck.pptx", want: true},
		{name: "shared alias with DOCX", tool: "office.replace_text", path: "report.docx", want: false},
		{name: "shared alias with XLSX", tool: "office.replace_text", path: "book.xlsx", want: false},
		{name: "shared alias without path", tool: "office.replace_text", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := hub.IsPPTXMutationTool(test.tool, map[string]any{"path": test.path}); got != test.want {
				t.Fatalf("IsPPTXMutationTool(%q, %q) = %t, want %t", test.tool, test.path, got, test.want)
			}
		})
	}
}

func TestPPTXCandidatePreparationTimeoutCoversConfiguredRepairBudget(t *testing.T) {
	base := time.Duration(pptxEditTimeoutMS) * time.Millisecond
	for _, test := range []struct {
		name string
		cfg  config.PPTXVisualQAAdapterConfig
		want time.Duration
	}{
		{
			name: "disabled uses mutation timeout",
			cfg:  config.PPTXVisualQAAdapterConfig{Phase: "disabled", TimeoutSeconds: 240, MaxRepairAttempts: 2},
			want: base,
		},
		{
			name: "short visual budget keeps mutation floor",
			cfg:  config.PPTXVisualQAAdapterConfig{Phase: "shadow", TimeoutSeconds: 30},
			want: base,
		},
		{
			name: "missing render timeout uses default",
			cfg:  config.PPTXVisualQAAdapterConfig{Phase: "warning", MaxRepairAttempts: 1},
			want: 330 * time.Second,
		},
		{
			name: "two repairs include render and planner budgets",
			cfg:  config.PPTXVisualQAAdapterConfig{Phase: "qualified_blocking", TimeoutSeconds: 120, MaxRepairAttempts: 2},
			want: 510 * time.Second,
		},
		{
			name: "total preparation is capped",
			cfg:  config.PPTXVisualQAAdapterConfig{Phase: "default_on", TimeoutSeconds: 240, MaxRepairAttempts: 2},
			want: 10 * time.Minute,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := pptxCandidatePreparationTimeout(test.cfg); got != test.want {
				t.Fatalf("pptxCandidatePreparationTimeout(%#v) = %s, want %s", test.cfg, got, test.want)
			}
		})
	}
}

func TestPreparePPTXCandidateRunsVisualQAShadowBeforeSealing(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	state := store.NewMemoryStore()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".artifacts")
	cfg.Adapters.PPTXVisualQA.Phase = "shadow"
	hub := New(cfg, state)
	runner := &recordingPPTXVisualQARunner{result: pptxVisualQAResult{
		SchemaVersion: pptxRenderAnalysisSchema, Status: "completed", CandidateSHA256: "candidate", PDFSHA256: "pdf", SlideCount: 2,
	}}
	hub.pptxVisualQA = runner

	_, err := hub.PreparePPTXCandidate(t.Context(), "pptx.update_slide", map[string]any{
		"path": "deck.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "deck.pptx"),
		"slide_index": 2, "layout_policy": "preserve",
		"updates":     []any{map[string]any{"shape_index": 2, "old_text": "Second body", "text": "Changed body"}},
		"output_path": "outputs/ignored.pptx",
	}, "session-visual", "run-visual")
	if err != nil {
		t.Fatal(err)
	}
	if !runner.candidateSeen || !slices.Equal(runner.request.SlideIndexes, []int{2}) || !slices.Equal(runner.request.ChangedShapeIndexes[2], []int{2}) {
		t.Fatalf("visual QA did not receive the ephemeral changed page: %#v", runner.request)
	}
	if _, err := os.Stat(runner.request.CandidatePath); !os.IsNotExist(err) {
		t.Fatalf("PPTX visual QA candidate was not cleaned up: %v", err)
	}
	events := mustToolHubListAudit(t, state, "")
	if !hasToolhubAuditType(events, "document.pptx.visual_qa") {
		t.Fatalf("PPTX visual QA shadow evidence was not audited: %#v", events)
	}
	foundAudit := false
	for _, event := range events {
		if event.Type != "document.pptx.visual_qa" {
			continue
		}
		foundAudit = true
		auditedSlides, _ := event.Fields["slide_indexes"].([]int)
		if event.Fields["phase"] != "shadow" || event.Fields["operation"] != "update_slide" || !slices.Equal(auditedSlides, []int{2}) {
			t.Fatalf("PPTX visual QA audit omitted rollout or selection context: %#v", event.Fields)
		}
	}
	if !foundAudit {
		t.Fatal("PPTX visual QA audit event was not found")
	}
}

func TestPreparePPTXCandidateUsesOperationSpecificVisualSelection(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	sourceSHA := docxSourceSHA256ForTest(t, root, "deck.pptx")
	for _, test := range []struct {
		name       string
		tool       string
		args       map[string]any
		wantSlides []int
		wantShapes map[int][]int
		wantAll    []int
	}{
		{
			name: "replacement", tool: "pptx.replace_text",
			args: map[string]any{
				"path": "deck.pptx", "source_sha256": sourceSHA, "expected_replacements": 1,
				"replacements": []any{map[string]any{"find": "Second body", "replace": "Improved body"}},
			},
			wantSlides: []int{2}, wantShapes: map[int][]int{2: {2}},
		},
		{
			name: "insertion", tool: "pptx.add_slide",
			args: map[string]any{
				"path": "deck.pptx", "source_sha256": sourceSHA, "after_slide_index": 1,
				"layout_ref": "layout:/ppt/slideLayouts/slideLayout2.xml", "title": "Inserted title", "body": "Inserted body",
			},
			wantSlides: []int{2}, wantAll: []int{2},
		},
		{
			name: "duplicate", tool: "pptx.duplicate_slide",
			args: map[string]any{"path": "deck.pptx", "source_sha256": sourceSHA, "slide_index": 1},
		},
		{
			name: "delete", tool: "pptx.delete_slide",
			args: map[string]any{"path": "deck.pptx", "source_sha256": sourceSHA, "slide_index": 2},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Workspaces.DefaultRoot = root
			cfg.Workspaces.Allowlist = []string{root}
			cfg.Storage.ArtifactDir = filepath.Join(root, ".artifacts", test.name)
			cfg.Adapters.PPTXVisualQA.Phase = "shadow"
			hub := New(cfg, store.NewMemoryStore())
			runner := &recordingPPTXVisualQARunner{result: pptxVisualQAResult{
				SchemaVersion: pptxRenderAnalysisSchema, Status: "completed", CandidateSHA256: "candidate", PDFSHA256: "pdf", SlideCount: 2,
			}}
			hub.pptxVisualQA = runner
			args := cloneTestMap(test.args)
			args["output_path"] = "outputs/ignored.pptx"
			if _, err := hub.PreparePPTXCandidate(t.Context(), test.tool, args, "session", "run"); err != nil {
				t.Fatal(err)
			}
			if !runner.candidateSeen || !slices.Equal(runner.request.SlideIndexes, test.wantSlides) || !slices.Equal(runner.request.ChangedAllSlides, test.wantAll) {
				t.Fatalf("unexpected visual request: %#v", runner.request)
			}
			for slideIndex, want := range test.wantShapes {
				if !slices.Equal(runner.request.ChangedShapeIndexes[slideIndex], want) {
					t.Fatalf("slide %d shape selection = %#v, want %#v", slideIndex, runner.request.ChangedShapeIndexes[slideIndex], want)
				}
			}
		})
	}
}

func TestPreparePPTXCandidateShadowBlocksIntegrityButNotModelFailure(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	baseArgs := map[string]any{
		"path": "deck.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "deck.pptx"),
		"slide_index": 2, "layout_policy": "preserve",
		"updates":     []any{map[string]any{"shape_index": 2, "old_text": "Second body", "text": "Changed body"}},
		"output_path": "outputs/ignored.pptx",
	}
	for _, test := range []struct {
		name    string
		kind    pptxVisualQAErrorKind
		wantErr bool
	}{
		{name: "integrity", kind: pptxVisualQAIntegrityError, wantErr: true},
		{name: "model", kind: pptxVisualQAModelError, wantErr: false},
		{name: "infrastructure", kind: pptxVisualQAInfrastructureError, wantErr: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.Workspaces.DefaultRoot = root
			cfg.Workspaces.Allowlist = []string{root}
			cfg.Storage.ArtifactDir = filepath.Join(root, ".artifacts", test.name)
			cfg.Adapters.PPTXVisualQA.Phase = "shadow"
			hub := New(cfg, store.NewMemoryStore())
			hub.pptxVisualQA = &recordingPPTXVisualQARunner{err: &pptxVisualQAError{Kind: test.kind, Err: os.ErrInvalid}}
			_, err := hub.PreparePPTXCandidate(t.Context(), "pptx.update_slide", cloneTestMap(baseArgs), "session", "run")
			if (err != nil) != test.wantErr {
				t.Fatalf("shadow error for %s = %v", test.kind, err)
			}
		})
	}
}

func TestPPTXSealedCandidatePublishesExactBytesAndIsIdempotent(t *testing.T) {
	hub, args, binding, manifest, candidate, root := preparePPTXSealedCandidateTest(t)
	outputPath := filepath.Join(root, "outputs", "sealed.pptx")
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("sealed candidate was visible before approval: %v", err)
	}
	sealedArgs := AttachPPTXSealedCandidate(args, binding)
	first, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	published, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(published, candidate) || pptxBytesSHA256(published) != manifest.CandidateSHA256 {
		t.Fatal("published PPTX bytes differ from the sealed candidate")
	}
	second, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", sealedArgs, "session", "run")
	if err != nil {
		t.Fatalf("idempotent sealed publication failed: %v", err)
	}
	if stringArg(first.Output.(map[string]any), "output_path", "") != outputPath ||
		stringArg(second.Output.(map[string]any), "output_path", "") != outputPath {
		t.Fatalf("sealed publication returned the wrong output: first=%#v second=%#v", first.Output, second.Output)
	}
}

func TestPPTXSealedCandidateRejectsSubstitutionArgumentDriftAndExpiry(t *testing.T) {
	t.Run("candidate substitution", func(t *testing.T) {
		hub, args, binding, manifest, _, _ := preparePPTXSealedCandidateTest(t)
		if _, err := hub.artifacts.Put(t.Context(), manifest.CandidateKey, "application/vnd.openxmlformats-officedocument.presentationml.presentation", []byte("PK-substituted")); err != nil {
			t.Fatal(err)
		}
		_, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run")
		if err == nil || !strings.Contains(err.Error(), "bytes do not match") {
			t.Fatalf("substituted candidate was not rejected: %v", err)
		}
	})

	t.Run("manifest substitution", func(t *testing.T) {
		hub, args, binding, _, _, _ := preparePPTXSealedCandidateTest(t)
		if _, err := hub.artifacts.Put(t.Context(), binding.ManifestKey, "application/json", []byte(`{"schema_version":"forged"}`)); err != nil {
			t.Fatal(err)
		}
		_, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run")
		if err == nil || !strings.Contains(err.Error(), "manifest digest mismatch") {
			t.Fatalf("substituted manifest was not rejected: %v", err)
		}
	})

	t.Run("argument drift", func(t *testing.T) {
		hub, args, binding, _, _, _ := preparePPTXSealedCandidateTest(t)
		args["output_path"] = "outputs/other.pptx"
		_, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run")
		if err == nil || !strings.Contains(err.Error(), "arguments do not match") {
			t.Fatalf("drifted arguments were not rejected: %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		hub, args, binding, manifest, _, _ := preparePPTXSealedCandidateTest(t)
		manifest.ExpiresAt = time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		manifestRaw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		binding.ExpiresAt = manifest.ExpiresAt
		binding.ManifestSHA256 = pptxBytesSHA256(manifestRaw)
		binding.ManifestKey = filepath.ToSlash(filepath.Join(filepath.Dir(binding.ManifestKey), binding.ManifestSHA256+".json"))
		if _, err := hub.artifacts.Put(t.Context(), binding.ManifestKey, "application/json", manifestRaw); err != nil {
			t.Fatal(err)
		}
		_, err = hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run")
		if err == nil || !strings.Contains(err.Error(), "expired") {
			t.Fatalf("expired sealed candidate was not rejected: %v", err)
		}
	})

	t.Run("policy drift", func(t *testing.T) {
		hub, args, binding, _, _, _ := preparePPTXSealedCandidateTest(t)
		hub.cfg.Adapters.PPTXVisualQA.MaxRepairAttempts = 1
		_, err := hub.PublishSealedPPTXCandidate(t.Context(), "pptx.update_slide", AttachPPTXSealedCandidate(args, binding), "session", "run")
		if app.ToolErrorCodeFrom(err) != app.ToolErrorPPTXRenderSourceStale || !strings.Contains(err.Error(), "policy changed") {
			t.Fatalf("changed visual policy did not invalidate the sealed candidate: %v", err)
		}
	})
}

func TestPPTXSealedApprovalArgumentsMustMatch(t *testing.T) {
	_, args, binding, _, _, _ := preparePPTXSealedCandidateTest(t)
	callArgs := AttachPPTXSealedCandidate(args, binding)
	approvalArgs := AttachPPTXSealedCandidate(cloneTestMap(args), binding)
	approvalArgs["output_path"] = "outputs/changed.pptx"
	if err := ValidatePPTXSealedApprovalArguments(callArgs, approvalArgs); err == nil {
		t.Fatal("mismatched approval arguments were accepted")
	}
}

func preparePPTXSealedCandidateTest(t *testing.T) (*ToolHub, map[string]any, PPTXSealedCandidateBinding, pptxSealedCandidateManifest, []byte, string) {
	t.Helper()
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	cfg.Storage.ArtifactDir = filepath.Join(root, ".artifacts")
	cfg.Adapters.PPTXVisualQA.Phase = "disabled"
	hub := New(cfg, store.NewMemoryStore())
	t.Cleanup(func() { _ = hub.Close() })
	args := map[string]any{
		"path": "deck.pptx", "source_sha256": docxSourceSHA256ForTest(t, root, "deck.pptx"),
		"slide_index": 2, "layout_policy": "preserve",
		"updates":     []any{map[string]any{"shape_index": 2, "old_text": "Second body", "text": "Changed body"}},
		"output_path": "outputs/sealed.pptx",
	}
	binding, err := hub.PreparePPTXCandidate(t.Context(), "pptx.update_slide", args, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	manifestRaw, err := hub.artifacts.Get(t.Context(), binding.ManifestKey)
	if err != nil {
		t.Fatal(err)
	}
	var manifest pptxSealedCandidateManifest
	if err := decodePPTXVisualStrictJSON(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	candidate, err := hub.artifacts.Get(t.Context(), manifest.CandidateKey)
	if err != nil {
		t.Fatal(err)
	}
	return hub, args, binding, manifest, candidate, root
}

func TestPPTXVisualQARejectsHTTP200NonPDF(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "candidate.pptx")
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("writer error page"))
	}))
	defer renderer.Close()
	service := newPPTXVisualQAService(testPPTXVisualQAConfig(renderer.URL), nil)

	_, err := service.Assess(t.Context(), pptxVisualQARequest{CandidatePath: filepath.Join(root, "candidate.pptx"), Operation: "update_slide", SlideIndexes: []int{1}})
	if err == nil || pptxVisualQAErrorKindOf(err) != pptxVisualQAIntegrityError || !strings.Contains(err.Error(), "not a PDF") {
		t.Fatalf("HTTP 200 non-PDF response was accepted: %v", err)
	}
}

func TestPPTXVisualQAEndToEndUsesFixedRenderAndStrictFastReview(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "candidate.pptx")
	pdfPath := filepath.Join(root, "rendered.pdf")
	writePPTXVisualQAPDFFixture(t, pdfPath, 960, 720)
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	rendererCalls := 0
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rendererCalls++
		if r.URL.Path != "/forms/libreoffice/convert" {
			t.Fatalf("unexpected Gotenberg path %q", r.URL.Path)
		}
		if err := r.ParseMultipartForm(2 << 20); err != nil {
			t.Fatal(err)
		}
		file, _, err := r.FormFile("files")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		header := make([]byte, 2)
		if _, err := io.ReadFull(file, header); err != nil || string(header) != "PK" {
			t.Fatalf("Gotenberg upload was not a PPTX: header=%q err=%v", header, err)
		}
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdf)
	}))
	defer renderer.Close()

	modelCalls := []string{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		responseFormat, _ := body["response_format"].(map[string]any)
		jsonSchema, _ := responseFormat["json_schema"].(map[string]any)
		name, _ := jsonSchema["name"].(string)
		modelCalls = append(modelCalls, name)
		content := `{"schema_version":"sparkclaw.pptx_visual_readiness.v1","image_seen":true}`
		if name == "pptx_visual_assessment" {
			content = `{"schema_version":"sparkclaw.pptx_visual_assessment.v1","slide_index":1,"fact_reviews":[],"subjective_issues":[]}`
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"content": content}, "finish_reason": "stop"}},
		})
	}))
	defer model.Close()

	cfg := configtest.MustLoadDefault()
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = model.URL
	cfg.Adapters.PPTXVisualQA = testPPTXVisualQAConfig(renderer.URL)
	service := newPPTXVisualQAService(cfg.Adapters.PPTXVisualQA, modelrouter.New(cfg))
	result, err := service.Assess(t.Context(), pptxVisualQARequest{
		CandidatePath: filepath.Join(root, "candidate.pptx"), Operation: "update_slide",
		SlideIndexes: []int{1}, ChangedShapeIndexes: map[int][]int{1: []int{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendererCalls != 1 || !slices.Equal(modelCalls, []string{"pptx_visual_readiness", "pptx_visual_assessment"}) {
		t.Fatalf("unexpected render/model calls: renderer=%d model=%#v", rendererCalls, modelCalls)
	}
	if result.Status != "completed" || result.SlideCount != 1 || len(result.Pages) != 1 || result.Pages[0].SlideIndex != 1 || result.Pages[0].Raster.Width != 960 || result.Pages[0].Raster.Height != 720 || !result.Pages[0].Raster.UniformWhite {
		t.Fatalf("unexpected PPTX visual QA result: %#v", result)
	}
	if len(result.Pages[0].Diagnostics.Facts) != 1 || result.Pages[0].Diagnostics.Facts[0].Kind != "text_clipping" || result.Pages[0].Diagnostics.Facts[0].Status != "unavailable" {
		t.Fatalf("missing explicit unavailable clipping evidence: %#v", result.Pages[0].Diagnostics)
	}
}

func TestPPTXVisualQAEmptyPageSelectionStillValidatesCompleteRender(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "candidate.pptx")
	pdfPath := filepath.Join(root, "rendered.pdf")
	writePPTXVisualQAPDFFixture(t, pdfPath, 960, 720)
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	rendererCalls := 0
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rendererCalls++
		_, _ = w.Write(pdf)
	}))
	defer renderer.Close()

	service := newPPTXVisualQAService(testPPTXVisualQAConfig(renderer.URL), nil)
	result, err := service.Assess(t.Context(), pptxVisualQARequest{
		CandidatePath: filepath.Join(root, "candidate.pptx"), Operation: "duplicate_slide",
	})
	if err != nil {
		t.Fatal(err)
	}
	if rendererCalls != 1 || result.Status != "completed" || result.SlideCount != 1 || len(result.Pages) != 0 {
		t.Fatalf("empty selection skipped conversion or produced pages: calls=%d result=%#v", rendererCalls, result)
	}
}

func TestPPTXVisualQAValidatesUnselectedPDFPageDimensions(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "candidate.pptx")
	pdfPath := filepath.Join(root, "rendered.pdf")
	writePPTXVisualQAMultiPagePDFFixture(t, pdfPath, [][2]int{{960, 720}, {1000, 500}})
	pdf, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	renderer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pdf)
	}))
	defer renderer.Close()

	service := newPPTXVisualQAService(testPPTXVisualQAConfig(renderer.URL), nil)
	_, err = service.Assess(t.Context(), pptxVisualQARequest{
		CandidatePath: filepath.Join(root, "candidate.pptx"), Operation: "delete_slide",
	})
	if err == nil || pptxVisualQAErrorKindOf(err) != pptxVisualQAIntegrityError || !strings.Contains(err.Error(), "aspect ratio") {
		t.Fatalf("unselected malformed PDF page was accepted: %v", err)
	}
}

func TestValidatePPTXVisualAssessmentRejectsWrongSemanticEffectAndUnknownShape(t *testing.T) {
	facts := []pptxDiagnosticFact{{DiagnosticID: "diag-1", Kind: "geometry_overlap", Status: "confirmed", ShapeRefs: []string{"slide:1:shape:1"}}}
	assessment := pptxVisualAssessment{
		SchemaVersion: pptxVisualAssessmentSchema, SlideIndex: 1,
		FactReviews: []pptxVisualFactReview{{DiagnosticID: "diag-1", SemanticEffect: "required_content_lost", ConfidenceMilli: 900}},
	}
	if err := validatePPTXVisualAssessment(assessment, 1, facts, []string{"slide:1:shape:1"}); err == nil {
		t.Fatal("diagnostic kind accepted an incompatible semantic effect")
	}
	assessment.FactReviews[0].SemanticEffect = "harmful_obstruction"
	assessment.SubjectiveIssues = []pptxVisualSubjectiveIssue{{
		VisualIssueID: "visual-1", Type: "weak_hierarchy", ConfidenceMilli: 800,
		RegionMilli: []int{0, 0, 1000, 1000}, ShapeRefs: []string{"slide:1:shape:99"},
	}}
	if err := validatePPTXVisualAssessment(assessment, 1, facts, []string{"slide:1:shape:1"}); err == nil {
		t.Fatal("visual assessment accepted an unoffered shape reference")
	}
}

func testPPTXVisualQAConfig(baseURL string) config.PPTXVisualQAAdapterConfig {
	return config.PPTXVisualQAAdapterConfig{
		Phase: "shadow", BaseURL: baseURL, TimeoutSeconds: 30,
		MaxInputBytes: 4 << 20, MaxPDFBytes: 4 << 20, MaxPages: 10, MaxChangedPages: 4,
		RasterScale: 1, MaxPagePixels: 2_000_000, MaxPNGBytes: 2 << 20,
		DiagnosticToleranceMilli: 2, ReadinessTTLSeconds: 300,
	}
}

func writePPTXVisualQAPDFFixture(t *testing.T, path string, width, height int) {
	t.Helper()
	script := `
from pypdf import PdfWriter
writer = PdfWriter()
writer.add_blank_page(width=int(__import__("sys").argv[2]), height=int(__import__("sys").argv[3]))
with open(__import__("sys").argv[1], "wb") as output:
    writer.write(output)
`
	cmd := exec.Command(documentPythonBinary(), "-c", script, path, strconv.Itoa(width), strconv.Itoa(height))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create PPTX visual QA PDF fixture: %v\n%s", err, output)
	}
}

func writePPTXVisualQAMultiPagePDFFixture(t *testing.T, path string, dimensions [][2]int) {
	t.Helper()
	raw, err := json.Marshal(dimensions)
	if err != nil {
		t.Fatal(err)
	}
	script := `
import json
from pypdf import PdfWriter
writer = PdfWriter()
for width, height in json.loads(__import__("sys").argv[2]):
    writer.add_blank_page(width=width, height=height)
with open(__import__("sys").argv[1], "wb") as output:
    writer.write(output)
`
	cmd := exec.Command(documentPythonBinary(), "-c", script, path, string(raw))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create multi-page PPTX visual QA PDF fixture: %v\n%s", err, output)
	}
}
