package agent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelcapacity"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func TestTreeRoutingProjectionsAreValidAndOmitRuntimeBindings(t *testing.T) {
	resources := []app.MessagePart{{
		ID: "part-secret", Kind: app.MessagePartFile, Disposition: app.MessageDispositionAttachment,
		Resource: &app.ResourceRef{Kind: "workspace", Ref: "uploads/report.docx"},
		Name:     "report.docx", ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		Bytes: 12345, SHA256: "secret-hash", Caption: "owner caption",
	}}
	documents := documentContextResolution{References: []documentContextReference{{
		DocumentID: "doc-secret", ParentDocumentID: "parent-secret", SourceID: "source-secret",
		Ref: "uploads/report.docx", Name: "report.docx", ContentType: "application/docx",
		Format: "docx", Source: "attachment", Activity: "read", Provenance: documentProvenanceDocumentRecord,
	}}}
	context := newTreeRoutingPromptContext(resources, agentContextSnapshot{}, documents)

	for name, projection := range map[string]string{
		"resources_full": context.ResourcesFull, "resources_minimal": context.ResourcesMinimal,
		"documents_full": context.DocumentsFull, "documents_minimal": context.DocumentsMinimal,
	} {
		var decoded []map[string]any
		if err := json.Unmarshal([]byte(projection), &decoded); err != nil || len(decoded) != 1 {
			t.Fatalf("%s projection is not valid JSON: %q err=%v", name, projection, err)
		}
		for _, forbidden := range []string{"part-secret", "secret-hash", "doc-secret", "parent-secret", "source-secret", `"bytes"`, `"id"`, `"sha256"`} {
			if strings.Contains(projection, forbidden) {
				t.Fatalf("%s projection leaked %q: %s", name, forbidden, projection)
			}
		}
	}
}

func TestTreePromptUsesLegalVariantsAndPreservesOwnerQuestion(t *testing.T) {
	cfg := agentTestConfig()
	cfg.Model.Fast.ContextTokens = 1800
	cfg.Model.Fast.OutputBudgets[modelcapacity.OutputCompactStructured] = 256
	st := store.NewMemoryStore()
	runtime := NewRuntime(st, toolhub.New(cfg, st), policy.New(cfg), modelrouter.New(cfg), nil)
	question := "请完整分析这个附件，不要改写我的问题"
	context := newTreeRoutingPromptContext([]app.MessagePart{{
		Kind: app.MessagePartFile, Resource: &app.ResourceRef{Kind: "workspace", Ref: "uploads/report.docx"},
		Name: "report.docx", ContentType: "application/docx", Caption: strings.Repeat("caption ", 3000),
	}}, agentContextSnapshot{Messages: []app.Message{{Role: "user", Content: strings.Repeat("history ", 1000)}}}, documentContextResolution{})
	options := modelrouter.ChatOptions{StrictJSONSchema: &modelrouter.StrictJSONSchema{
		Name: "tree_test", Schema: map[string]any{"type": "object"},
	}}
	admission, err := runtime.admitTreePrompt(t.Context(), modelcapacity.OperationIntentTreeScore,
		"Score candidates.", question, app.MessageSourceWeb,
		`[{"candidate_id":"conversation.answer#answer"}]`, context, "", "", options,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(admission.User, "Owner semantic query:\n"+question) || strings.Contains(admission.User, "prompt_truncated") {
		t.Fatalf("Tree admission changed the owner question: %s", admission.User)
	}
	selected, ok := admission.SelectedVariants["current_resources"]
	if !ok || selected.Name != "minimal" {
		t.Fatalf("Tree admission did not select the legal minimal resource projection: %#v", admission.SelectedVariants)
	}
	jsonStart := strings.Index(selected.Text, "\n")
	if jsonStart < 0 || !json.Valid([]byte(selected.Text[jsonStart+1:])) {
		t.Fatalf("selected resource variant is not valid JSON: %q", selected.Text)
	}
	for _, decision := range admission.SectionDecisions {
		if decision.ToVariant == "truncated" {
			t.Fatalf("Tree admission used forbidden hard truncation: %#v", decision)
		}
	}
}

func TestWorkflowOwnerGoalIsFixed(t *testing.T) {
	goal := strings.Repeat("原样问题", 40)
	builder := workflowStepContextBuilderForTimezone(goal, 1, nil, workflowStageContext{}, nil, provisionedWorkflowEvidence{}, agentContextSnapshot{}, "")
	for _, section := range builder.Sections {
		if section.Kind != "owner_goal" {
			continue
		}
		if section.Policy != contextPolicyFixed || len(section.Variants) != 1 || section.Variants[0].Text != goal {
			t.Fatalf("owner goal is not fixed and unchanged: %#v", section)
		}
		return
	}
	t.Fatal("owner goal section is missing")
}
