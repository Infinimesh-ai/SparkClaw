package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const InfoResultSchemaVersion = 2

type Adapter interface {
	Search(ctx context.Context, request Request) (Result, error)
}

type Request struct {
	Query      string
	MaxResults int
	Freshness  string
}

// Result is the persisted Info aggregate. Answer-bearing data has one owner
// under Aggregate; Sources remains separate for ordered browser consumption.
type Result struct {
	SchemaVersion int       `json:"schema_version"`
	RequestID     string    `json:"request_id"`
	Status        string    `json:"status"`
	Query         string    `json:"query"`
	Provider      string    `json:"provider"`
	RetrievedAt   string    `json:"retrieved_at"`
	TookMS        int64     `json:"took_ms,omitempty"`
	Aggregate     Aggregate `json:"aggregate"`
	Sources       []Source  `json:"sources"`
	Usage         Usage     `json:"usage"`
	Untrusted     bool      `json:"untrusted"`

	legacy bool
}

type Aggregate struct {
	Summary                string     `json:"summary"`
	Facts                  []Fact     `json:"facts"`
	Conflicts              []Conflict `json:"conflicts,omitempty"`
	Freshness              Freshness  `json:"freshness"`
	Uncertainty            []string   `json:"uncertainty,omitempty"`
	RecommendedNextActions []string   `json:"recommended_next_actions,omitempty"`
}

type Fact struct {
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence,omitempty"`
	Sources    []string `json:"sources"`
}

type Conflict struct {
	Topic      string      `json:"topic"`
	Viewpoints []Viewpoint `json:"viewpoints"`
}

type Viewpoint struct {
	Claim   string   `json:"claim"`
	Sources []string `json:"sources"`
}

type Freshness struct {
	Status           string  `json:"status"`
	LatestSourceDate *string `json:"latest_source_date,omitempty"`
	StalenessRisk    string  `json:"staleness_risk"`
}

type Source struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	SourceType     string   `json:"source_type"`
	PublishedAt    *string  `json:"published_at,omitempty"`
	RetrievedAt    string   `json:"retrieved_at"`
	AuthorityScore float64  `json:"authority_score"`
	Snippets       []string `json:"snippets,omitempty"`

	evidenceIndex    int
	evidenceIndexSet bool
}

type Usage struct {
	CostCredits int    `json:"cost_credits"`
	TokenType   string `json:"token_type"`
	CacheHit    bool   `json:"cache_hit,omitempty"`
}

func (r Result) Legacy() bool {
	return r.legacy
}

// DecodeResult accepts the current versioned result and the single legacy
// persisted shape. Producers must write only Result schema version 2.
func DecodeResult(value any) (Result, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return Result{}, errors.New("Info result encoding failed")
	}
	var envelope struct {
		SchemaVersion int    `json:"schema_version"`
		Provider      string `json:"provider"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return Result{}, errors.New("Info result decoding failed")
	}
	if strings.TrimSpace(envelope.Provider) != InfoProviderName {
		return Result{}, errors.New("Info result provider is unsupported")
	}
	switch envelope.SchemaVersion {
	case InfoResultSchemaVersion:
		var result Result
		if err := json.Unmarshal(raw, &result); err != nil {
			return Result{}, errors.New("Info result v2 decoding failed")
		}
		for index := range result.Sources {
			result.Sources[index].evidenceIndex = index
			result.Sources[index].evidenceIndexSet = true
		}
		return result, nil
	case 0:
		return decodeLegacyResult(raw)
	default:
		return Result{}, fmt.Errorf("Info result schema version %d is unsupported", envelope.SchemaVersion)
	}
}

type legacyResult struct {
	RequestID   string          `json:"request_id"`
	Query       string          `json:"query"`
	Summary     string          `json:"summary"`
	Provider    string          `json:"provider"`
	Results     []legacyItem    `json:"results"`
	KeyFacts    []legacyKeyFact `json:"key_facts"`
	RetrievedAt string          `json:"retrieved_at"`
	TookMS      int64           `json:"took_ms,omitempty"`
	Untrusted   bool            `json:"untrusted"`
}

type legacyItem struct {
	EvidenceIndex  int      `json:"evidence_index"`
	ID             string   `json:"id,omitempty"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	Snippet        string   `json:"snippet"`
	Snippets       []string `json:"snippets"`
	Source         string   `json:"source"`
	PublishedAt    string   `json:"published_at"`
	RetrievedAt    string   `json:"retrieved_at,omitempty"`
	AuthorityScore float64  `json:"authority_score,omitempty"`
}

