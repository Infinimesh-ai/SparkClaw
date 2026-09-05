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
	"regexp"
	"strings"
	"time"
)

const (
	maxControllerResponseBytes          = 64 << 10
	maxControllerExecutionResponseBytes = 10 << 20
)

type ControllerClient interface {
	ValidateToken(context.Context, string, []byte) (ValidationResult, error)
	Acquire(context.Context, AcquireRequest, []byte) (SessionLease, error)
	Execute(context.Context, ExecuteRequest) (ExecutionResult, error)
	Release(context.Context, ReleaseRequest) (ReleaseResult, error)
	RunScript(context.Context, RunScriptRequest, []byte) (ScriptExecutionResult, error)
	OpenProviderLogin(context.Context, OpenProviderLoginRequest) (OpenProviderLoginResult, error)
}

type Versions struct {
	Client            string `json:"client,omitempty"`
	ClientVersion     string `json:"client_version,omitempty"`
	PlaywrightVersion string `json:"playwright_version,omitempty"`
	BrowserChannel    string `json:"browser_channel,omitempty"`
	CLI               string `json:"cli,omitempty"`
	CLIVersion        string `json:"cli_version,omitempty"`
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

type AcquireRequest struct {
	ProfileID            string
	TaskID               string
	CredentialGeneration int64
	WaitTimeoutMS        int64
	SessionTTLMS         int64
}

type SessionLease struct {
	SchemaVersion        int       `json:"schema_version"`
	State                string    `json:"state"`
	ProfileID            string    `json:"profile_id"`
	Lane                 string    `json:"lane"`
	SessionID            string    `json:"session_id"`
	CredentialGeneration int64     `json:"credential_generation"`
	ControllerGeneration int64     `json:"controller_generation"`
	SessionGeneration    int64     `json:"session_generation"`
	PageGeneration       int64     `json:"page_generation"`
	ExpiresAt            time.Time `json:"expires_at"`
}

type ExecuteRequest struct {
	Lease     SessionLease
	Operation string
	Arguments map[string]any
}

type ExecutionResult struct {
	SchemaVersion        int             `json:"schema_version"`
	State                string          `json:"state"`
	ProfileID            string          `json:"profile_id"`
	Lane                 string          `json:"lane"`
	SessionID            string          `json:"session_id"`
	CredentialGeneration int64           `json:"credential_generation"`
	ControllerGeneration int64           `json:"controller_generation"`
	SessionGeneration    int64           `json:"session_generation"`
	PageGeneration       int64           `json:"page_generation"`
	Operation            string          `json:"operation"`
	Result               json.RawMessage `json:"result"`
}

type ReleaseRequest struct {
	ProfileID            string
	SessionID            string
	ControllerGeneration int64
	SessionGeneration    int64
}

type ReleaseResult struct {
	SchemaVersion        int    `json:"schema_version"`
	State                string `json:"state"`
	ProfileID            string `json:"profile_id"`
	ControllerGeneration int64  `json:"controller_generation"`
	SessionGeneration    int64  `json:"session_generation"`
}

type RunScriptRequest struct {
	ProfileID            string
	TaskID               string
	CredentialGeneration int64
	Provider             string
	Operation            string
	ScriptID             string
	Revision             int
	Input                any
	WaitTimeoutMS        int64
}

type ScriptExecutionResult struct {
	SchemaVersion        int             `json:"schema_version"`
	State                string          `json:"state"`
	ProfileID            string          `json:"profile_id"`
	Lane                 string          `json:"lane"`
	Provider             string          `json:"provider"`
	Operation            string          `json:"operation"`
	ScriptID             string          `json:"script_id"`
	Revision             int             `json:"revision"`
	SourceChecksum       string          `json:"source_checksum"`
	CredentialGeneration int64           `json:"credential_generation"`
	ControllerGeneration int64           `json:"controller_generation"`
	SessionGeneration    int64           `json:"session_generation"`
	Result               json.RawMessage `json:"result"`
}

type OpenProviderLoginRequest struct {
	ProfileID     string
	TaskID        string
	Provider      string
	WaitTimeoutMS int64
}

type OpenProviderLoginResult struct {
	SchemaVersion        int    `json:"schema_version"`
	State                string `json:"state"`
	ProfileID            string `json:"profile_id"`
	Provider             string `json:"provider"`
	ControllerGeneration int64  `json:"controller_generation"`
	SessionGeneration    int64  `json:"session_generation"`
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
		http:       &http.Client{Transport: transport},
	}, nil
}

