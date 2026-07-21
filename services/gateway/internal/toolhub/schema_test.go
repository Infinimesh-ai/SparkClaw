package toolhub

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/document"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestValidateInputSupportsJSONDecodedSchemaForms(t *testing.T) {
	def := app.ToolDefinition{
		Name: "test.schema",
		InputSchema: map[string]any{
			"type":                 "object",
			"required":             []any{"mode", "count"},
			"additionalProperties": false,
			"properties": map[string]any{
				"mode":  map[string]any{"enum": []any{"fast", "deep"}},
				"count": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(3)},
			},
		},
	}

	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2)}); err != nil {
		t.Fatal(err)
	}
	if err := validateInput(def, map[string]any{"mode": "slow", "count": float64(2)}); err == nil || !strings.Contains(err.Error(), "arguments.mode must be one of [fast, deep]") {
		t.Fatalf("expected enum validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2.5)}); err == nil || !strings.Contains(err.Error(), "arguments.count must be integer") {
		t.Fatalf("expected integer validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(4)}); err == nil || !strings.Contains(err.Error(), "arguments.count must be <= 3") {
		t.Fatalf("expected maximum validation error, got %v", err)
	}
	if err := validateInput(def, map[string]any{"mode": "fast", "count": float64(2), "extra": true}); err == nil || !strings.Contains(err.Error(), "arguments.extra is not allowed") {
		t.Fatalf("expected additionalProperties validation error, got %v", err)
	}
}

func TestImagesInspectUsesMockMultimodalModel(t *testing.T) {
	root := t.TempDir()
	raw, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sample.png"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{
		"path":     "sample.png",
		"question": "这张图片是什么？",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	if output["status"] != "completed" || output["content_type"] != "image/png" || output["mock"] != true {
		t.Fatalf("unexpected image inspection output: %#v", output)
	}
	if output["lane"] != "fast" {
		t.Fatalf("image inspection must use only the Fast model in the first phase: %#v", output)
	}
	if output["width"] != 1 || output["height"] != 1 {
		t.Fatalf("expected 1x1 dimensions, got %#v x %#v", output["width"], output["height"])
	}
	summary, _ := output["summary"].(string)
	if !strings.Contains(summary, "Mock image inspection") {
		t.Fatalf("missing mock image summary: %#v", output["summary"])
	}
}

func TestRenderWeatherCardCreatesMediaPNG(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "media.render_weather_card", map[string]any{
		"location": "杭州",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	mediaPath, _ := output["media_path"].(string)
	if !strings.HasPrefix(mediaPath, "media/") || !strings.HasSuffix(mediaPath, ".png") {
		t.Fatalf("unexpected media path: %#v", output)
	}
	if output["content_type"] != "image/png" || output["width"] != weatherCardWidth || output["height"] != weatherCardHeight {
		t.Fatalf("unexpected weather card metadata: %#v", output)
	}
	if _, err := os.Stat(filepath.Join(root, mediaPath)); err != nil {
		t.Fatalf("weather card was not written: %v", err)
	}
}

func TestRenderWeatherCardRejectsLegacyWeatherInputs(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	for _, field := range []string{"raw_json", "raw_json_ref", "snapshot_ref"} {
		err := hub.Validate("media.render_weather_card", map[string]any{
			"location": "杭州",
			field:      `{"current_condition":[{"temp_C":"30"}]}`,
		})
		if err == nil {
			t.Fatalf("expected legacy weather input %q to be rejected", field)
		}
	}
}

func TestSplitTemperatureDisplayDropsCelsiusLetter(t *testing.T) {
	cases := []string{
		"30°C",
		"30°c",
		"30° C",
		"30 ℃",
		"30 C",
		"30c",
		"30 摄氏度",
		"30 celsius",
	}
	for _, input := range cases {
		number, unit := splitTemperatureDisplay(input)
		if number != "30" || unit != "°" {
			t.Fatalf("splitTemperatureDisplay(%q) = %q, %q; want 30, °", input, number, unit)
		}
	}
}

func TestWeatherForecastSlotsUseHoursOnly(t *testing.T) {
	slots := weatherForecastSlots(weatherCardData{
		Temperature: "35°C",
		Condition:   "Partly cloudy",
		UpdatedAt:   "2026-07-03 16:30",
		Hourly: []weatherForecastHour{
			{Time: "1700", Temp: "35°C", Condition: "Partly cloudy"},
			{Time: "1800", Temp: "32°C", Condition: "Cloudy"},
			{Time: "1900", Temp: "30°C", Condition: "Sunny"},
		},
		Forecast: []weatherForecastDay{
			{Date: "2026-07-04", MaxTemp: "36°C", Condition: "Rain"},
		},
	}, "partly")
	labels := []string{}
	for _, slot := range slots {
		labels = append(labels, slot.Label)
	}
	joined := strings.Join(labels, ",")
	if strings.Contains(joined, "明日") || strings.Contains(joined, "后天") {
		t.Fatalf("hourly slots must not include daily labels: %v", labels)
	}
	for _, want := range []string{"现在", "17时", "18时", "19时"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing hourly label %q in %v", want, labels)
		}
	}
}

func TestWeatherForecastSlotsFilterPastHours(t *testing.T) {
	slots := weatherForecastSlots(weatherCardData{
		Temperature: "35°C",
		Condition:   "Partly cloudy",
		UpdatedAt:   "2026-07-03 17:04",
		Hourly: []weatherForecastHour{
			{Time: "1700", Temp: "35°C", Condition: "Partly cloudy"},
			{Time: "1800", Temp: "32°C", Condition: "Cloudy"},
			{Time: "1900", Temp: "30°C", Condition: "Sunny"},
		},
	}, "partly")
	labels := []string{}
	for _, slot := range slots {
		labels = append(labels, slot.Label)
	}
	joined := strings.Join(labels, ",")
	if strings.Contains(joined, "17时") {
		t.Fatalf("past hourly label should be filtered using updated_at: %v", labels)
	}
	for _, want := range []string{"现在", "18时", "19时"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing hourly label %q in %v", want, labels)
		}
	}
}

