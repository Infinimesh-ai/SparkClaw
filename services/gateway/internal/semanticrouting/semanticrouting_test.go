package semanticrouting

import (
	"errors"
	"math"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/capability"
)

func TestCompileBuildsStableCatalogGroundedCandidates(t *testing.T) {
	catalog := testCatalog(t)
	graph, err := Compile(catalog, testRegistrations())
	if err != nil {
		t.Fatal(err)
	}
	if graph.CatalogRevision() != "test.v1" || graph.Revision() == graph.CatalogRevision() || len(graph.Candidates()) != 2 {
		t.Fatalf("compiled graph identity is incomplete: revision=%q candidates=%#v", graph.Revision(), graph.Candidates())
	}
	candidate, ok := graph.Candidate("test.answer#answer")
	if !ok || candidate.Route.Operation != app.RouteOperationAnswer || len(candidate.CapabilityPath) != 2 || candidate.CapabilityPath[1] != "test.answer" {
		t.Fatalf("compiled candidate lost its Catalog identity: %#v", candidate)
	}
}

func TestCompileRejectsVariantOutsideCatalogContract(t *testing.T) {
	registrations := testRegistrations()
	registrations[0].Semantics.Variants[0].Route.Operation = app.RouteOperationDelete
	if _, err := Compile(testCatalog(t), registrations); err == nil {
		t.Fatal("graph compiler accepted an operation outside the Catalog contract")
	}
}