func (c *HTTPControllerClient) ValidateToken(ctx context.Context, profileID string, token []byte) (ValidationResult, error) {
	payload := struct {
		ProfileID string `json:"profile_id"`
		Token     string `json:"token"`
	}{ProfileID: profileID, Token: string(token)}
	var result ValidationResult
	if err := c.postJSON(ctx, "/v1/validate-token", payload, maxControllerResponseBytes, &result); err != nil {
		return ValidationResult{}, err
	}
	if result.SchemaVersion != 1 || result.State != "ready" || result.ProfileID != profileID ||
		result.ControllerGeneration <= 0 || result.SessionGeneration <= 0 || result.PageGeneration <= 0 {
		return ValidationResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller response contract is invalid"))
	}
	return result, nil
}

func (c *HTTPControllerClient) Acquire(ctx context.Context, input AcquireRequest, token []byte) (SessionLease, error) {
	payload := struct {
		ProfileID            string `json:"profile_id"`
		Lane                 string `json:"lane"`
		TaskID               string `json:"task_id"`
		CredentialGeneration int64  `json:"credential_generation"`
		Token                string `json:"token"`
		WaitTimeoutMS        int64  `json:"wait_timeout_ms,omitempty"`
		SessionTTLMS         int64  `json:"session_ttl_ms,omitempty"`
	}{
		ProfileID: input.ProfileID, Lane: "mcp", TaskID: input.TaskID,
		CredentialGeneration: input.CredentialGeneration, Token: string(token),
		WaitTimeoutMS: input.WaitTimeoutMS, SessionTTLMS: input.SessionTTLMS,
	}
	var result SessionLease
	if err := c.postJSON(ctx, "/v1/acquire", payload, maxControllerResponseBytes, &result); err != nil {
		return SessionLease{}, err
	}
	if result.SchemaVersion != 1 || result.State != "acquired" || result.ProfileID != input.ProfileID ||
		result.Lane != "mcp" || strings.TrimSpace(result.SessionID) == "" ||
		result.CredentialGeneration != input.CredentialGeneration || result.ControllerGeneration <= 0 ||
		result.SessionGeneration <= 0 || result.PageGeneration <= 0 || result.ExpiresAt.IsZero() {
		return SessionLease{}, invalidControllerResponse()
	}
	return result, nil
}

func (c *HTTPControllerClient) Execute(ctx context.Context, input ExecuteRequest) (ExecutionResult, error) {
	lease := input.Lease
	payload := struct {
		SessionID            string         `json:"session_id"`
		ControllerGeneration int64          `json:"controller_generation"`
		SessionGeneration    int64          `json:"session_generation"`
		PageGeneration       int64          `json:"page_generation"`
		Operation            string         `json:"operation"`
		Arguments            map[string]any `json:"arguments"`
	}{
		SessionID: lease.SessionID, ControllerGeneration: lease.ControllerGeneration,
		SessionGeneration: lease.SessionGeneration, PageGeneration: lease.PageGeneration,
		Operation: input.Operation, Arguments: input.Arguments,
	}
	var result ExecutionResult
	if err := c.postJSON(ctx, "/v1/execute", payload, maxControllerExecutionResponseBytes, &result); err != nil {
		return ExecutionResult{}, err
	}
	if result.SchemaVersion != 1 || result.State != "completed" || result.ProfileID != lease.ProfileID ||
		result.Lane != lease.Lane || result.SessionID != lease.SessionID ||
		result.CredentialGeneration != lease.CredentialGeneration ||
		result.ControllerGeneration != lease.ControllerGeneration ||
		result.SessionGeneration != lease.SessionGeneration || result.PageGeneration <= 0 ||
		result.Operation != input.Operation || len(result.Result) == 0 || !json.Valid(result.Result) {
		return ExecutionResult{}, invalidControllerResponse()
	}
	return result, nil
}

func (c *HTTPControllerClient) Release(ctx context.Context, input ReleaseRequest) (ReleaseResult, error) {
	payload := struct {
		SessionID            string `json:"session_id"`
		ControllerGeneration int64  `json:"controller_generation"`
		SessionGeneration    int64  `json:"session_generation"`
	}{input.SessionID, input.ControllerGeneration, input.SessionGeneration}
	var result ReleaseResult
	if err := c.postJSON(ctx, "/v1/release", payload, maxControllerResponseBytes, &result); err != nil {
		return ReleaseResult{}, err
	}
	if result.SchemaVersion != 1 || result.State != "released" || result.ProfileID != input.ProfileID ||
		result.ControllerGeneration != input.ControllerGeneration || result.SessionGeneration != input.SessionGeneration {
		return ReleaseResult{}, invalidControllerResponse()
	}
	return result, nil
}

