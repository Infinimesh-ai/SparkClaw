package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
)

func TestTreeRoutingStrictJSONSchemaMatchesEligibleCandidates(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	eligible := graph.EligibleCandidates(app.MessageSourceWeb)[:3]
	options := treeRoutingChatOptions(graph.Revision(), eligible)
	if !options.ForceDisableThinking || options.StrictJSONSchema == nil {
		t.Fatalf("Tree options do not enforce structured non-thinking output: %#v", options)
	}

	schema := options.StrictJSONSchema.Schema
	properties := schema["properties"].(map[string]any)
	revision := properties["graph_revision"].(map[string]any)
	if !slices.Equal(revision["enum"].([]string), []string{graph.Revision()}) {
		t.Fatalf("Tree schema does not freeze graph revision: %#v", revision)
	}
	candidates := properties["candidates"].(map[string]any)
	if candidates["minItems"] != len(eligible) || candidates["maxItems"] != len(eligible) {
		t.Fatalf("Tree schema does not require the eligible candidate count: %#v", candidates)
	}
	itemProperties := candidates["items"].(map[string]any)["properties"].(map[string]any)
	ids := itemProperties["candidate_id"].(map[string]any)["enum"].([]any)
	for index, candidate := range eligible {
		if ids[index] != candidate.ID {
			t.Fatalf("Tree schema candidate %d = %#v, want %q", index, ids[index], candidate.ID)
		}
	}
	score := itemProperties["tree_score"].(map[string]any)
	if score["type"] != "number" || score["minimum"] != 0 || score["maximum"] != 1 {
		t.Fatalf("Tree schema score bounds are incomplete: %#v", score)
	}
}

func TestTreeChannelUsesStrictOutputAndRepairsAtMostOnce(t *testing.T) {
	catalog := capability.MustDefaultCatalog()
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	eligible := graph.EligibleCandidates(app.MessageSourceWeb)[:2]
	valid := treeRoutingTestJSON(t, graph.Revision(), eligible)

	tests := []struct {
		name       string
		responses  []string
		wantCalls  int32
		wantStatus semanticrouting.ChannelStatus
		wantReason string
	}{
		{name: "first response valid", responses: []string{valid}, wantCalls: 1, wantStatus: semanticrouting.ChannelHealthy},
		{name: "one repair succeeds", responses: []string{"not json", valid}, wantCalls: 2, wantStatus: semanticrouting.ChannelHealthy},
		{name: "one repair fails", responses: []string{"not json", "still not json"}, wantCalls: 2, wantStatus: semanticrouting.ChannelFailed, wantReason: "tree_output_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			var requestSchemas []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					return
				}
				kwargs, _ := body["chat_template_kwargs"].(map[string]any)
				if kwargs["enable_thinking"] != false {
					t.Errorf("Tree call did not force thinking off: %#v", kwargs)
				}
				responseFormat, _ := body["response_format"].(map[string]any)
				jsonSchema, _ := responseFormat["json_schema"].(map[string]any)
				if responseFormat["type"] != "json_schema" || jsonSchema["strict"] != true {
					t.Errorf("Tree call omitted strict JSON schema: %#v", responseFormat)
				}
				schemaJSON, err := json.Marshal(jsonSchema["schema"])
				if err != nil {
					t.Error(err)
					return
				}
				requestSchemas = append(requestSchemas, string(schemaJSON))
				if body["temperature"] != 0.2 {
					t.Errorf("Tree call changed temperature: %#v", body["temperature"])
				}
				index := int(calls.Add(1)) - 1
				if index >= len(test.responses) {
					index = len(test.responses) - 1
				}
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]string{"content": test.responses[index]}}},
				})
			}))
			defer server.Close()

			cfg := config.Default()
			cfg.Model.Mock = false
			cfg.Model.DisableThinking = false
			cfg.Model.Fast.BaseURL = server.URL
			cfg.Model.Fast.Name = "sparkclaw-fast"
			cfg.Model.Fast.Model = "Qwen/Fast"
			st := store.NewMemoryStore()
			runtime := Runtime{
				store: st, models: modelrouter.New(cfg),
				semanticRouter: newSemanticIntentRouter(catalog.Revision(), graph),
			}

			result := runtime.scoreTreeChannel(t.Context(), "session", "run", "owner question", treeRoutingPromptContext{}, app.MessageSourceWeb, eligible)
			if got := calls.Load(); got != test.wantCalls {
				t.Fatalf("Tree model call count=%d want=%d", got, test.wantCalls)
			}
			expectedSchema, err := json.Marshal(treeRoutingChatOptions(graph.Revision(), eligible).StrictJSONSchema.Schema)
			if err != nil {
				t.Fatal(err)
			}
			if len(requestSchemas) != int(test.wantCalls) {
				t.Fatalf("captured Tree schemas=%d want=%d", len(requestSchemas), test.wantCalls)
			}
			for index, requestSchema := range requestSchemas {
				if requestSchema != string(expectedSchema) {
					t.Fatalf("Tree call %d used a different schema: got=%s want=%s", index+1, requestSchema, expectedSchema)
				}
			}
			if result.state.Status != test.wantStatus || result.state.ReasonCode != test.wantReason {
				t.Fatalf("unexpected Tree state: %#v", result.state)
			}
			if test.wantStatus == semanticrouting.ChannelHealthy && len(result.evidence) != len(eligible) {
				t.Fatalf("valid structured Tree output lost evidence: %#v", result.evidence)
			}
		})
	}
}

func treeRoutingTestJSON(t *testing.T, revision string, eligible []semanticrouting.Candidate) string {
	t.Helper()
	output := treeRoutingOutput{GraphRevision: revision}
	for index, candidate := range eligible {
		score := 0.9 - float64(index)*0.1
		output.Candidates = append(output.Candidates, treeRoutingCandidate{CandidateID: candidate.ID, TreeScore: &score})
	}
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
