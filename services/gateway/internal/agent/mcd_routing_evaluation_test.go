package agent

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/semanticrouting"
)

const mcdOrderCandidateID = "dining.mcd_order#order"
const mcdRoutingCorpusPath = "../../../../eval/golden/mcd-order-routing.json"

type mcdRoutingCase struct {
	ID                string   `json:"id"`
	Language          string   `json:"language"`
	Prompt            string   `json:"prompt"`
	ExpectedCandidate string   `json:"expected_candidate"`
	Tags              []string `json:"tags"`
}

type mcdRoutingCorpus struct {
	SchemaVersion string           `json:"schema_version"`
	Scope         string           `json:"scope"`
	Cases         []mcdRoutingCase `json:"cases"`
}

func loadMCDRoutingCorpus(t *testing.T) mcdRoutingCorpus {
	t.Helper()
	raw, err := os.ReadFile(mcdRoutingCorpusPath)
	if err != nil {
		t.Fatal(err)
	}
	var corpus mcdRoutingCorpus
	if err := json.Unmarshal(raw, &corpus); err != nil {
		t.Fatal(err)
	}
	return corpus
}

func TestMCDRoutingCorpusCoverage(t *testing.T) {
	corpus := loadMCDRoutingCorpus(t)
	if corpus.SchemaVersion != "mcd_order_routing_eval_v1" || strings.TrimSpace(corpus.Scope) == "" || len(corpus.Cases) < 30 {
		t.Fatalf("invalid or incomplete ordering evaluation corpus: %#v", corpus)
	}
	seenIDs, seenPrompts := map[string]bool{}, map[string]bool{}
	counts := map[string]int{}
	for _, sample := range corpus.Cases {
		if sample.ID == "" || seenIDs[sample.ID] || strings.TrimSpace(sample.Prompt) == "" || seenPrompts[sample.Prompt] {
			t.Fatalf("empty or duplicate evaluation sample: %#v", sample)
		}
		seenIDs[sample.ID], seenPrompts[sample.Prompt] = true, true
		if sample.Language != "en" && sample.Language != "zh-CN" {
			t.Fatalf("unsupported sample language: %#v", sample)
		}
		ordering := sample.ExpectedCandidate == mcdOrderCandidateID
		if ordering != slices.Contains(sample.Tags, "ordering") || ordering == slices.Contains(sample.Tags, "hard_negative") {
			t.Fatalf("sample intent and tags disagree: %#v", sample)
		}
		if !ordering && sample.ExpectedCandidate != "conversation.answer#answer" && sample.ExpectedCandidate != "browser.internet_search#search" {
			t.Fatalf("non-ordering sample must stay with general answers or search: %#v", sample)
		}
		for _, tag := range sample.Tags {
			counts[sample.Language+":"+tag]++
		}
	}
	for _, language := range []string{"zh-CN", "en"} {
		if counts[language+":ordering"] < 10 || counts[language+":hard_negative"] < 10 {
			t.Fatalf("insufficient positive/negative coverage in %s: %#v", language, counts)
		}
		for _, tag := range []string{"direct", "quantity", "store", "pickup_time", "wallet", "amount_limit", "discount", "configuration", "missing_store", "new_order", "paraphrase", "history", "nutrition", "nearby", "prior_order", "pickup_code", "menu_browse", "offer_browse", "cart_only", "price_only", "negation", "quotation", "hypothetical"} {
			if counts[language+":"+tag] == 0 {
				t.Errorf("missing %s %s sample", language, tag)
			}
		}
	}
}

// This checks the labeled corpus against the actual registry, not a mock
// classifier. Model accuracy requires the explicitly enabled calibration test.
func TestMCDRoutingCorpusContracts(t *testing.T) {
	registry := defaultWorkflowProfileRegistry()
	catalog := capability.MustDefaultCatalog()
	graph, err := registry.SemanticGraph(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range loadMCDRoutingCorpus(t).Cases {
		t.Run(sample.ID, func(t *testing.T) {
			candidate, ok := graph.Candidate(sample.ExpectedCandidate)
			if !ok {
				t.Fatalf("expected route is not registered: %s", sample.ExpectedCandidate)
			}
			decision := app.RouteDecision{SchemaVersion: app.RouteDecisionSchemaVersion, Status: app.RouteMatched,
				CatalogRevision: catalog.Revision(), CapabilityPath: candidate.CapabilityPath,
				Slots: app.RouteSlots{Operation: candidate.Route.Operation, FactScope: candidate.Route.FactScope, Query: sample.Prompt}}
			resolved, err := registry.Resolve(catalog, decision, sample.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (resolved.Profile.ID() == app.WorkflowMCDOrder) != slices.Contains(sample.Tags, "ordering") {
				t.Fatalf("labeled intent resolved to an inappropriate workflow: %s", resolved.Profile.ID())
			}
		})
	}
}

func TestMCDOrderingMenuAndStatusHardNegativesReachBothChannels(t *testing.T) {
	graph, err := defaultWorkflowProfileRegistry().SemanticGraph(capability.MustDefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	candidate, ok := graph.Candidate(mcdOrderCandidateID)
	if !ok {
		t.Fatal("ordering semantic candidate is missing")
	}
	projected := treeGraphProjection([]semanticrouting.Candidate{candidate})
	if len(projected) != 1 {
		t.Fatal("ordering candidate was lost from Tree projection")
	}
	negatives, ok := projected[0]["hard_negatives"].([]string)
	if !ok {
		t.Fatal("ordering Tree projection lost hard negatives")
	}
	for _, sample := range loadMCDRoutingCorpus(t).Cases {
		if !slices.Contains(sample.Tags, "menu_browse") && !slices.Contains(sample.Tags, "prior_order") {
			continue
		}
		if !slices.Contains(negatives, sample.Prompt) || slices.Contains(candidate.EmbedTexts, sample.Prompt) {
			t.Errorf("%s is not an explicit ordering hard negative in the Tree projection", sample.ID)
		}
		found := false
		for _, entry := range graph.EmbeddingCorpus() {
			if entry.CandidateID == mcdOrderCandidateID && entry.Text == sample.Prompt {
				found = true
				if !entry.Negative {
					t.Errorf("%s incorrectly supplies positive ordering embedding evidence", sample.ID)
				}
			}
		}
		if !found {
			t.Errorf("%s is missing from the ordering negative embedding corpus", sample.ID)
		}
	}
}