type legacyKeyFact struct {
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence,omitempty"`
	Sources    []string `json:"sources,omitempty"`
}

func decodeLegacyResult(raw []byte) (Result, error) {
	var legacy legacyResult
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return Result{}, errors.New("legacy Info result decoding failed")
	}
	result := Result{
		SchemaVersion: InfoResultSchemaVersion,
		RequestID:     legacy.RequestID,
		Status:        "ok",
		Query:         legacy.Query,
		Provider:      legacy.Provider,
		RetrievedAt:   legacy.RetrievedAt,
		TookMS:        legacy.TookMS,
		Aggregate:     Aggregate{Summary: legacy.Summary},
		Untrusted:     legacy.Untrusted,
		legacy:        true,
	}
	IDsByURL := map[string]string{}
	for index, source := range legacy.Results {
		id := strings.TrimSpace(source.ID)
		if id == "" {
			id = fmt.Sprintf("legacy-source:%d", index)
		}
		publishedAt := strings.TrimSpace(source.PublishedAt)
		var publishedAtPointer *string
		if publishedAt != "" {
			publishedAtPointer = &publishedAt
		}
		snippets := append([]string(nil), source.Snippets...)
		if len(snippets) == 0 && strings.TrimSpace(source.Snippet) != "" {
			snippets = []string{source.Snippet}
		}
		result.Sources = append(result.Sources, Source{
			ID: id, Title: source.Title, URL: source.URL, SourceType: source.Source,
			PublishedAt: publishedAtPointer, RetrievedAt: source.RetrievedAt,
			AuthorityScore: source.AuthorityScore, Snippets: snippets,
			evidenceIndex: source.EvidenceIndex, evidenceIndexSet: true,
		})
		if rawURL := strings.TrimSpace(source.URL); rawURL != "" {
			IDsByURL[rawURL] = id
		}
	}
	for _, fact := range legacy.KeyFacts {
		sourceIDs := make([]string, 0, len(fact.Sources))
		for _, sourceRef := range fact.Sources {
			sourceRef = strings.TrimSpace(sourceRef)
			if sourceID := IDsByURL[sourceRef]; sourceID != "" {
				sourceRef = sourceID
			}
			sourceIDs = append(sourceIDs, sourceRef)
		}
		result.Aggregate.Facts = append(result.Aggregate.Facts, Fact{
			Claim: fact.Claim, Confidence: fact.Confidence, Sources: sourceIDs,
		})
	}
	return result, nil
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

func NewAdapter(cfg config.Config) Adapter {
	provider := strings.ToLower(strings.TrimSpace(cfg.Tools.Web.Search.Provider))
	switch provider {
	case "", InfoProviderName:
		adapter, err := NewInfinimeshInfoAdapter(cfg.Plugins.Entries.InfinimeshInfo.Config, nil)
		if err != nil {
			return disabledAdapter{reason: err.Error()}
		}
		return adapter
	default:
		return disabledAdapter{reason: "unsupported web search provider: " + provider}
	}
}

type disabledAdapter struct {
	reason string
}

func (a disabledAdapter) Search(context.Context, Request) (Result, error) {
	if strings.TrimSpace(a.reason) == "" {
		return Result{}, errors.New("web search adapter is disabled")
	}
	return Result{}, errors.New(a.reason)
}
