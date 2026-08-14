package infinimeshinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultBaseURL           = "https://info.infinimesh.cloud"
	ProviderName             = "infinimesh-info"
	TokenTypeBasic TokenType = "info.basic"
)

type TokenType string

type Config struct {
	BaseURL              string
	LicenseID            string
	LicenseKey           string
	TokenBatchSize       int
	MaxAttempts          int
	RetryBaseDelay       time.Duration
	RequestTimeout       time.Duration
	ResponseBodyMaxBytes int64
}

func (cfg Config) Configured() bool {
	licenseID := strings.TrimSpace(cfg.LicenseID)
	keyLicenseID, ok := ParseLicenseKeyLicenseID(cfg.LicenseKey)
	return licenseID != "" && ok && keyLicenseID == licenseID
}

// ParseLicenseKeyLicenseID extracts the license ID embedded in an Info license
// key. Possession is verified by the Info service; SparkClaw only validates the
// public wire shape and guards against pairing a key with the wrong license.
func ParseLicenseKeyLicenseID(key string) (string, bool) {
	const prefix = "ilk_v1."
	key = strings.TrimSpace(key)
	if !strings.HasPrefix(key, prefix) {
		return "", false
	}
	rest := key[len(prefix):]
	dot := strings.LastIndexByte(rest, '.')
	if dot <= 0 || dot == len(rest)-1 {
		return "", false
	}
	return rest[:dot], true
}

type Token struct {
	Value     string
	Type      TokenType
	ExpiresAt time.Time
}

type TokenIssuer interface {
	Issue(ctx context.Context, tokenType TokenType, count int) ([]Token, error)
}

type TokenWallet interface {
	Reserve(ctx context.Context, tokenType TokenType) (string, error)
	DiscardAll(tokenType TokenType)
}

type QueryRequest struct {
	Query      string
	TaskType   string
	Freshness  string
	MaxSources int
	Language   string
}

type QueryResponse struct {
	RequestID     string        `json:"request_id"`
	Status        string        `json:"status"`
	AnswerContext AnswerContext `json:"answer_context"`
	Sources       []Source      `json:"sources"`
	Usage         Usage         `json:"usage"`
}

func (response *QueryResponse) UnmarshalJSON(data []byte) error {
	type responseAlias QueryResponse
	var decoded responseAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if err := requireJSONMembers(envelope, "request_id", "status", "answer_context", "sources", "usage"); err != nil {
		return err
	}
	var answer map[string]json.RawMessage
	if err := json.Unmarshal(envelope["answer_context"], &answer); err != nil || answer == nil {
		return errors.New("Info answer_context must be an object")
	}
	if err := requireJSONMembers(answer, "summary", "key_facts", "freshness"); err != nil {
		return err
	}
	if err := validateAnswerContextMembers(answer); err != nil {
		return err
	}
	var sources []map[string]json.RawMessage
	if string(envelope["sources"]) != "null" {
		if err := json.Unmarshal(envelope["sources"], &sources); err != nil {
			return err
		}
	}
	for _, source := range sources {
		if source == nil {
			return errors.New("Info source must be an object")
		}
		if err := requireJSONMembers(source, "id", "title", "url", "source_type", "retrieved_at", "authority_score"); err != nil {
			return err
		}
	}
	var usage map[string]json.RawMessage
	if err := json.Unmarshal(envelope["usage"], &usage); err != nil || usage == nil {
		return errors.New("Info usage must be an object")
	}
	if err := requireJSONMembers(usage, "cost_credits", "token_type"); err != nil {
		return err
	}
	*response = QueryResponse(decoded)
	return nil
}

func validateAnswerContextMembers(answer map[string]json.RawMessage) error {
	var facts []map[string]json.RawMessage
	if string(answer["key_facts"]) != "null" {
		if err := json.Unmarshal(answer["key_facts"], &facts); err != nil {
			return err
		}
	}
	for _, fact := range facts {
		if fact == nil {
			return errors.New("Info key fact must be an object")
		}
		if err := requireJSONMembers(fact, "claim", "confidence", "sources"); err != nil {
			return err
		}
	}
	if raw, exists := answer["conflicts"]; exists && string(raw) != "null" {
		var conflicts []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &conflicts); err != nil {
			return err
		}
		for _, conflict := range conflicts {
			if conflict == nil {
				return errors.New("Info conflict must be an object")
			}
			if err := requireJSONMembers(conflict, "topic", "viewpoints"); err != nil {
				return err
			}
			var viewpoints []map[string]json.RawMessage
			if string(conflict["viewpoints"]) != "null" {
				if err := json.Unmarshal(conflict["viewpoints"], &viewpoints); err != nil {
					return err
				}
			}
			for _, viewpoint := range viewpoints {
				if viewpoint == nil {
					return errors.New("Info conflict viewpoint must be an object")
				}
				if err := requireJSONMembers(viewpoint, "claim", "sources"); err != nil {
					return err
				}
			}
		}
	}
	var freshness map[string]json.RawMessage
	if err := json.Unmarshal(answer["freshness"], &freshness); err != nil || freshness == nil {
		return errors.New("Info freshness must be an object")
	}
	return requireJSONMembers(freshness, "status", "staleness_risk")
}

func requireJSONMembers(object map[string]json.RawMessage, members ...string) error {
	for _, member := range members {
		if _, ok := object[member]; !ok {
			return errors.New("Info response is missing required member " + member)
		}
	}
	return nil
}

type AnswerContext struct {
	Summary                string          `json:"summary"`
	KeyFacts               []KeyFact       `json:"key_facts"`
	Conflicts              []Conflict      `json:"conflicts,omitempty"`
	Freshness              FreshnessStatus `json:"freshness"`
	RecommendedNextActions []string        `json:"recommended_next_actions,omitempty"`
	Uncertainty            []string        `json:"uncertainty,omitempty"`
}

type KeyFact struct {
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence"`
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

type FreshnessStatus struct {
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
	Snippets       []string `json:"snippets"`
}

type Usage struct {
	CostCredits int    `json:"cost_credits"`
	TokenType   string `json:"token_type"`
	CacheHit    bool   `json:"cache_hit"`
}

type Client struct {
	baseURL     string
	cfg         Config
	httpClient  *http.Client
	wallet      TokenWallet
	randomID    func() (string, error)
	retryJitter func(time.Duration) time.Duration
}