func TestEmbeddingIndexAggregatesPositiveAndNegativeEvidence(t *testing.T) {
	graph, err := Compile(testCatalog(t), testRegistrations())
	if err != nil {
		t.Fatal(err)
	}
	corpus := graph.EmbeddingCorpus()
	vectors := make([][]float32, len(corpus))
	for index, entry := range corpus {
		switch {
		case entry.CandidateID == "test.answer#answer" && !entry.Negative:
			vectors[index] = []float32{1, 0}
		case entry.CandidateID == "test.answer#answer":
			vectors[index] = []float32{0, 1}
		default:
			vectors[index] = []float32{0, 1}
		}
	}
	if _, err := BuildEmbeddingIndex(graph, " ", vectors); err == nil {
		t.Fatal("embedding index accepted a blank model identity")
	}
	index, err := BuildEmbeddingIndex(graph, "embedding@test", vectors)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := index.Score([]float32{1, 0}, map[string]bool{"test.answer#answer": true, "test.search#search": true}, DefaultCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if evidence["test.answer#answer"].Score <= evidence["test.search#search"].Score {
		t.Fatalf("positive semantic support did not outrank the unrelated candidate: %#v", evidence)
	}
}

func TestFusionProducesClearTopTwoWithoutAuthorizingSecondCandidate(t *testing.T) {
	graph, eligible := testGraphAndCandidates(t)
	channels := healthyChannels()
	decision, err := Fuse(graph, eligible,
		map[string]EmbeddingEvidence{
			"test.answer#answer": {Score: 0.92}, "test.search#search": {Score: 0.30},
		},
		map[string]TreeEvidence{
			"test.answer#answer": {Score: 0.90}, "test.search#search": {Score: 0.25},
		},
		map[string]float64{"test.answer#answer": 0.95, "test.search#search": 0.20}, channels, DefaultCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Verdict != VerdictClear || len(decision.Candidates) != 2 || decision.Candidates[0].Candidate.ID != "test.answer#answer" || decision.Margin <= 0 {
		t.Fatalf("clear fusion decision lost its final Top-2 evidence: %#v", decision)
	}
}

func TestFusionDistinguishesAmbiguousLowAndUnavailable(t *testing.T) {
	graph, eligible := testGraphAndCandidates(t)
	calibration := DefaultCalibration()
	tests := []struct {
		name      string
		embedding map[string]EmbeddingEvidence
		tree      map[string]TreeEvidence
		reranked  map[string]float64
		channels  map[string]ChannelState
		verdict   Verdict
	}{
		{
			name:      "ambiguous",
			embedding: map[string]EmbeddingEvidence{"test.answer#answer": {Score: 0.78}, "test.search#search": {Score: 0.77}},
			tree:      map[string]TreeEvidence{"test.answer#answer": {Score: 0.80}, "test.search#search": {Score: 0.79}},
			reranked:  map[string]float64{"test.answer#answer": 0.81, "test.search#search": 0.80}, channels: healthyChannels(), verdict: VerdictAmbiguous,
		},
		{
			name:      "low",
			embedding: map[string]EmbeddingEvidence{"test.answer#answer": {Score: 0.10}, "test.search#search": {Score: 0.08}},
			tree:      map[string]TreeEvidence{"test.answer#answer": {Score: 0.12}, "test.search#search": {Score: 0.10}},
			reranked:  map[string]float64{"test.answer#answer": 0.10, "test.search#search": 0.08}, channels: healthyChannels(), verdict: VerdictLow,
		},
		{
			name: "unavailable", channels: map[string]ChannelState{
				"embedding": {Status: ChannelFailed}, "fast": {Status: ChannelFailed}, "reranker": {Status: ChannelFailed},
			}, verdict: VerdictBlocked,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision, err := Fuse(graph, eligible, test.embedding, test.tree, test.reranked, test.channels, calibration)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Verdict != test.verdict {
				t.Fatalf("verdict=%q want %q: %#v", decision.Verdict, test.verdict, decision)
			}
		})
	}
}

func TestRankFusionUsesAlphaAndOneMinusAlpha(t *testing.T) {
	_, eligible := testGraphAndCandidates(t)
	calibration := DefaultCalibration()
	calibration.Alpha = 0.60
	scores, err := RankFusion(
		eligible,
		map[string]EmbeddingEvidence{
			"test.answer#answer": {Score: 0.80},
			"test.search#search": {Score: 0.70},
		},
		map[string]TreeEvidence{
			"test.answer#answer": {Score: 0.40},
		},
		healthyChannels(),
		calibration,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 || scores[0].Candidate.ID != "test.answer#answer" {
		t.Fatalf("weighted fusion returned the wrong order: %#v", scores)
	}
	if math.Abs(scores[0].FusionScore-0.64) > 1e-9 {
		t.Fatalf("fusion score=%v want alpha*0.8+(1-alpha)*0.4=0.64", scores[0].FusionScore)
	}
	if scores[1].TreeScore != 0 || math.Abs(scores[1].FusionScore-0.42) > 1e-9 {
		t.Fatalf("omitted Tree candidate did not contribute zero: %#v", scores[1])
	}
}

func TestRankFusionReportsBothSemanticChannelsUnavailable(t *testing.T) {
	_, eligible := testGraphAndCandidates(t)
	_, err := RankFusion(eligible, nil, nil, map[string]ChannelState{
		"embedding": {Status: ChannelFailed}, "tree": {Status: ChannelFailed},
	}, DefaultCalibration())
	if !errors.Is(err, ErrSemanticChannelsUnavailable) {
		t.Fatalf("both-channel outage did not fail closed: %v", err)
	}
}

func TestMutationBlocksWheneverSemanticPipelineIsDegraded(t *testing.T) {
	_, eligible := testGraphAndCandidates(t)
	mutation := eligible[0]
	mutation.Route.Operation = app.RouteOperationEdit
	channels := healthyChannels()
	channels["reranker"] = ChannelState{Status: ChannelFailed, ReasonCode: "timeout"}
	decision, err := Decide([]CandidateScore{{Candidate: mutation, FusionScore: 0.99}}, nil, channels, DefaultCalibration())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Verdict != VerdictBlocked || decision.ReasonCode != "mutation_requires_healthy_semantic_pipeline" || !decision.Degraded || len(decision.Candidates) != 1 {
		t.Fatalf("degraded mutation did not fail closed with retained evidence: %#v", decision)
	}
}

func testGraphAndCandidates(t *testing.T) (*Graph, []Candidate) {
	t.Helper()
	graph, err := Compile(testCatalog(t), testRegistrations())
	if err != nil {
		t.Fatal(err)
	}
	return graph, graph.EligibleCandidates(app.MessageSourceWeb)
}

func healthyChannels() map[string]ChannelState {
	return map[string]ChannelState{
		"embedding": {Status: ChannelHealthy, Model: "embedding@test"},
		"tree":      {Status: ChannelHealthy, Model: "fast@test"},
		"reranker":  {Status: ChannelHealthy, Model: "reranker@test"},
	}
}

func testRegistrations() []Registration {
	return []Registration{
		{
			Capability: "test.answer", Workflow: app.WorkflowContractRef{ID: "test.answer", Revision: 1},
			Semantics: WorkflowSemantics{Variants: []IntentVariant{{
				Key: "answer", Route: RouteTemplate{Operation: app.RouteOperationAnswer},
				EmbedTexts: []string{"explain a stable fact"}, TreeDescription: "Answer stable facts.", HardNegatives: []string{"current price"},
			}}},
		},
		{
			Capability: "test.search", Workflow: app.WorkflowContractRef{ID: "test.search", Revision: 1},
			Semantics: WorkflowSemantics{Variants: []IntentVariant{{
				Key: "search", Route: RouteTemplate{Operation: app.RouteOperationSearch, FactScope: app.RouteFactScopeCurrentInternet},
				EmbedTexts: []string{"find the current price"}, TreeDescription: "Search current facts.", HardNegatives: []string{"stable explanation"},
			}}},
		},
	}
}

func testCatalog(t *testing.T) capability.Catalog {
	t.Helper()
	answerWorkflow := app.WorkflowContractRef{ID: "test.answer", Revision: 1}
	searchWorkflow := app.WorkflowContractRef{ID: "test.search", Revision: 1}
	catalog, err := capability.NewCatalog("test.v1", []capability.Node{
		{ID: capability.RootID, Kind: capability.NodeBranch, Description: "test capabilities"},
		{ID: "test", ParentID: capability.RootID, Kind: capability.NodeBranch, Description: "test branch"},
		{ID: "test.answer", ParentID: "test", Kind: capability.NodeLeaf, Description: "stable answers", Workflow: &answerWorkflow,
			Route: &capability.RouteContract{Operations: []app.RouteOperation{app.RouteOperationAnswer}, RequireQuery: true}},
		{ID: "test.search", ParentID: "test", Kind: capability.NodeLeaf, Description: "current search", Workflow: &searchWorkflow,
			Route: &capability.RouteContract{Operations: []app.RouteOperation{app.RouteOperationSearch}, FactScopes: []app.RouteFactScope{app.RouteFactScopeCurrentInternet}, RequireQuery: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}
