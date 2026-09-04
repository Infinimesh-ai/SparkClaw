package browsercontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const maxControllerResponseBytes = 64 << 10

type ControllerClient interface {
	ValidateToken(context.Context, string, []byte) (ValidationResult, error)
}

type Versions struct {
	Client            string `json:"client,omitempty"`
	ClientVersion     string `json:"client_version,omitempty"`
	PlaywrightVersion string `json:"playwright_version,omitempty"`
	BrowserChannel    string `json:"browser_channel,omitempty"`
}

type ValidationResult struct {
	SchemaVersion        int      `json:"schema_version"`
	State                string   `json:"state"`
	ProfileID            string   `json:"profile_id"`
	ControllerGeneration int64    `json:"controller_generation"`
	SessionGeneration    int64    `json:"session_generation"`
	PageGeneration       int64    `json:"page_generation"`
	Versions             Versions `json:"versions"`
}

type HTTPControllerClient struct {
	socketPath string
	http       *http.Client
}

func NewHTTPControllerClient(socketPath string, timeout time.Duration) (*HTTPControllerClient, error) {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" || !filepath.IsAbs(socketPath) {
		return nil, errors.New("browser controller socket must be an absolute path")
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		},
		DisableCompression: true,
	}
	return &HTTPControllerClient{
		socketPath: socketPath,
		http:       &http.Client{Transport: transport, Timeout: timeout},
	}, nil
}

func (c *HTTPControllerClient) ValidateToken(ctx context.Context, profileID string, token []byte) (ValidationResult, error) {
	payload, err := json.Marshal(struct {
		ProfileID string `json:"profile_id"`
		Token     string `json:"token"`
	}{ProfileID: profileID, Token: string(token)})
	if err != nil {
		return ValidationResult{}, newError(CodeInvalidRequest, false, err)
	}
	defer zero(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://browser-controller.local/v1/validate-token", bytes.NewReader(payload))
	if err != nil {
		return ValidationResult{}, newError(CodeInvalidRequest, false, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ValidationResult{}, newError(CodeControllerUnavailable, true, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxControllerResponseBytes+1))
	if err != nil || len(body) > maxControllerResponseBytes {
		zero(body)
		return ValidationResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller response is unavailable"))
	}
	defer zero(body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ValidationResult{}, mapControllerFailure(response.StatusCode, body)
	}
	var result ValidationResult
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return ValidationResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller response is invalid"))
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ValidationResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller response has trailing data"))
	}
	if result.SchemaVersion != 1 || result.State != "ready" || result.ProfileID != profileID ||
		result.ControllerGeneration <= 0 || result.SessionGeneration <= 0 || result.PageGeneration <= 0 {
		return ValidationResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller response contract is invalid"))
	}
	return result, nil
}

func (c *HTTPControllerClient) Close() {
	if transport, ok := c.http.Transport.(*http.Transport); ok {
		transport.CloseIdleConnections()
	}
}

func mapControllerFailure(status int, body []byte) error {
	var projected struct {
		Code      string `json:"code"`
		Retryable bool   `json:"retryable"`
	}
	_ = json.Unmarshal(body, &projected)
	switch projected.Code {
	case "browser_busy":
		return newError(CodeBusy, true, errors.New("controller reported a busy profile"))
	case "invalid_request":
		return newError(CodeInvalidRequest, false, errors.New("controller rejected the request contract"))
	case "browser_extension_rejected":
		return newError(CodeExtensionRejected, false, errors.New("controller rejected the extension credential"))
	case "browser_extension_unavailable":
		return newError(CodeExtensionUnavailable, true, errors.New("controller could not reach the browser extension"))
	default:
		return newError(CodeControllerUnavailable, status >= 500 || projected.Retryable, fmt.Errorf("browser controller returned status %d", status))
	}
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
