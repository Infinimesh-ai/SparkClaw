package semanticrouting

import (
	"errors"
	"slices"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

var ErrSemanticChannelsUnavailable = errors.New("semantic routing channels are unavailable")

type ChannelStatus string

const (
	ChannelHealthy ChannelStatus = "healthy"
	ChannelFailed  ChannelStatus = "failed"
)

type Verdict string

const (
	VerdictClear     Verdict = "clear"
	VerdictAmbiguous Verdict = "ambiguous"
	VerdictLow       Verdict = "low"
	VerdictBlocked   Verdict = "blocked"
)

type ChannelState struct {
	Status     ChannelStatus
	Model      string
	ReasonCode string
}

type CandidateScore struct {
	Candidate        Candidate
	EmbeddingScore   float64
	TreeScore        float64
	FusionScore      float64
	RerankerScore    float64
	FinalScore       float64
	NegativeConflict float64
}

type Decision struct {
	Verdict    Verdict
	Candidates []CandidateScore
	Confidence float64
	Margin     float64
	Degraded   bool
	ReasonCode string
}

type TreeEvidence struct {
	Score float64
}

func RankFusion(eligible []Candidate, embeddings map[string]EmbeddingEvidence, tree map[string]TreeEvidence, channels map[string]ChannelState, calibration Calibration) ([]CandidateScore, error) {
	if err := calibration.Validate(); err != nil {
		return nil, err
	}
	embeddingHealthy := channels["embedding"].Status == ChannelHealthy
	treeHealthy := channels["tree"].Status == ChannelHealthy
	if !embeddingHealthy && !treeHealthy {
		return nil, ErrSemanticChannelsUnavailable
	}
	scores := make([]CandidateScore, 0, len(eligible))
	for _, candidate := range eligible {
		embeddingScore := 0.0
		negativeConflict := 0.0
		if evidence, ok := embeddings[candidate.ID]; ok && embeddingHealthy {
			embeddingScore = evidence.Score
			negativeConflict = evidence.NegativeConflict
		}
		treeScore := 0.0
		if evidence, ok := tree[candidate.ID]; ok && treeHealthy {
			treeScore = evidence.Score
		}
		fusionScore := 0.0
		switch {
		case embeddingHealthy && treeHealthy:
			fusionScore = calibration.Alpha*embeddingScore + (1-calibration.Alpha)*treeScore
		case embeddingHealthy:
			fusionScore = embeddingScore
		case treeHealthy:
			fusionScore = treeScore
		}
		scores = append(scores, CandidateScore{
			Candidate: candidate, EmbeddingScore: embeddingScore, TreeScore: treeScore,
			FusionScore: fusionScore, NegativeConflict: negativeConflict,
		})
	}
	sortCandidateScores(scores, func(score CandidateScore) float64 { return score.FusionScore })
	if len(scores) > calibration.FusionTopN {
		scores = scores[:calibration.FusionTopN]
	}
	return scores, nil
}

func Decide(scores []CandidateScore, reranked map[string]float64, channels map[string]ChannelState, calibration Calibration) (Decision, error) {
	if err := calibration.Validate(); err != nil {
		return Decision{}, err
	}
	if len(scores) == 0 {
		return Decision{}, errors.New("semantic fusion produced no eligible candidates")
	}
	embeddingHealthy := channels["embedding"].Status == ChannelHealthy
	treeHealthy := channels["tree"].Status == ChannelHealthy
	rerankerHealthy := channels["reranker"].Status == ChannelHealthy
	for index := range scores {
		rerankScore, ok := reranked[scores[index].Candidate.ID]
		if rerankerHealthy && ok {
			scores[index].RerankerScore = clamp01(rerankScore)
			scores[index].FinalScore = clamp01((1-calibration.RerankerWeight)*scores[index].FusionScore + calibration.RerankerWeight*scores[index].RerankerScore)
		} else {
			scores[index].FinalScore = scores[index].FusionScore
		}
	}
	sortCandidateScores(scores, func(score CandidateScore) float64 { return score.FinalScore })
	if len(scores) > 2 {
		scores = scores[:2]
	}
	degraded := !embeddingHealthy || !treeHealthy || !rerankerHealthy
	topScore := scores[0].FinalScore
	margin := topScore
	if len(scores) > 1 {
		margin -= scores[1].FinalScore
	}
	minimum, requiredMargin := calibration.ClearMinimum, calibration.ClearMargin
	if isMutation(scores[0].Candidate.Route.Operation) {
		minimum = max(minimum, calibration.MutationMinimum)
		requiredMargin = max(requiredMargin, calibration.MutationMargin)
	}
	if degraded {
		minimum = max(minimum, calibration.DegradedMinimum)
		requiredMargin = max(requiredMargin, calibration.DegradedMargin)
	}
	decision := Decision{Candidates: scores, Confidence: topScore, Margin: margin, Degraded: degraded}
	if isMutation(scores[0].Candidate.Route.Operation) && degraded {
		decision.Verdict, decision.ReasonCode = VerdictBlocked, "mutation_requires_healthy_semantic_pipeline"
		return decision, nil
	}
	switch {
	case topScore >= minimum && margin >= requiredMargin:
		decision.Verdict, decision.ReasonCode = VerdictClear, "top_candidate_clear"
	case topScore >= calibration.AmbiguousMinimum:
		decision.Verdict, decision.ReasonCode = VerdictAmbiguous, "top_candidates_compete"
	default:
		decision.Verdict, decision.ReasonCode = VerdictLow, "semantic_coverage_low"
	}
	return decision, nil
}

func sortCandidateScores(scores []CandidateScore, value func(CandidateScore) float64) {
	slices.SortFunc(scores, func(left, right CandidateScore) int {
		leftValue, rightValue := value(left), value(right)
		if leftValue > rightValue {
			return -1
		}
		if leftValue < rightValue {
			return 1
		}
		if left.Candidate.ID < right.Candidate.ID {
			return -1
		}
		if left.Candidate.ID > right.Candidate.ID {
			return 1
		}
		return 0
	})
}

func isMutation(operation app.RouteOperation) bool {
	return operation == "create" || operation == "edit" || operation == "transform" || operation == "delete" || operation == "interact"
}