func TestOpenMeteoHourlyFromDataUsesFutureHours(t *testing.T) {
	hours, ok := openMeteoHourlyFromData(map[string]any{
		"current": map[string]any{"time": "2026-07-03T18:00"},
		"hourly": map[string]any{
			"time":           []any{"2026-07-03T18:00", "2026-07-03T19:00", "2026-07-03T20:00"},
			"temperature_2m": []any{28.4, 27.9, 27.5},
			"weather_code":   []any{3.0, 3.0, 61.0},
		},
	})
	if !ok {
		t.Fatal("expected Open-Meteo hourly forecast")
	}
	if len(hours) != 2 {
		t.Fatalf("expected future hours only, got %#v", hours)
	}
	if hours[0].Time != "19:00" || hours[0].Temp != "28°C" || hours[0].Condition != "多云" {
		t.Fatalf("unexpected first future hour: %#v", hours[0])
	}
	if hours[1].Time != "20:00" || hours[1].Condition != "有雨" {
		t.Fatalf("unexpected second future hour: %#v", hours[1])
	}
}

func TestWeatherTempRangeHidesConflictingRange(t *testing.T) {
	low, high := weatherTempRange(weatherCardData{
		Temperature: "35°C",
		Forecast: []weatherForecastDay{
			{MinTemp: "24°C", MaxTemp: "31°C"},
		},
	})
	if low != "" || high != "" {
		t.Fatalf("conflicting range should be hidden, got %q/%q", low, high)
	}

	low, high = weatherTempRange(weatherCardData{
		Temperature: "28°C",
		Forecast: []weatherForecastDay{
			{MinTemp: "24°C", MaxTemp: "35°C"},
		},
	})
	if low != "24°" || high != "35°" {
		t.Fatalf("valid range should render, got %q/%q", low, high)
	}
}

func TestWeatherReferenceMinuteUsesBeijingTime(t *testing.T) {
	minute, ok := weatherReferenceMinute("2026-07-03T09:04:00Z")
	if !ok {
		t.Fatal("expected RFC3339 updated_at to parse")
	}
	if minute != 17*60+4 {
		t.Fatalf("weatherReferenceMinute RFC3339 = %d; want Beijing 17:04", minute)
	}

	if got := displayUpdateTime("2026-07-03T09:04:00Z"); got != "17:04" {
		t.Fatalf("displayUpdateTime RFC3339 = %q; want Beijing 17:04", got)
	}
}

func TestToolHubUsesSessionWorkspaceRoot(t *testing.T) {
	globalRoot := t.TempDir()
	userA := filepath.Join(globalRoot, "users", "a")
	userB := filepath.Join(globalRoot, "users", "b")
	if err := os.MkdirAll(userA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(userB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userA, "note.txt"), []byte("alpha workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userB, "note.txt"), []byte("beta workspace"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = globalRoot
	cfg.Workspaces.Allowlist = []string{globalRoot}
	st := store.NewMemoryStore()
	sessionA := st.CreateSessionWithScope("A", "owner-a", userA, "weixin", true)
	sessionB := st.CreateSessionWithScope("B", "owner-b", userB, "weixin", true)
	hub := New(cfg, st)

	read := func(sessionID string) string {
		t.Helper()
		result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "note.txt"}, sessionID, "run")
		if err != nil {
			t.Fatal(err)
		}
		output, _ := result.Output.(map[string]any)
		content, _ := output["content"].(string)
		return content
	}
	if got := read(sessionA.ID); !strings.Contains(got, "alpha workspace") {
		t.Fatalf("session A read wrong workspace content: %q", got)
	}
	if got := read(sessionB.ID); !strings.Contains(got, "beta workspace") {
		t.Fatalf("session B read wrong workspace content: %q", got)
	}
}