func (c *HTTPControllerClient) RunScript(ctx context.Context, input RunScriptRequest, token []byte) (ScriptExecutionResult, error) {
	payload := struct {
		ProfileID            string `json:"profile_id"`
		TaskID               string `json:"task_id"`
		CredentialGeneration int64  `json:"credential_generation"`
		Token                string `json:"token"`
		Provider             string `json:"provider"`
		Operation            string `json:"operation"`
		ScriptID             string `json:"script_id"`
		Revision             int    `json:"revision"`
		Input                any    `json:"input"`
		WaitTimeoutMS        int64  `json:"wait_timeout_ms,omitempty"`
	}{
		ProfileID: input.ProfileID, TaskID: input.TaskID,
		CredentialGeneration: input.CredentialGeneration, Token: string(token),
		Provider: input.Provider, Operation: input.Operation, ScriptID: input.ScriptID,
		Revision: input.Revision, Input: input.Input, WaitTimeoutMS: input.WaitTimeoutMS,
	}
	var result ScriptExecutionResult
	if err := c.postJSON(ctx, "/v1/run-script", payload, maxControllerResponseBytes, &result); err != nil {
		return ScriptExecutionResult{}, err
	}
	if result.SchemaVersion != 1 || result.State != "completed" && result.State != "failed" ||
		result.ProfileID != input.ProfileID || result.Lane != "cli" ||
		result.Provider != input.Provider || result.Operation != input.Operation ||
		result.ScriptID != input.ScriptID || result.Revision != input.Revision ||
		!sourceChecksumPattern.MatchString(result.SourceChecksum) ||
		result.CredentialGeneration != input.CredentialGeneration ||
		result.ControllerGeneration <= 0 || result.SessionGeneration <= 0 ||
		len(result.Result) == 0 || !json.Valid(result.Result) {
		return ScriptExecutionResult{}, invalidControllerResponse()
	}
	return result, nil
}

func (c *HTTPControllerClient) OpenProviderLogin(ctx context.Context, input OpenProviderLoginRequest) (OpenProviderLoginResult, error) {
	payload := struct {
		ProfileID     string `json:"profile_id"`
		TaskID        string `json:"task_id"`
		Provider      string `json:"provider"`
		WaitTimeoutMS int64  `json:"wait_timeout_ms,omitempty"`
	}{input.ProfileID, input.TaskID, input.Provider, input.WaitTimeoutMS}
	var result OpenProviderLoginResult
	if err := c.postJSON(ctx, "/v1/open-provider-login", payload, maxControllerResponseBytes, &result); err != nil {
		return OpenProviderLoginResult{}, err
	}
	if result.SchemaVersion != 1 || result.State != "opened" ||
		result.ProfileID != input.ProfileID || result.Provider != input.Provider ||
		result.ControllerGeneration <= 0 || result.SessionGeneration <= 0 {
		return OpenProviderLoginResult{}, invalidControllerResponse()
	}
	return result, nil
}

// controllerRequestBackstop bounds a controller round trip whose caller
// carries no deadline. Provider scripts run for up to 90s plus a bounded
// profile wait, so this only catches a wedged controller process.
const controllerRequestBackstop = 5 * time.Minute

func (c *HTTPControllerClient) postJSON(ctx context.Context, route string, input any, maximum int64, output any) error {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, controllerRequestBackstop)
		defer cancel()
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return newError(CodeInvalidRequest, false, err)
	}
	defer zero(payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://browser-controller.local"+route, bytes.NewReader(payload))
	if err != nil {
		return newError(CodeInvalidRequest, false, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return newError(CodeControllerUnavailable, true, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil || int64(len(body)) > maximum {
		zero(body)
		return newError(CodeControllerUnavailable, true, errors.New("browser controller response is unavailable"))
	}
	defer zero(body)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return mapControllerFailure(response.StatusCode, body)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return invalidControllerResponse()
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return invalidControllerResponse()
	}
	return nil
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
	case "browser_controller_stale":
		return newError(CodeControllerStale, false, errors.New("controller generation changed"))
	case "browser_session_not_found":
		return newError(CodeSessionNotFound, false, errors.New("controller session was not found"))
	case "browser_session_stale", "browser_session_invalid":
		return newError(CodeSessionStale, false, errors.New("controller session is stale"))
	case "browser_page_stale":
		return newError(CodePageStale, false, errors.New("controller page generation is stale"))
	case "browser_operation_unavailable":
		return newError(CodeOperationUnavailable, false, errors.New("controller operation is unavailable"))
	case "browser_script_unavailable":
		return newError(CodeScriptUnavailable, false, errors.New("controller provider script is unavailable"))
	case "browser_script_timeout":
		return newError(CodeScriptTimeout, true, errors.New("controller provider script timed out"))
	case "browser_lane_unavailable":
		return newError(CodeOperationUnavailable, false, errors.New("controller lane is unavailable"))
	case "browser_controller_stopping":
		return newError(CodeControllerUnavailable, true, errors.New("controller is stopping"))
	default:
		return newError(CodeControllerUnavailable, status >= 500 || projected.Retryable, fmt.Errorf("browser controller returned status %d", status))
	}
}

var sourceChecksumPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func invalidControllerResponse() error {
	return newError(CodeControllerUnavailable, true, errors.New("browser controller response contract is invalid"))
}

func zero(value []byte) {
	for i := range value {
		value[i] = 0
	}
}
