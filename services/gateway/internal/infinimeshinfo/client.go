package infinimeshinfo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	issueTokensPath = "/v1/info/tokens/issue"
	queryPath       = "/v1/info/query"
)

type httpTokenIssuer struct {
	client *Client
}

type issueTokensRequest struct {
	DeviceAttestation    string           `json:"device_attestation"`
	LicenseProof         string           `json:"license_proof"`
	Epoch                string           `json:"epoch"`
	TokenMode            string           `json:"token_mode"`
	RequestedTokens      []requestedToken `json:"requested_tokens"`
	BlindedTokenRequests []any            `json:"blinded_token_requests"`
}

type requestedToken struct {
	Type  TokenType `json:"type"`
	Count int       `json:"count"`
}

type issueTokensResponse struct {
	Epoch          string         `json:"epoch"`
	IssuedTokens   []issuedToken  `json:"issued_tokens"`
	QuotaRemaining map[string]int `json:"quota_remaining"`
}

type issuedToken struct {
	Type      TokenType `json:"type"`
	TokenMode string    `json:"token_mode"`
	Token     string    `json:"token"`
	ExpiresAt string    `json:"expires_at"`
}

type infoQueryRequest struct {
	RequestID     string             `json:"request_id"`
	Product       string             `json:"product"`
	TaskType      string             `json:"task_type"`
	Query         string             `json:"query"`
	ContextPolicy queryContextPolicy `json:"context_policy"`
	Requirements  queryRequirements  `json:"requirements"`
}

type queryContextPolicy struct {
	IncludePrivateContext bool    `json:"include_private_context"`
	LocalContextSummary   *string `json:"local_context_summary"`
}

type queryRequirements struct {
	Freshness        string `json:"freshness"`
	CitationRequired bool   `json:"citation_required"`
	MaxSources       int    `json:"max_sources"`
	Language         string `json:"language"`
	ResponseMode     string `json:"response_mode"`
}

type errorEnvelope struct {
	RequestID string `json:"request_id"`
	Error     struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Retryable bool           `json:"retryable"`
		Details   map[string]any `json:"details"`
	} `json:"error"`
}

type APIError struct {
	Endpoint   string
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *APIError) Error() string {
	code := strings.TrimSpace(e.Code)
	if code == "" {
		code = "HTTP_ERROR"
	}
	return fmt.Sprintf("infinimesh info %s failed: %s (HTTP %d)", e.Endpoint, code, e.StatusCode)
}

type TransportError struct {
	Endpoint string
	Cause    error
}

func (e *TransportError) Error() string {
	return "infinimesh info " + e.Endpoint + " transport failed"
}

func (e *TransportError) Unwrap() error {
	return e.Cause
}

func NewClient(cfg Config, httpClient *http.Client) (*Client, error) {
	cfg = normalizeClientConfig(cfg)
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("infinimesh info base URL must be an absolute HTTP(S) URL")
	}
	if !cfg.Configured() {
		return nil, errors.New("infinimesh info credentials are not configured")
	}
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	clientCopy := *httpClient
	clientCopy.Timeout = cfg.RequestTimeout
	client := &Client{
		baseURL:    baseURL,
		cfg:        cfg,
		httpClient: &clientCopy,
		randomID:   randomRequestID,
		retryJitter: func(delay time.Duration) time.Duration {
			var b [1]byte
			if _, err := rand.Read(b[:]); err != nil {
				return delay
			}
			return delay + time.Duration(int64(delay)*int64(b[0])/510)
		},
	}
	client.wallet = NewTokenWallet(httpTokenIssuer{client: client}, cfg.TokenBatchSize)
	return client, nil
}

func normalizeClientConfig(cfg Config) Config {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	cfg.EntitlementProof = strings.TrimSpace(cfg.EntitlementProof)
	cfg.DeviceAttestation = strings.TrimSpace(cfg.DeviceAttestation)
	cfg.LicenseProof = strings.TrimSpace(cfg.LicenseProof)
	if cfg.TokenBatchSize <= 0 {
		cfg.TokenBatchSize = 10
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.MaxAttempts > 5 {
		cfg.MaxAttempts = 5
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = 200 * time.Millisecond
	}
	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = 30 * time.Second
	}
	if cfg.ResponseBodyMaxBytes <= 0 {
		cfg.ResponseBodyMaxBytes = 4 << 20
	}
	return cfg
}

func (c *Client) Query(ctx context.Context, request QueryRequest) (QueryResponse, error) {
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" {
		return QueryResponse{}, errors.New("infinimesh info query cannot be empty")
	}
	if strings.TrimSpace(request.TaskType) == "" {
		request.TaskType = "general_research"
	}
	if strings.TrimSpace(request.Freshness) == "" {
		request.Freshness = "medium"
	}
	if request.MaxSources <= 0 {
		request.MaxSources = 8
	}
	if strings.TrimSpace(request.Language) == "" {
		request.Language = "zh-CN"
	}

	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return QueryResponse{}, err
		}
		token, err := c.wallet.Reserve(ctx, TokenTypeBasic)
		if err != nil {
			return QueryResponse{}, err
		}
		response, err := c.queryOnce(ctx, token, request)
		if err == nil {
			return response, nil
		}
		lastErr = err
		if apiErrorCode(err) == "TOKEN_EXPIRED" {
			c.wallet.DiscardAll(TokenTypeBasic)
		}
		if attempt == c.cfg.MaxAttempts || !retryableQueryError(err) {
			break
		}
		if err := c.waitForRetry(ctx, attempt); err != nil {
			return QueryResponse{}, err
		}
	}
	return QueryResponse{}, lastErr
}

