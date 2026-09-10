package emailmanagement

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

// Explicit opt-in: regular tests never call a configured external model. This
// smoke uses synthetic correspondence and does not meet the reviewed-corpus
// assignment/summary gates in the stage-five acceptance document.
func TestEmailManagementRealModelSmoke(t *testing.T) {
	configuration := os.Getenv("SPARKCLAW_TEST_EMAIL_MODEL_CONFIG")
	if configuration == "" {
		t.Skip("set SPARKCLAW_TEST_EMAIL_MODEL_CONFIG for explicit real-model qualification")
	}
	cfg, err := config.Load(configuration)
	if err != nil {
		t.Fatal(err)
	}
	analyzer := NewModelAnalyzer(modelrouter.New(cfg))
	input := AnalysisInput{Kind: app.EmailJobMessageSummary, TargetID: "synthetic-mail", Subject: "Purchase approval", Participants: []string{"owner@example.test", "supplier@example.test"},
		Evidence: []Evidence{{Ref: "synthetic:headers", Text: "From supplier@example.test to owner@example.test; subject Purchase approval"}, {Ref: "synthetic:body", Text: "Please approve the purchase of 12 desk lamps for a total of USD 240. Delivery date is not yet agreed."}}, Candidates: []AnalysisCandidate{}, MissingContext: []string{}}
	for _, kind := range []string{app.EmailJobMessageSummary, app.EmailJobAssignment, app.EmailJobRelationshipCheck, app.EmailJobConversationSummary} {
		t.Run(kind, func(t *testing.T) {
			input.Kind = kind
			if kind == app.EmailJobRelationshipCheck {
				input.CurrentConversationID = "fixed-conversation"
			} else {
				input.CurrentConversationID = ""
			}
			output, err := analyzer.Analyze(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if output.Mock || output.ModelVersion == "" {
				t.Fatal("real model evidence absent")
			}
			if kind == app.EmailJobAssignment && output.Action != "new" {
				t.Fatalf("synthetic new topic action=%q", output.Action)
			}
			raw, _ := json.Marshal(output)
			t.Logf("model=%s prompt=%s synthetic_output=%s", output.ModelVersion, analysisPromptVersion, raw)
		})
	}
}