func TestImagesInspectResizesLargeImagesBeforeModelCall(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "large.jpg")
	if err := writeTestJPEG(path, 1200, 3600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Model.Mock = true
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "images.inspect", map[string]any{
		"path":     "large.jpg",
		"question": "这张图片是什么？",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	output, ok := result.Output.(map[string]any)
	if !ok {
		t.Fatalf("unexpected output type %T", result.Output)
	}
	if output["resized"] != true {
		t.Fatalf("expected large image to be resized: %#v", output)
	}
	if output["width"] != 1200 || output["height"] != 3600 {
		t.Fatalf("expected original dimensions to be preserved, got %#v x %#v", output["width"], output["height"])
	}
	if output["model_width"] != 800 || output["model_height"] != 2400 {
		t.Fatalf("expected model dimensions to fit 2400 long edge, got %#v x %#v", output["model_width"], output["model_height"])
	}
	if output["model_content_type"] != "image/jpeg" {
		t.Fatalf("expected resized model input to be jpeg, got %#v", output["model_content_type"])
	}
	if output["fallback_policy"] != "" {
		t.Fatalf("normal resize should not be marked as fallback: %#v", output["fallback_policy"])
	}
}

func TestPrepareImageForModelMarksOriginalSendFallback(t *testing.T) {
	prepared, err := prepareImageForModel([]byte("not actually a png"), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.FallbackPolicy != "image.inspect_dimension_decode_failed_original_sent" {
		t.Fatalf("expected visible fallback policy, got %#v", prepared)
	}
	if !strings.Contains(prepared.ResizeNote, "sent original bytes") {
		t.Fatalf("expected resize note to explain fallback: %#v", prepared.ResizeNote)
	}
}

func writeTestJPEG(path string, width, height int) error {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 180, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return jpeg.Encode(file, img, &jpeg.Options{Quality: 85})
}

func TestValidateInputAllowsVerifierMetadataForApprovalArguments(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())

	err := hub.Validate("shell.exec_sandboxed", map[string]any{
		"command": "ls -la",
		"_verifier": app.VerifierDecision{
			Verdict: "ask_user",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDefaultDefinitionsExposeOutputSchemas(t *testing.T) {
	hub := New(config.Default(), store.NewMemoryStore())
	defs := hub.Definitions()
	if len(defs) == 0 {
		t.Fatal("expected default definitions")
	}
	for _, def := range defs {
		if len(def.InputSchema) == 0 {
			t.Fatalf("%s missing input schema", def.Name)
		}
		if len(def.OutputSchema) == 0 {
			t.Fatalf("%s missing output schema", def.Name)
		}
		if def.Risk == "" || def.TimeoutMS <= 0 || def.Sandbox == "" || def.Audit == "" {
			t.Fatalf("%s missing required contract metadata: %#v", def.Name, def)
		}
	}
}

func TestValidateOutputNormalizesStructResults(t *testing.T) {
	def := app.ToolDefinition{
		Name: "memory.write_candidate",
		OutputSchema: objectSchema([]string{"id", "created_at"}, map[string]any{
			"id":         stringSchema(),
			"created_at": stringSchema(),
		}),
	}

	err := validateOutput(def, app.MemoryCandidate{
		ID:        "mem_test",
		CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestValidateOutputRejectsContractMismatch(t *testing.T) {
	def := app.ToolDefinition{
		Name: "files.read",
		OutputSchema: objectSchema([]string{"bytes"}, map[string]any{
			"bytes": integerSchema(),
		}),
	}

	err := validateOutput(def, map[string]any{"bytes": "not-a-number"})
	if err == nil || !strings.Contains(err.Error(), "files.read output schema violation") {
		t.Fatalf("expected output schema violation, got %v", err)
	}
}

func TestExecuteValidatesFilesReadOutput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	if err := os.WriteFile(path, []byte("stable output contract"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": path}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["content"] != "stable output contract" {
		t.Fatalf("unexpected output: %#v", out)
	}
}

func TestFilesReadReturnsFullTextUntilMaxBytes(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 520)
	for i := range lines {
		lines[i] = fmt.Sprintf("line-%03d", i+1)
	}
	if err := os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	first, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "large.txt"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	firstOut := first.Output.(map[string]any)
	firstContent := firstOut["content"].(string)
	if firstOut["truncated"] != false || !strings.Contains(firstContent, "line-001") || !strings.Contains(firstContent, "line-520") {
		t.Fatalf("expected full small text read, got %#v", firstOut)
	}
	textDocument := firstOut["document"].(map[string]any)
	if textDocument["schema_version"] != "document_read_v1" || textDocument["format"] != "text" {
		t.Fatalf("text read should use unified document envelope: %#v", textDocument)
	}
	textStrategy := textDocument["strategy"].(map[string]any)
	if textStrategy["mode"] != "full" || textStrategy["complete"] != true {
		t.Fatalf("text read should report full strategy: %#v", textStrategy)
	}
	textPipeline := textDocument["pipeline"].(map[string]any)
	textPipelineStrategy := textPipeline["strategy"].(map[string]any)
	if textPipeline["status"] != "succeeded" || textPipelineStrategy["strategy"] != "small_direct" || textPipelineStrategy["context_mode"] != "full_text" {
		t.Fatalf("text read should enter the small-document pipeline: %#v", textPipeline)
	}
	textIndex := textPipeline["index"].(map[string]any)
	if textIndex["index_status"] != "skipped" {
		t.Fatalf("small text read should skip retrieval index: %#v", textIndex)
	}
	_, err = hub.Execute(context.Background(), "files.read", map[string]any{
		"path":      "large.txt",
		"max_bytes": 80,
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeStrategyDeferred) || !strings.Contains(err.Error(), "limit=80") {
		t.Fatalf("limited small-file read must defer instead of truncating, got %v", err)
	}
}

func TestFilesReadReturnsFullDocxWithLocations(t *testing.T) {
	root := t.TempDir()
	lines := make([]string, 12)
	for i := range lines {
		lines[i] = fmt.Sprintf("docx-line-%03d", i+1)
	}
	writeDocxFixture(t, root, "large.docx", strings.Join(lines, "\n"))
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	first, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "large.docx"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	firstOut := first.Output.(map[string]any)
	firstContent := firstOut["content"].(string)
	if firstOut["kind"] != "docx" || firstOut["truncated"] != false || !strings.Contains(firstContent, "docx-line-001") || !strings.Contains(firstContent, "docx-line-012") {
		t.Fatalf("unexpected docx full-read output: %#v", firstOut)
	}
	if _, ok := firstOut["document"].(map[string]any); !ok {
		t.Fatalf("docx read should preserve structured document payload: %#v", firstOut)
	}
	firstDocument := firstOut["document"].(map[string]any)
	if firstDocument["schema_version"] != "document_read_v1" || firstDocument["source"] != "python_docx" {
		t.Fatalf("docx should use unified document schema: %#v", firstDocument)
	}
	strategy := firstDocument["strategy"].(map[string]any)
	if strategy["mode"] != "full" || strategy["complete"] != true {
		t.Fatalf("docx should use unified full-read strategy metadata: %#v", strategy)
	}
	pipeline := firstDocument["pipeline"].(map[string]any)
	pipelineStrategy := pipeline["strategy"].(map[string]any)
	if pipeline["status"] != "succeeded" || pipelineStrategy["strategy"] != "small_direct" || pipelineStrategy["context_mode"] != "full_text" {
		t.Fatalf("docx should enter the small-document pipeline: %#v", pipeline)
	}
	paragraphs := firstDocument["paragraphs"].([]any)
	if len(paragraphs) != 12 {
		t.Fatalf("expected all docx paragraphs, got %#v", firstDocument)
	}
	evidenceBlocks := testAnySlice(firstDocument["evidence_blocks"])
	if len(evidenceBlocks) != 12 {
		t.Fatalf("expected docx evidence blocks, got %#v", firstDocument)
	}
	firstBlock := evidenceBlocks[0].(map[string]any)
	if firstBlock["blockId"] != "document.p[1]" || firstBlock["documentId"] != "large.docx" || firstBlock["fileType"] != "docx" || firstBlock["sourceHash"] == "" {
		t.Fatalf("unexpected evidence block identity: %#v", firstBlock)
	}
	location := firstBlock["location"].(map[string]any)
	if intArg(location, "paragraphIndex", 0) != 1 {
		t.Fatalf("evidence block should expose normalized paragraphIndex: %#v", firstBlock)
	}
}

func TestFilesReadDocxLocationDistinguishesTableCells(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
doc = Document()
doc.add_paragraph("Before table")
table = doc.add_table(rows=1, cols=2)
table.cell(0, 0).text = "Cell A"
table.cell(0, 1).text = "Cell B"
doc.add_paragraph("After table")
doc.save(root / "table.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create table docx fixture: %v\n%s", err, out)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "table.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	document := out["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	var tableLocation map[string]any
	for _, value := range blocks {
		block := value.(map[string]any)
		if block["text"] == "Cell A" {
			tableLocation = block["location"].(map[string]any)
			break
		}
	}
	if tableLocation == nil {
		t.Fatalf("missing table cell block in docx read output: %#v", document)
	}
	if tableLocation["block_type"] != "table_cell" ||
		intArg(tableLocation, "paragraph_index", -1) != 0 ||
		intArg(tableLocation, "table_index", 0) != 1 ||
		intArg(tableLocation, "row_index", 0) != 1 ||
		intArg(tableLocation, "cell_index", 0) != 1 {
		t.Fatalf("unexpected table cell location: %#v", tableLocation)
	}
	evidenceBlocks := testAnySlice(document["evidence_blocks"])
	foundCellAnchor := false
	for _, value := range evidenceBlocks {
		block := value.(map[string]any)
		if block["text"] != "Cell A" {
			continue
		}
		foundCellAnchor = true
		if block["type"] != "table_cell" {
			t.Fatalf("table cell evidence block should keep type: %#v", block)
		}
		location := block["location"].(map[string]any)
		if location["tableId"] != "table_1" || intArg(location, "rowIndex", 0) != 1 || intArg(location, "columnIndex", 0) != 1 {
			t.Fatalf("table cell evidence block should normalize location: %#v", block)
		}
	}
	if !foundCellAnchor {
		t.Fatalf("missing table cell evidence block: %#v", evidenceBlocks)
	}
}

func TestDocxParagraphToolsAcceptReadLocation(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "First paragraph\nSecond paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read, err := hub.Execute(context.Background(), "files.read", map[string]any{
		"path": "note.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	readOut := read.Output.(map[string]any)
	document := readOut["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	if len(blocks) < 2 {
		t.Fatalf("expected full read blocks, got %#v", document)
	}
	location := blocks[1].(map[string]any)["location"].(map[string]any)

	result, err := hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path":        "note.docx",
		"location":    location,
		"old_text":    "Second paragraph",
		"source_hash": sourceHash("Second paragraph"),
		"text":        "Replaced by location",
		"output_path": "outputs/location-replaced.docx",
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "paragraph_index", 0) != 2 {
		t.Fatalf("location should resolve paragraph 2: %#v", out)
	}
	edited, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": out["output_path"]}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	content := edited.Output.(map[string]any)["content"].(string)
	if !strings.Contains(content, "Replaced by location") || strings.Contains(content, "Second paragraph") {
		t.Fatalf("location replacement did not apply to expected paragraph: %q", content)
	}

	_, err = hub.Execute(context.Background(), "docx.replace_paragraph", map[string]any{
		"path":        "note.docx",
		"location":    location,
		"old_text":    "Wrong paragraph",
		"text":        "Should not be written",
		"output_path": "outputs/location-mismatch.docx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "old_text mismatch") {
		t.Fatalf("expected old_text preflight mismatch, got %v", err)
	}
}

func TestDocxParagraphToolsRejectTableCellLocation(t *testing.T) {
	root := t.TempDir()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
doc = Document()
table = doc.add_table(rows=1, cols=1)
table.cell(0, 0).text = "Cell A"
doc.save(root / "table.docx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create table docx fixture: %v\n%s", err, out)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": "table.docx"}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	document := read.Output.(map[string]any)["document"].(map[string]any)
	blocks := document["blocks"].([]any)
	location := blocks[0].(map[string]any)["location"].(map[string]any)
	_, err = hub.Execute(context.Background(), "docx.delete_paragraph", map[string]any{
		"path":        "table.docx",
		"location":    location,
		"output_path": "outputs/deleted.docx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "only top-level paragraph locations are currently editable") {
		t.Fatalf("expected table cell location rejection, got %v", err)
	}
}

func TestFilesReadExtractsStructuredOfficeDocuments(t *testing.T) {
	root := t.TempDir()
	writeStructuredOfficeFixtures(t, root)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := map[string][]string{
		"note.docx":   {"Docx alpha", "Docx beta"},
		"slides.pptx": {"Slide title"},
		"book.xlsx":   {"Header", "Cell value"},
	}
	for name, wants := range cases {
		result, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": name}, "s", "run")
		if err != nil {
			t.Fatal(err)
		}
		out := result.Output.(map[string]any)
		content := out["content"].(string)
		if out["kind"] != strings.TrimPrefix(filepath.Ext(name), ".") {
			t.Fatalf("%s kind mismatch: %#v", name, out)
		}
		if _, ok := out["document"].(map[string]any); !ok {
			t.Fatalf("%s missing structured document payload: %#v", name, out)
		}
		for _, want := range wants {
			if !strings.Contains(content, want) {
				t.Fatalf("%s content missing %q: %q", name, want, content)
			}
		}
		if out["untrusted"] != true {
			t.Fatalf("%s should remain untrusted: %#v", name, out)
		}
	}
}

func writeStructuredOfficeFixtures(t *testing.T, root string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from docx import Document
from pptx import Presentation
from pptx.util import Inches
root = Path(__import__("sys").argv[1])
doc = Document()
doc.add_paragraph("Docx alpha")
doc.add_paragraph("Docx beta")
doc.save(root / "note.docx")
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[5])
slide.shapes.title.text = "Slide title"
box = slide.shapes.add_textbox(Inches(1), Inches(1.5), Inches(6), Inches(1))
box.text = "Slide body"
prs.save(root / "slides.pptx")
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create office fixtures with python adapters: %v\n%s", err, out)
	}
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Sheet One");
  sheet.addRow(["Header", "Cell value"]);
  await workbook.xlsx.writeFile(root + "/book.xlsx");
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});
`
	cmd = exec.Command(documentNodeBinary(), "-e", nodeScript, root)
	cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create xlsx fixture with exceljs: %v\n%s", err, out)
	}
}

func TestOfficeReplaceTextRequiresMappedLibrary(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Replace Alpha in this document.")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path":        "note.docx",
		"output_path": "outputs/note.edited.docx",
		"replacements": []any{
			map[string]any{"find": "Alpha", "replace": "Beta"},
		},
		"expected_replacements": 1,
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if out["replacements"] != 1 {
		t.Fatalf("unexpected replace output: %#v", out)
	}
}

func TestDocxParagraphToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "First paragraph\nSecond paragraph\nThird paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{
			tool: "docx.replace_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"paragraph_index": 2,
				"old_text":        "Second paragraph",
				"text":            "Replaced second paragraph",
				"output_path":     "outputs/replaced.docx",
			},
			want: "Replaced second paragraph",
		},
		{
			tool: "docx.insert_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"paragraph_index": 1,
				"position":        "after",
				"text":            "Inserted after first",
				"output_path":     "outputs/inserted.docx",
			},
			want: "Inserted after first",
		},
		{
			tool: "docx.delete_paragraph",
			args: map[string]any{
				"path":            "note.docx",
				"paragraph_index": 2,
				"output_path":     "outputs/deleted.docx",
			},
			want: "First paragraph",
		},
		{
			tool: "docx.set_text_style",
			args: map[string]any{
				"path":            "note.docx",
				"paragraph_index": 1,
				"style":           map[string]any{"builtin_style": "Heading 1", "bold": true, "font_size_pt": 18},
				"output_path":     "outputs/styled.docx",
			},
			want: "First paragraph",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), tc.tool, tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "note.docx") {
				t.Fatalf("tool overwrote input: %#v", out)
			}
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": outputPath}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			content := read.Output.(map[string]any)["content"].(string)
			if !strings.Contains(content, tc.want) {
				t.Fatalf("edited docx missing %q: %q", tc.want, content)
			}
		})
	}
}

func TestDocxParagraphToolRejectsOutOfRangeParagraph(t *testing.T) {
	root := t.TempDir()
	writeDocxFixture(t, root, "note.docx", "Only paragraph")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "docx.delete_paragraph", map[string]any{
		"path":            "note.docx",
		"paragraph_index": 99,
		"output_path":     "outputs/deleted.docx",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed paragraph target error, got %v", err)
	}
}

func TestPptxSlideToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		tool       string
		args       map[string]any
		wantSlides int
		wantText   string
		wantTitles []string
	}{
		{
			tool: "pptx.add_slide",
			args: map[string]any{
				"path":         "deck.pptx",
				"layout_index": 1,
				"title":        "Added slide",
				"body":         "Added body",
				"output_path":  "outputs/added.pptx",
			},
			wantSlides: 3,
			wantText:   "Added slide",
		},
		{
			tool: "pptx.update_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 2,
				"updates": []any{map[string]any{
					"shape_index": 2,
					"old_text":    "Second body",
					"text":        "Expanded second body",
				}},
				"output_path": "outputs/updated.pptx",
			},
			wantSlides: 2,
			wantText:   "Expanded second body",
		},
		{
			tool: "pptx.duplicate_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 1,
				"output_path": "outputs/duplicated.pptx",
			},
			wantSlides: 3,
			wantText:   "First slide",
			wantTitles: []string{"First slide", "First slide", "Second slide"},
		},
		{
			tool: "pptx.delete_slide",
			args: map[string]any{
				"path":        "deck.pptx",
				"slide_index": 2,
				"output_path": "outputs/deleted.pptx",
			},
			wantSlides: 1,
			wantText:   "First slide",
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), tc.tool, tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "deck.pptx") {
				t.Fatalf("tool overwrote input: %#v", out)
			}
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": outputPath}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			readOut := read.Output.(map[string]any)
			content := readOut["content"].(string)
			document := readOut["document"].(map[string]any)
			slides := document["slides"].([]any)
			if len(slides) != tc.wantSlides {
				t.Fatalf("expected %d slides, got %#v", tc.wantSlides, document)
			}
			if !strings.Contains(content, tc.wantText) {
				t.Fatalf("edited pptx missing %q: %q", tc.wantText, content)
			}
			if len(tc.wantTitles) > 0 {
				gotTitles := pptxSlideTitles(document)
				if !slicesEqual(gotTitles, tc.wantTitles) {
					t.Fatalf("unexpected slide order, got %#v want %#v", gotTitles, tc.wantTitles)
				}
			}
		})
	}
}

func TestPptxUpdateSlideRejectsStaleShapeEvidence(t *testing.T) {
	root := t.TempDir()
	writePptxFixture(t, root, "deck.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "deck.pptx", "slide_index": 2, "output_path": "outputs/stale.pptx",
		"updates": []any{map[string]any{"shape_index": 2, "old_text": "Invented body", "text": "Updated body"}},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "old_text does not match slide shape 2") {
		t.Fatalf("expected stale PPTX shape evidence error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "stale.pptx")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed PPTX update left an output copy: %v", statErr)
	}
}

func TestPptxUpdateSlideExpandsLongTextWithoutShrinkingFont(t *testing.T) {
	root := t.TempDir()
	writePptxSingleLineFixture(t, root, "single-line.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "single-line.pptx", "slide_index": 1, "output_path": "outputs/fitted.pptx",
		"updates": []any{map[string]any{
			"shape_index": 1,
			"old_text":    "应用层协议",
			"text":        "HTTP、DNS、SMTP、FTP；处理用户可见的数据格式与交互逻辑。",
		}},
	}, "s", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "fitted_shapes", -1) != 0 || intArg(out, "layout_adjusted_shapes", 0) != 1 || stringArg(out, "layout_policy", "") != "coordinated" {
		t.Fatalf("long single-line text was not safely expanded: %#v", out)
	}
	outputPath := stringArg(out, "output_path", "")
	pythonScript := `
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
shape = prs.slides[0].shapes[0]
tf = shape.text_frame
print(tf.paragraphs[0].runs[0].font.size.pt, tf.word_wrap, shape.width)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, outputPath)
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect fitted PPTX: %v\n%s", err, inspection)
	}
	var size float64
	var wordWrap string
	var width int
	if _, err := fmt.Sscan(string(inspection), &size, &wordWrap, &width); err != nil {
		t.Fatalf("parse fitted PPTX inspection %q: %v", inspection, err)
	}
	if size != 18 || wordWrap != "False" || width <= 6*914400 {
		t.Fatalf("unexpected expanded text properties: size=%v word_wrap=%s width=%d", size, wordWrap, width)
	}
}

func TestPptxUpdateSlideCoordinatesPeerBandsAndReportsLayoutChecks(t *testing.T) {
	root := t.TempDir()
	writePptxBandFixture(t, root, "bands.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	result, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "bands.pptx", "slide_index": 1, "layout_policy": "coordinated", "output_path": "outputs/bands.pptx",
		"updates": []any{
			map[string]any{"shape_index": 3, "old_text": "读取内容", "text": "完整读取演示文稿内容并保留结构证据"},
			map[string]any{"shape_index": 6, "old_text": "定位内容", "text": "使用稳定的页面与形状索引定位修改目标"},
			map[string]any{"shape_index": 9, "old_text": "修改内容", "text": "生成新版本并校验原始文件保持不变"},
		},
	}, "session", "run")
	if err != nil {
		t.Fatal(err)
	}
	out := result.Output.(map[string]any)
	if intArg(out, "layout_adjusted_shapes", 0) != 6 || intArg(out, "companion_groups_used", 0) != 3 {
		t.Fatalf("peer band layout was not coordinated: %#v", out)
	}
	checks := out["layout_checks"].(map[string]any)
	if checks["updated_text_fits"] != true || checks["canvas_bounds"] != true || checks["companion_non_overlap"] != true || checks["peer_font_uniform"] != true {
		t.Fatalf("layout checks are incomplete: %#v", checks)
	}
	summary := out["change_summary"].(map[string]any)
	if summary["layout_policy"] != "coordinated" || intArg(summary, "layout_adjusted_shapes", 0) != 6 || summary["original_unchanged"] != true {
		t.Fatalf("change summary omitted coordinated layout evidence: %#v", summary)
	}
	if len(documentAnySlice(summary["preservation_warnings"])) == 0 {
		t.Fatalf("page marker warning was not surfaced in change_summary: %#v", summary)
	}
	outputPath := stringArg(out, "output_path", "")
	pythonScript := `
from pptx import Presentation
prs = Presentation(__import__("sys").argv[1])
slide = prs.slides[0]
for background_index, body_index in ((0, 2), (3, 5), (6, 8)):
    background = slide.shapes[background_index]
    body = slide.shapes[body_index]
    size = body.text_frame.paragraphs[0].runs[0].font.size.pt
    print(background.left + background.width, body.left, body.width, size, body.text_frame.word_wrap)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, outputPath)
	inspection, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect coordinated PPTX: %v\n%s", err, inspection)
	}
	lines := strings.Split(strings.TrimSpace(string(inspection)), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected coordinated inspection: %q", inspection)
	}
	width := 0
	for _, line := range lines {
		var backgroundRight, bodyLeft, bodyWidth int
		var size float64
		var wordWrap string
		if _, err := fmt.Sscan(line, &backgroundRight, &bodyLeft, &bodyWidth, &size, &wordWrap); err != nil {
			t.Fatalf("parse coordinated inspection %q: %v", line, err)
		}
		if backgroundRight > bodyLeft || size != 16.5 || wordWrap != "False" || (width != 0 && width != bodyWidth) {
			t.Fatalf("peer band geometry or typography is inconsistent: %q", line)
		}
		width = bodyWidth
	}
}

func TestPptxUpdateSlideRejectsUnreadablyLongText(t *testing.T) {
	root := t.TempDir()
	writePptxSingleLineFixture(t, root, "single-line.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.update_slide", map[string]any{
		"path": "single-line.pptx", "slide_index": 1, "output_path": "outputs/unreadable.pptx",
		"updates": []any{map[string]any{
			"shape_index": 1,
			"old_text":    "应用层协议",
			"text":        strings.Repeat("过长内容", 50),
		}},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "updated text is too long for its slide shape") {
		t.Fatalf("expected unreadable text rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "outputs", "unreadable.pptx")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unreadable PPTX update left an output copy: %v", statErr)
	}
}

func pptxSlideTitles(document map[string]any) []string {
	slides, _ := document["slides"].([]any)
	out := []string{}
	for _, slideValue := range slides {
		slide, _ := slideValue.(map[string]any)
		items, _ := slide["items"].([]any)
		title := ""
		for _, itemValue := range items {
			item, _ := itemValue.(map[string]any)
			if item["type"] == "text" {
				title = fmt.Sprint(item["text"])
				break
			}
		}
		out = append(out, title)
	}
	return out
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPptxDeleteSlideRejectsOnlySlide(t *testing.T) {
	root := t.TempDir()
	writeSingleSlidePptxFixture(t, root, "single.pptx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pptx.delete_slide", map[string]any{
		"path":        "single.pptx",
		"slide_index": 1,
		"output_path": "outputs/deleted.pptx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "cannot delete the only slide") {
		t.Fatalf("expected only-slide error, got %v", err)
	}
}

func TestXlsxStructureToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		tool     string
		args     map[string]any
		contains []string
		wantRow  int
	}{
		{
			tool: "xlsx.update_cell",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"cell":        "B2",
				"value":       99,
				"output_path": "outputs/cell.xlsx",
			},
			contains: []string{"99"},
		},
		{
			tool: "xlsx.insert_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"position":    "after",
				"values":      []any{"Inserted", 77, "New"},
				"output_path": "outputs/inserted.xlsx",
			},
			contains: []string{"Inserted", "77", "New"},
		},
		{
			tool: "xlsx.delete_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"output_path": "outputs/deleted.xlsx",
			},
			contains: []string{"Bob", "92"},
		},
		{
			tool: "xlsx.update_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"row":         2,
				"values":      []any{"Updated", 66, "Changed"},
				"output_path": "outputs/row.xlsx",
			},
			contains: []string{"Updated", "66", "Changed"},
		},
		{
			tool: "xlsx.append_row",
			args: map[string]any{
				"path":        "book.xlsx",
				"sheet":       "Sheet1",
				"values":      []any{"Appended", 55, "Done"},
				"output_path": "outputs/appended.xlsx",
			},
			contains: []string{"Appended", "55", "Done"},
			wantRow:  4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), tc.tool, tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			if tc.wantRow > 0 && intArg(out, "row", 0) != tc.wantRow {
				t.Fatalf("appended XLSX row ignored the structured content boundary: got=%#v want=%d", out["row"], tc.wantRow)
			}
			outputPath := out["output_path"].(string)
			if outputPath == filepath.Join(root, "book.xlsx") {
				t.Fatalf("tool overwrote input: %#v", out)
			}
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
			read, err := hub.Execute(context.Background(), "files.read", map[string]any{"path": outputPath}, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			content := read.Output.(map[string]any)["content"].(string)
			for _, want := range tc.contains {
				if !strings.Contains(content, want) {
					t.Fatalf("edited xlsx missing %q: %q", want, content)
				}
			}
		})
	}
}

func TestXlsxStructureToolRejectsMissingSheet(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "xlsx.update_cell", map[string]any{
		"path":        "book.xlsx",
		"sheet":       "Missing",
		"cell":        "A1",
		"value":       "x",
		"output_path": "outputs/missing.xlsx",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed missing-sheet target error, got %v", err)
	}
}

func TestXlsxStructureToolRejectsInvalidCell(t *testing.T) {
	root := t.TempDir()
	writeXlsxFixture(t, root, "book.xlsx")
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "xlsx.update_cell", map[string]any{
		"path":        "book.xlsx",
		"sheet":       "Sheet1",
		"cell":        "bad",
		"value":       "x",
		"output_path": "outputs/bad.xlsx",
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "cell must be a valid A1 address") {
		t.Fatalf("expected invalid cell error, got %v", err)
	}
}

func TestPDFTransformToolsWriteNewVersions(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "first.pdf", 3)
	writePDFBlankFixture(t, root, "second.pdf", 2)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	cases := []struct {
		name      string
		args      map[string]any
		wantPages int
	}{
		{
			name: "extract_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "extract_pages",
				"pages":       []any{1, 3},
				"output_path": "outputs/extracted.pdf",
			},
			wantPages: 2,
		},
		{
			name: "delete_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "delete_pages",
				"pages":       []any{2},
				"output_path": "outputs/deleted.pdf",
			},
			wantPages: 2,
		},
		{
			name: "rotate_pages",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "rotate_pages",
				"pages":       []any{1},
				"rotation":    90,
				"output_path": "outputs/rotated.pdf",
			},
			wantPages: 3,
		},
		{
			name: "merge",
			args: map[string]any{
				"operation":   "merge",
				"inputs":      []any{"first.pdf", "second.pdf"},
				"output_path": "outputs/merged.pdf",
			},
			wantPages: 5,
		},
		{
			name: "split",
			args: map[string]any{
				"path":        "first.pdf",
				"operation":   "split",
				"output_path": "outputs/split.pdf",
			},
			wantPages: 3,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := hub.Execute(context.Background(), "pdf.transform", tc.args, "s", "run")
			if err != nil {
				t.Fatal(err)
			}
			out := result.Output.(map[string]any)
			if out["pages"] != tc.wantPages {
				t.Fatalf("unexpected pages for %s: %#v", tc.name, out)
			}
			if tc.name == "split" {
				outputs := out["outputs"].([]string)
				if len(outputs) != tc.wantPages {
					t.Fatalf("split should return one output per page: %#v", out)
				}
				for _, path := range outputs {
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("split output missing: %v", err)
					}
				}
				if out["output_path"] != outputs[0] {
					t.Fatalf("split primary output must name an existing typed resource: %#v", out)
				}
				return
			}
			outputPath := out["output_path"].(string)
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("expected output file: %v", err)
			}
		})
	}
}

func TestPDFTransformRejectsOutOfRangePage(t *testing.T) {
	root := t.TempDir()
	writePDFBlankFixture(t, root, "first.pdf", 1)
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "pdf.transform", map[string]any{
		"path":        "first.pdf",
		"operation":   "extract_pages",
		"pages":       []any{2},
		"output_path": "outputs/bad.pdf",
	}, "s", "run")
	if !document.IsErrorCode(err, document.CodeTargetNotFound) {
		t.Fatalf("expected typed page target error, got %v", err)
	}
}

func TestResolvePathAcceptsAllowedMacAbsolutePathMissingLeadingSlash(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	missingSlash := strings.TrimPrefix(filepath.Join(root, "note.txt"), string(os.PathSeparator))
	got, err := hub.resolvePath(missingSlash)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "note.txt") {
		t.Fatalf("unexpected normalized path: %q", got)
	}
}

func writeDocxFixture(t *testing.T, root, name, text string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from docx import Document
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
text = __import__("sys").argv[3]
doc = Document()
for part in text.split("\n"):
    doc.add_paragraph(part)
doc.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, text)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create docx fixture: %v\n%s", err, out)
	}
}

func writePptxFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide1 = prs.slides.add_slide(prs.slide_layouts[1])
slide1.shapes.title.text = "First slide"
slide1.placeholders[1].text = "First body"
slide2 = prs.slides.add_slide(prs.slide_layouts[1])
slide2.shapes.title.text = "Second slide"
slide2.placeholders[1].text = "Second body"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create pptx fixture: %v\n%s", err, out)
	}
}

func writePptxSingleLineFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
shape = slide.shapes.add_textbox(Inches(1), Inches(1), Inches(6), Inches(0.35))
run = shape.text_frame.paragraphs[0].add_run()
run.text = "应用层协议"
run.font.size = Pt(18)
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create single-line pptx fixture: %v\n%s", err, out)
	}
}

func writePptxBandFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
from pptx.dml.color import RGBColor
from pptx.enum.shapes import MSO_AUTO_SHAPE_TYPE
from pptx.util import Inches, Pt
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[6])
rows = ((2.0, "读取", "读取内容", (22, 101, 52)), (3.0, "定位", "定位内容", (3, 105, 161)), (4.0, "修改", "修改内容", (180, 83, 9)))
for top, label_text, body_text, color in rows:
    band = slide.shapes.add_shape(MSO_AUTO_SHAPE_TYPE.RECTANGLE, Inches(1.5), Inches(top), Inches(4.5), Inches(.6))
    band.fill.solid()
    band.fill.fore_color.rgb = RGBColor(*color)
    band.line.fill.background()
    label = slide.shapes.add_textbox(Inches(1.7), Inches(top + .08), Inches(1.2), Inches(.35))
    label_run = label.text_frame.paragraphs[0].add_run()
    label_run.text = label_text
    label_run.font.size = Pt(16.5)
    body = slide.shapes.add_textbox(Inches(3.2), Inches(top + .08), Inches(5), Inches(.35))
    body_run = body.text_frame.paragraphs[0].add_run()
    body_run.text = body_text
    body_run.font.size = Pt(16.5)
marker = slide.shapes.add_textbox(Inches(8.5), Inches(6.8), Inches(1), Inches(.3))
marker.text_frame.paragraphs[0].add_run().text = "课程 · 2/4"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create band pptx fixture: %v\n%s", err, out)
	}
}

func writeXlsxFixture(t *testing.T, root, name string) {
	t.Helper()
	nodeScript := `
const ExcelJS = require("exceljs");
(async () => {
  const root = process.argv[1];
  const name = process.argv[2];
  const workbook = new ExcelJS.Workbook();
  const sheet = workbook.addWorksheet("Sheet1");
  sheet.addRow(["Name", "Score", "Status"]);
  sheet.addRow(["Alice", 88, "Ready"]);
  sheet.addRow(["Bob", 92, "Done"]);
  sheet.getCell("B10").font = { italic: true };
  await workbook.xlsx.writeFile(root + "/" + name);
})().catch(error => {
  console.error(error && error.stack || error);
  process.exit(1);
});
`
	cmd := exec.Command(documentNodeBinary(), "-e", nodeScript, root, name)
	cmd.Env = append(os.Environ(), "NODE_PATH="+documentNodeModulesPath())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create xlsx fixture: %v\n%s", err, out)
	}
}

func writePDFBlankFixture(t *testing.T, root, name string, pages int) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pypdf import PdfWriter
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
pages = int(__import__("sys").argv[3])
writer = PdfWriter()
for _ in range(pages):
    writer.add_blank_page(width=200, height=200)
with open(root / name, "wb") as f:
    writer.write(f)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name, fmt.Sprint(pages))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create pdf fixture: %v\n%s", err, out)
	}
}

func writeSingleSlidePptxFixture(t *testing.T, root, name string) {
	t.Helper()
	pythonScript := `
from pathlib import Path
from pptx import Presentation
root = Path(__import__("sys").argv[1])
name = __import__("sys").argv[2]
prs = Presentation()
slide = prs.slides.add_slide(prs.slide_layouts[1])
slide.shapes.title.text = "Only slide"
slide.placeholders[1].text = "Only body"
prs.save(root / name)
`
	cmd := exec.Command(documentPythonBinary(), "-c", pythonScript, root, name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create single pptx fixture: %v\n%s", err, out)
	}
}

func TestOfficeReplaceTextRejectsEscapingOutputPath(t *testing.T) {
	root := t.TempDir()
	if err := writeZipFile(filepath.Join(root, "note.docx"), map[string]string{
		"word/document.xml": `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>No match here.</w:t></w:r></w:p></w:body></w:document>`,
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspaces.DefaultRoot = root
	cfg.Workspaces.Allowlist = []string{root}
	hub := New(cfg, store.NewMemoryStore())

	_, err := hub.Execute(context.Background(), "office.replace_text", map[string]any{
		"path":        "note.docx",
		"output_path": "../note.edited.docx",
		"replacements": []any{
			map[string]any{"find": "missing", "replace": "new"},
		},
	}, "s", "run")
	if err == nil || !strings.Contains(err.Error(), "cannot escape workspace") {
		t.Fatalf("expected escaping output path error, got %v", err)
	}
}

func writeZipFile(path string, entries map[string]string) error {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func testAnySlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case []map[string]any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}
