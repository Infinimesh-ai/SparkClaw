package emailmanagement

import (
	"context"
	"encoding/json"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

type semanticSample struct {
	ID               string `json:"id"`
	Group            string `json:"group"`
	Language         string `json:"language"`
	Provider         string `json:"provider"`
	Body             string `json:"body"`
	ExpectedCategory string `json:"expected_category"`
	ExpectedCode     string `json:"expected_code"`
}

func loadEmailSemanticCorpus(t *testing.T) []semanticSample {
	t.Helper()
	raw, err := os.ReadFile("testdata/email_analysis_semantic_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Samples []semanticSample `json:"samples"`
	}
	if err = json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	return corpus.Samples
}
func TestEmailSemanticCorpusFrozenCoverage(t *testing.T) {
	rows := loadEmailSemanticCorpus(t)
	if len(rows) < 120 {
		t.Fatal("fewer than120samples")
	}
	groups := map[string]int{}
	ids := map[string]bool{}
	for _, r := range rows {
		if ids[r.ID] {
			t.Fatal("duplicatecase")
		}
		ids[r.ID] = true
		groups[r.Group]++
	}
	for _, g := range []string{"interaction", "notification", "verification", "ambiguous"} {
		if groups[g] < 30 {
			t.Fatalf("group %s under30", g)
		}
	}
}

// Explicit opt-in is required: this test calls the configured real Fast model
// with only the checked-in synthetic corpus, never a mailbox or a send tool.
func TestEmailSemanticCorpusRealModel(t *testing.T) {
	if os.Getenv("SPARKCLAW_EMAIL_REAL_EVAL") != "1" {
		t.Skip("opt-in real-model semantic qualification")
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model.Mock {
		t.Fatal("mock profile cannot qualify semantics")
	}
	recorder := &semanticRecordingClient{modelClient: modelrouter.New(cfg), outputs: map[string]string{}}
	analyzer := NewModelAnalyzer(recorder)
	samples := loadEmailSemanticCorpus(t)
	type result struct {
		RawCategory    string `json:"raw_category,omitempty"`
		RawReason      string `json:"raw_reason,omitempty"`
		RawCodeAttempt bool   `json:"raw_code_attempt"`
		ValidatedCopy  bool   `json:"validated_copy"`
		ID             string `json:"id"`
		Group          string `json:"group"`
		Expected       string `json:"expected"`
		Category       string `json:"category"`
		Fallback       bool   `json:"fallback"`
		CodeCorrect    bool   `json:"code_correct"`
		FalseCode      bool   `json:"false_code"`
		Model          string `json:"model"`
		Error          string `json:"error,omitempty"`
	}
	results := make([]result, len(samples))
	queue := make(chan int)
	var wg sync.WaitGroup
	var completed atomic.Int64
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				row := samples[i]
				input := AnalysisInput{Kind: app.EmailJobClassification, TargetID: row.ID, OutputLanguage: row.Language, SourceTime: time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC), Subject: "", Evidence: []Evidence{{Ref: "representation:" + row.ID + ":body", Text: row.Body}}, Candidates: []AnalysisCandidate{}, MissingContext: []string{}}
				output, err := analyzer.Analyze(t.Context(), input)
				r := result{ID: row.ID, Group: row.Group, Expected: row.ExpectedCategory, Category: output.Category, Fallback: output.Uncertainty || output.Category == "unknown", CodeCorrect: output.VerificationCode == row.ExpectedCode, FalseCode: output.VerificationCode != "" && output.VerificationCode != row.ExpectedCode, Model: output.ModelVersion}
				if err != nil {
					r.Error = safeCode(err)
					if strings.HasPrefix(err.Error(), "email_model_") || strings.HasPrefix(err.Error(), "email_verification_") {
						r.Error = err.Error()
					}
					r.Category = "error"
					r.Fallback = true
				}
				recorder.mu.Lock()
				raw := recorder.outputs[row.ID]
				recorder.mu.Unlock()
				var decoded AnalysisOutput
				if json.Unmarshal([]byte(raw), &decoded) == nil {
					r.RawCategory = decoded.Category
					r.RawReason = decoded.Reason
					r.RawCodeAttempt = decoded.VerificationCode != ""
				}
				if err == nil {
					v, validationErr := verificationFromOutput(input, output)
					r.ValidatedCopy = validationErr == nil && v != nil
				}
				results[i] = r
				if n := completed.Add(1); n%10 == 0 {
					t.Logf("real semantic progress=%d/%d", n, len(samples))
				}
			}
		}()
	}
	for i := range samples {
		queue <- i
	}
	close(queue)
	wg.Wait()
	matrix := map[string]map[string]int{}
	automatic, clear, errors, interactionToNotification, ambiguousWrong, falseCodes, correctCodes := 0, 0, 0, 0, 0, 0, 0
	precisionTotal := map[string]int{}
	precisionCorrect := map[string]int{}
	expectedTotal := map[string]int{}
	for _, r := range results {
		if matrix[r.Expected] == nil {
			matrix[r.Expected] = map[string]int{}
		}
		matrix[r.Expected][r.Category]++
		expectedTotal[r.Expected]++
		if r.Error != "" {
			errors++
		}
		if r.Group != "ambiguous" {
			clear++
			if !r.Fallback {
				automatic++
			}
		}
		if r.Expected == "interaction" && r.Category == "notification" && !r.Fallback {
			interactionToNotification++
		}
		if r.Group == "ambiguous" && !r.Fallback {
			ambiguousWrong++
		}
		if r.FalseCode {
			falseCodes++
		}
		if r.Group == "verification" && r.CodeCorrect {
			correctCodes++
		}
		if !r.Fallback && (r.Category == "notification" || r.Category == "interaction") {
			precisionTotal[r.Category]++
			if r.Category == r.Expected {
				precisionCorrect[r.Category]++
			}
		}
	}
	precision := map[string]float64{}
	recall := map[string]float64{}
	for _, c := range []string{"interaction", "notification"} {
		precision[c] = float64(precisionCorrect[c]) / float64(max(1, precisionTotal[c]))
		recall[c] = float64(precisionCorrect[c]) / float64(max(1, expectedTotal[c]))
	}
	coverage := float64(automatic) / float64(clear)
	leak := float64(interactionToNotification) / float64(expectedTotal["interaction"])
	passed := coverage >= 0.90 && precision["interaction"] >= 0.95 && precision["notification"] >= 0.95 && leak <= 0.02 && ambiguousWrong == 0 && falseCodes == 0
	report := map[string]any{"prompt_version": analysisPromptVersion, "schema_validity_rate": float64(len(samples)-errors) / float64(len(samples)), "fallback_rate": 1 - float64(precisionTotal["notification"]+precisionTotal["interaction"])/float64(len(samples)), "corpus": "synthetic-v1", "sample_count": len(samples), "model_profile": cfg.Model.CapacityProfile, "confusion_matrix": matrix, "precision": precision, "recall": recall, "clear_automatic_coverage": coverage, "required_interaction_to_notification_rate": leak, "ambiguous_without_fallback": ambiguousWrong, "false_code_extractions": falseCodes, "correct_verification_codes": correctCodes, "model_errors": errors, "release_gates_passed": passed, "results": results}
	raw, _ := json.MarshalIndent(report, "", "  ")
	path := os.Getenv("SPARKCLAW_EMAIL_EVAL_REPORT")
	if path == "" {
		path = filepath.Join(t.TempDir(), "semantic-report.json")
	}
	if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("samples=%d errors=%d coverage=%.3f precision(notification/interaction)=%.3f/%.3f interaction_leak=%.3f ambiguity_failures=%d false_codes=%d report=%s", len(samples), errors, coverage, precision["notification"], precision["interaction"], leak, ambiguousWrong, falseCodes, path)
	if !passed {
		t.Error("real-model semantic release gates not met; see frozen report")
	}
}

type semanticRecordingClient struct {
	modelClient
	mu      sync.Mutex
	outputs map[string]string
}

func (c *semanticRecordingClient) ChatWithProfileOptions(ctx context.Context, op modelcapacity.Operation, lane, system, prompt string, opts modelrouter.ChatOptions) (modelrouter.ChatResult, error) {
	result, err := c.modelClient.ChatWithProfileOptions(ctx, op, lane, system, prompt, opts)
	var input struct {
		TargetID string `json:"target_id"`
	}
	_ = json.Unmarshal([]byte(prompt), &input)
	c.mu.Lock()
	c.outputs[input.TargetID] = result.Content
	c.mu.Unlock()
	return result, err
}