func apiErrorCode(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return ""
}

func (c *Client) queryOnce(ctx context.Context, token string, request QueryRequest) (QueryResponse, error) {
	requestID, err := c.randomID()
	if err != nil {
		return QueryResponse{}, errors.New("infinimesh info query request ID generation failed")
	}
	payload := infoQueryRequest{
		RequestID: requestID,
		Product:   "sparkclaw",
		TaskType:  request.TaskType,
		Query:     request.Query,
		ContextPolicy: queryContextPolicy{
			IncludePrivateContext: false,
			LocalContextSummary:   nil,
		},
		Requirements: queryRequirements{
			Freshness:        request.Freshness,
			CitationRequired: true,
			MaxSources:       request.MaxSources,
			Language:         request.Language,
			ResponseMode:     "agent_context",
		},
	}
	var response QueryResponse
	if err := c.postJSON(ctx, queryPath, "PrivateToken "+token, requestID, payload, &response); err != nil {
		return QueryResponse{}, err
	}
	if strings.TrimSpace(response.Status) != "ok" {
		return QueryResponse{}, errors.New("infinimesh info query returned an invalid status")
	}
	if response.RequestID != requestID {
		return QueryResponse{}, errors.New("infinimesh info query returned a mismatched request ID")
	}
	return response, nil
}

func (i httpTokenIssuer) Issue(ctx context.Context, tokenType TokenType, count int) ([]Token, error) {
	if tokenType != TokenTypeBasic {
		return nil, errUnsupportedTokenType
	}
	requestID, err := i.client.randomID()
	if err != nil {
		return nil, errors.New("infinimesh info token request ID generation failed")
	}
	payload := issueTokensRequest{
		DeviceAttestation: i.client.cfg.DeviceAttestation,
		LicenseProof:      i.client.cfg.LicenseProof,
		Epoch:             time.Now().UTC().Format("2006-01-02"),
		TokenMode:         "internal_opaque",
		RequestedTokens: []requestedToken{{
			Type:  tokenType,
			Count: count,
		}},
		BlindedTokenRequests: []any{},
	}
	var response issueTokensResponse
	if err := i.client.postJSON(
		ctx,
		issueTokensPath,
		"Bearer "+i.client.cfg.EntitlementProof,
		requestID,
		payload,
		&response,
	); err != nil {
		return nil, err
	}
	tokens := make([]Token, 0, len(response.IssuedTokens))
	for _, issued := range response.IssuedTokens {
		if issued.Type != tokenType || issued.TokenMode != "internal_opaque" {
			continue
		}
		expiresAt, err := time.Parse(time.RFC3339, issued.ExpiresAt)
		if err != nil {
			continue
		}
		tokens = append(tokens, Token{Value: issued.Token, Type: issued.Type, ExpiresAt: expiresAt})
	}
	return tokens, nil
}

func (c *Client) postJSON(ctx context.Context, endpoint, authorization, requestID string, payload, response any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return errors.New("infinimesh info " + endpoint + " request encoding failed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("infinimesh info " + endpoint + " request construction failed")
	}
	req.Header.Set("Authorization", authorization)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-Id", requestID)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &TransportError{Endpoint: endpoint, Cause: err}
	}
	defer resp.Body.Close()
	body, err := readBounded(resp.Body, c.cfg.ResponseBodyMaxBytes)
	if err != nil {
		return errors.New("infinimesh info " + endpoint + " response read failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(endpoint, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, response); err != nil {
		return errors.New("infinimesh info " + endpoint + " response decoding failed")
	}
	return nil
}

func readBounded(reader io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("response body exceeds configured limit")
	}
	return body, nil
}

func decodeAPIError(endpoint string, statusCode int, body []byte) error {
	var envelope errorEnvelope
	_ = json.Unmarshal(body, &envelope)
	return &APIError{
		Endpoint:   endpoint,
		StatusCode: statusCode,
		Code:       envelope.Error.Code,
		Message:    envelope.Error.Message,
		Retryable:  envelope.Error.Retryable,
	}
}

func retryableQueryError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.Retryable {
			return true
		}
		switch apiErr.Code {
		case "TOKEN_INVALID", "TOKEN_EXPIRED", "TOKEN_REDEEMED", "REQUEST_TIMEOUT", "RATE_LIMITED", "UPSTREAM_ERROR", "SERVICE_DEGRADED", "INTERNAL_ERROR":
			return true
		}
		return apiErr.StatusCode == http.StatusRequestTimeout ||
			apiErr.StatusCode == http.StatusTooManyRequests ||
			apiErr.StatusCode >= http.StatusInternalServerError
	}
	var transportErr *TransportError
	return errors.As(err, &transportErr)
}

func (c *Client) waitForRetry(ctx context.Context, attempt int) error {
	delay := c.cfg.RetryBaseDelay * time.Duration(1<<(attempt-1))
	if delay > 5*time.Second {
		delay = 5 * time.Second
	}
	if c.retryJitter != nil {
		delay = c.retryJitter(delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomRequestID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
