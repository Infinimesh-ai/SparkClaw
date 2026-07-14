package infinimeshinfo

import (
	"context"
	"net/http"
	"time"
)

const (
	DefaultBaseURL           = "https://info.infinimesh.cn"
	ProviderName             = "infinimesh-info"
	TokenTypeBasic TokenType = "info.basic"
)

type TokenType string

type Config struct {
	BaseURL              string
	EntitlementProof     string
	DeviceAttestation    string
	LicenseProof         string
	TokenBatchSize       int
	MaxAttempts          int
	RetryBaseDelay       time.Duration
	RequestTimeout       time.Duration
	ResponseBodyMaxBytes int64
}

func (cfg Config) Configured() bool {
	return cfg.EntitlementProof != "" && cfg.DeviceAttestation != "" && cfg.LicenseProof != ""
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

type AnswerContext struct {
	Summary   string    `json:"summary"`
	KeyFacts  []KeyFact `json:"key_facts"`
	Citations []string  `json:"citations"`
}

type KeyFact struct {
	Claim      string   `json:"claim"`
	Confidence string   `json:"confidence"`
	Sources    []string `json:"sources"`
}

type Source struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	SourceType     string   `json:"source_type"`
	PublishedAt    string   `json:"published_at"`
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
