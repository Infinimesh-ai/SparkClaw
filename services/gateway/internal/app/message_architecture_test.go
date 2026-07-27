package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestEffectiveMessagingBindingScopesUpgradesLegacyBindings(t *testing.T) {
	want := DefaultMessagingBindingScopes()
	for _, scopes := range [][]string{nil, {BindingScopeReminderSendSelf}, {BindingScopeMessageSendSelf}} {
		if got := EffectiveMessagingBindingScopes(scopes); !slices.Equal(got, want) {
			t.Fatalf("legacy scopes %#v normalized to %#v, want %#v", scopes, got, want)
		}
	}

	unknown := []string{"unknown"}
	if got := EffectiveMessagingBindingScopes(unknown); !slices.Equal(got, unknown) {
		t.Fatalf("unknown-only scopes were expanded: %#v", got)
	}
}

func TestIntentFusionCandidatePersistsOnlyChannelScores(t *testing.T) {
	raw, err := json.Marshal(IntentFusionDecision{
		SchemaVersion: IntentFusionDecisionSchemaVersion,
		Channels: IntentFusionChannels{
			Embedding: IntentFusionChannel{Status: "healthy"},
			Tree:      IntentFusionChannel{Status: "healthy"},
		},
		Candidates: []IntentFusionCandidate{{
			CandidateID: "schedule.manage#create", EmbeddingScore: 0.8, TreeScore: 0.7,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, required := range []string{`"tree"`, `"embedding_score"`, `"tree_score"`} {
		if !strings.Contains(encoded, required) {
			t.Fatalf("intent fusion JSON is missing %s: %s", required, encoded)
		}
	}
	for _, removed := range []string{`"fast_score"`, `"fast_reason_code"`, `"reranker"`, `"reranker_score"`, `"final_score"`} {
		if strings.Contains(encoded, removed) {
			t.Fatalf("intent fusion JSON retained removed field %s: %s", removed, encoded)
		}
	}
}
