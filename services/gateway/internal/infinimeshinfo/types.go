package infinimeshinfo

import (
	"context"
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
