package emailautomation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
)

const (
	maxProbeInputBytes   = 16 << 10
	maxSendInputBytes    = 256 << 10
	maxScriptOutputBytes = 64 << 10
	maxRecipientBytes    = 320
	maxSubjectRunes      = 998
	maxBodyBytes         = 200 << 10
)

var (
	invocationIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
	scriptCodePattern   = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	recipientPattern    = regexp.MustCompile(`^[^\s@<>]+@[^\s@<>]+\.[^\s@<>]+$`)
)

type ProbeResult struct {
	Provider    string
	AccountHint string
	Generation  uint64
	Revision    int
	CheckedAt   time.Time
}

type SendRequest = app.EmailSendRequest
type SendResult = app.EmailSendResult

type ScriptRunner interface {
	Probe(context.Context, Provider, string, uint64) (ProbeResult, error)
	Send(context.Context, Provider, SendRequest) (SendResult, error)
}

type Runner struct {
	browser             BrowserController
	agentBrowserCommand string
	now                 func() time.Time
	mu                  sync.Mutex
}

func NewRunner(browser BrowserController, agentBrowserCommand string) *Runner {
	return &Runner{browser: browser, agentBrowserCommand: strings.TrimSpace(agentBrowserCommand), now: func() time.Time { return time.Now().UTC() }}
}

func (r *Runner) Probe(ctx context.Context, provider Provider, invocationID string, expectedGeneration uint64) (ProbeResult, error) {
	if !invocationIDPattern.MatchString(invocationID) {
		return ProbeResult{}, codedError(CodeInvalidInput, "Email probe invocation is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, err := r.headlessEndpoint(ctx, expectedGeneration)
	if err != nil {
		return ProbeResult{}, err
	}
	input := struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		InvocationID  string `json:"invocation_id"`
		Provider      string `json:"provider"`
		Account       string `json:"account"`
	}{1, "probe", invocationID, provider.ID, app.EmailAccountDefault}
	stdout, stderr, err := r.execute(ctx, provider.Probe, endpoint, invocationID, input, maxProbeInputBytes, false)
	if err != nil {
		return ProbeResult{}, r.scriptFailure(provider, stderr, err, false)
	}
	var output struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		Provider      string `json:"provider"`
		AccountHint   string `json:"account_hint,omitempty"`
	}
	if err := decodeStrictJSON(stdout, &output); err != nil || output.SchemaVersion != 1 || output.Status != "ready" || output.Provider != provider.ID || !validAccountHint(output.AccountHint) {
		return ProbeResult{}, codedError(CodeScriptInvalidOutput, "Email login probe returned an invalid result")
	}
	return ProbeResult{
		Provider: provider.ID, AccountHint: output.AccountHint, Generation: endpoint.Generation,
		Revision: provider.Probe.Revision, CheckedAt: r.now(),
	}, nil
}

func (r *Runner) Send(ctx context.Context, provider Provider, request SendRequest) (SendResult, error) {
	request.Provider = strings.ToLower(strings.TrimSpace(request.Provider))
	request.Account = strings.ToLower(strings.TrimSpace(request.Account))
	request.Recipient = strings.TrimSpace(request.Recipient)
	if request.Provider != provider.ID || request.Account != app.EmailAccountDefault || request.BrowserGeneration == 0 ||
		request.ProbeRevision != provider.Probe.Revision || request.ScriptRevision != provider.Send.Revision ||
		!invocationIDPattern.MatchString(request.InvocationID) {
		return SendResult{}, codedError(CodeInvalidInput, "Email send binding is invalid")
	}
	if err := validateMessage(request.Recipient, request.Subject, request.Body); err != nil {
		return SendResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	endpoint, err := r.headlessEndpoint(ctx, request.BrowserGeneration)
	if err != nil {
		return SendResult{}, err
	}
	input := struct {
		SchemaVersion int    `json:"schema_version"`
		Operation     string `json:"operation"`
		InvocationID  string `json:"invocation_id"`
		Provider      string `json:"provider"`
		Account       string `json:"account"`
		Message       struct {
			Recipient string `json:"recipient"`
			Subject   string `json:"subject"`
			Body      struct {
				Format  string `json:"format"`
				Content string `json:"content"`
			} `json:"body"`
		} `json:"message"`
	}{SchemaVersion: 1, Operation: "send", InvocationID: request.InvocationID, Provider: provider.ID, Account: app.EmailAccountDefault}
	input.Message.Recipient = request.Recipient
	input.Message.Subject = request.Subject
	input.Message.Body.Format = "text"
	input.Message.Body.Content = request.Body
	stdout, stderr, err := r.execute(ctx, provider.Send, endpoint, request.InvocationID, input, maxSendInputBytes, true)
	if err != nil {
		return SendResult{}, r.scriptFailure(provider, stderr, err, true)
	}
	var output struct {
		SchemaVersion     int    `json:"schema_version"`
		Status            string `json:"status"`
		Provider          string `json:"provider"`
		RecipientDigest   string `json:"recipient_digest"`
		ProviderMessageID string `json:"provider_message_id,omitempty"`
	}
	if err := decodeStrictJSON(stdout, &output); err != nil || output.SchemaVersion != 1 || output.Status != "sent" || output.Provider != provider.ID ||
		output.RecipientDigest != recipientDigest(request.Recipient) || !validOpaqueProviderID(output.ProviderMessageID) {
		return SendResult{}, codedError(CodeScriptInvalidOutput, "Email send script returned an invalid result")
	}
	return SendResult{
		Provider: provider.ID, Status: output.Status, RecipientDigest: output.RecipientDigest,
		ProviderMessageID: output.ProviderMessageID, BrowserGeneration: endpoint.Generation, ScriptRevision: provider.Send.Revision,
	}, nil
}

func (r *Runner) headlessEndpoint(ctx context.Context, expectedGeneration uint64) (browserautomation.HostCDPEndpoint, error) {
	if r.browser == nil {
		return browserautomation.HostCDPEndpoint{}, codedError(CodeProviderUnavailable, "Email browser automation is unavailable")
	}
	endpoint, err := r.browser.EnsureHeadless(ctx)
	if err != nil {
		return browserautomation.HostCDPEndpoint{}, err
	}
	if endpoint.Presentation != "headless" {
		return browserautomation.HostCDPEndpoint{}, codedError(CodeProviderUnavailable, "Email automation requires headless Chromium")
	}
	if expectedGeneration != 0 && endpoint.Generation != expectedGeneration {
		return browserautomation.HostCDPEndpoint{}, codedError(CodeAdmissionStale, "Email login admission is stale; check login status again")
	}
	return endpoint, nil
}

func (r *Runner) execute(ctx context.Context, script Script, endpoint browserautomation.HostCDPEndpoint, invocationID string, input any, maxInput int, send bool) ([]byte, []byte, error) {
	if len(script.Command) == 0 {
		return nil, nil, errors.New("email script command is missing")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	if len(payload) == 0 || len(payload) > maxInput {
		return nil, nil, codedError(CodeInvalidInput, "Email script input exceeds the configured limit")
	}
	if err := validateScriptFile(script.Command); err != nil {
		return nil, nil, err
	}
	timeout := script.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(execCtx, script.Command[0], script.Command[1:]...)
	configureScriptCommand(command)
	command.Env = r.scriptEnvironment(endpoint, invocationID)
	command.Stdin = bytes.NewReader(append(payload, '\n'))
	stdout := &boundedBuffer{limit: maxScriptOutputBytes}
	stderr := &boundedBuffer{limit: maxScriptOutputBytes}
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		return nil, nil, codedError(CodeProviderUnavailable, "Email provider script could not start")
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if stdout.overflowed() || stderr.overflowed() {
			return stdout.bytes(), stderr.bytes(), codedError(CodeScriptInvalidOutput, "Email provider script exceeded its output limit")
		}
		return stdout.bytes(), stderr.bytes(), err
	case <-execCtx.Done():
		terminateScriptCommand(command)
		<-done
		if send {
			return stdout.bytes(), stderr.bytes(), codedError(CodeSendOutcomeUnknown, "Email send outcome is unknown and must not be retried")
		}
		if ctx.Err() != nil {
			return stdout.bytes(), stderr.bytes(), ctx.Err()
		}
		return stdout.bytes(), stderr.bytes(), codedError(CodeScriptTimeout, "Email provider script timed out")
	}
}

func (r *Runner) scriptEnvironment(endpoint browserautomation.HostCDPEndpoint, invocationID string) []string {
	environment := make([]string, 0, len(os.Environ())+8)
	for _, item := range os.Environ() {
		key := item
		if index := strings.IndexByte(item, '='); index >= 0 {
			key = item[:index]
		}
		if strings.HasPrefix(key, "AGENT_BROWSER_") || key == "SPARKCLAW_AGENT_BROWSER" || key == "GMAIL_LOGIN_PROBE_AGENT_BROWSER_BIN" {
			continue
		}
		environment = append(environment, item)
	}
	sessionDigest := sha256.Sum256([]byte(invocationID))
	environment = append(environment,
		"AGENT_BROWSER_CDP="+browserautomation.HostCDPWebSocketURL(endpoint),
		"AGENT_BROWSER_SESSION=sc-email-"+hex.EncodeToString(sessionDigest[:8]),
		fmt.Sprintf("SPARKCLAW_EMAIL_BROWSER_GENERATION=%d", endpoint.Generation),
	)
	if r.agentBrowserCommand != "" {
		environment = append(environment,
			"SPARKCLAW_AGENT_BROWSER="+r.agentBrowserCommand,
			"GMAIL_LOGIN_PROBE_AGENT_BROWSER_BIN="+r.agentBrowserCommand,
		)
		path := os.Getenv("PATH")
		environment = append(environment, "PATH="+filepath.Dir(r.agentBrowserCommand)+string(os.PathListSeparator)+path)
	}
	return environment
}

func (r *Runner) scriptFailure(provider Provider, stderr []byte, executionErr error, send bool) error {
	if code := ErrorCode(executionErr); code != "" {
		return executionErr
	}
	var output struct {
		SchemaVersion int    `json:"schema_version"`
		Status        string `json:"status"`
		Provider      string `json:"provider"`
		Code          string `json:"code"`
		Message       string `json:"message,omitempty"`
	}
	if err := decodeStrictJSON(stderr, &output); err != nil || output.SchemaVersion != 1 ||
		(output.Status != "error" && output.Status != "failed") || output.Provider != provider.ID || !scriptCodePattern.MatchString(output.Code) {
		if send {
			return codedError(CodeSendOutcomeUnknown, "Email send outcome is unknown and must not be retried")
		}
		return codedError(CodeScriptInvalidOutput, "Email provider script returned an invalid error")
	}
	code := normalizeScriptErrorCode(output.Code)
	message := publicScriptErrorMessage(code)
	return codedError(code, message)
}

func validateMessage(recipient, subject, body string) error {
	if len(recipient) == 0 || len(recipient) > maxRecipientBytes || strings.ContainsAny(recipient, "\r\n\x00") || !recipientPattern.MatchString(recipient) {
		return codedError(CodeInvalidInput, "Recipient must be one valid email address")
	}
	if !utf8.ValidString(subject) || utf8.RuneCountInString(subject) > maxSubjectRunes || strings.ContainsAny(subject, "\r\n\x00") {
		return codedError(CodeInvalidInput, "Email subject must be one bounded line")
	}
	if !utf8.ValidString(body) || strings.TrimSpace(body) == "" || len(body) > maxBodyBytes || strings.ContainsRune(body, '\x00') {
		return codedError(CodeInvalidInput, "Email body must be non-empty bounded UTF-8 text")
	}
	return nil
}

func recipientDigest(recipient string) string {
	digest := sha256.Sum256([]byte(recipient))
	return "sha256:" + hex.EncodeToString(digest[:])
}

func validAccountHint(value string) bool {
	if value == "" {
		return true
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 64 || strings.ContainsAny(value, "\r\n\x00") {
		return false
	}
	separator := strings.LastIndex(value, "***@")
	if separator <= 0 || strings.Count(value, "***@") != 1 {
		return false
	}
	prefix, domain := value[:separator], value[separator+4:]
	return utf8.RuneCountInString(prefix) <= 2 && strings.TrimSpace(prefix) == prefix && domain != "" &&
		domain == strings.ToLower(domain) && !strings.ContainsAny(domain, " @/\\") && strings.Contains(domain, ".")
}

func validOpaqueProviderID(value string) bool {
	return value == "" || utf8.ValidString(value) && len(value) <= 256 && !strings.ContainsAny(value, "\r\n\x00")
}

func decodeStrictJSON(raw []byte, output any) error {
	if len(raw) == 0 || len(raw) > maxScriptOutputBytes {
		return errors.New("JSON output is empty or oversized")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("JSON output has a trailing value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func normalizeScriptErrorCode(code string) string {
	switch code {
	case CodeNotConfigured, CodeLoginRequired, CodeAccountAmbiguous, CodeProviderUnavailable,
		CodePageContractChanged, CodeInvalidInput, CodeDraftConflict, CodeDraftVerifyFailed,
		CodeSendControlUnverified, CodeSendOutcomeUnknown, CodeScriptTimeout, CodeScriptInvalidOutput:
		return code
	case "invalid_request", "invalid_input", "email_probe_invalid_input", "email_send_invalid_input", "invalid_recipient", "invalid_subject", "invalid_body":
		return CodeInvalidInput
	case "page_contract_changed", "email_login_evidence_conflict", "login_evidence_conflict", "provider_origin_mismatch":
		return CodePageContractChanged
	case "email_provider_origin_invalid", "outlook_origin_not_allowed", "outlook_evidence_conflict", "outlook_page_contract_changed":
		return CodePageContractChanged
	case "existing_draft_open", "draft_conflict":
		return CodeDraftConflict
	case "draft_verification_failed", "field_verification_failed", "email_send_precondition_failed", "send_precondition_failed", "send_preparation_failed":
		return CodeDraftVerifyFailed
	case "send_control_not_ready", "send_control_unverified", "send_unavailable":
		return CodeSendControlUnverified
	case "send_not_confirmed", "send_clicked_unverified", "send_outcome_unknown":
		return CodeSendOutcomeUnknown
	case "probe_timeout", "login_probe_timeout", "agent_browser_timeout", "email_probe_timeout":
		return CodeScriptTimeout
	case "agent_browser_invalid_output", "email_agent_browser_invalid_output", "login_probe_invalid_output", "send_browser_output_invalid":
		return CodeScriptInvalidOutput
	default:
		return CodeProviderUnavailable
	}
}

func publicScriptErrorMessage(code string) string {
	switch code {
	case CodeLoginRequired:
		return "Email login is required"
	case CodeInvalidInput:
		return "Email request is invalid"
	case CodePageContractChanged:
		return "Email provider page contract changed"
	case CodeDraftConflict:
		return "An existing email draft prevents this send"
	case CodeDraftVerifyFailed:
		return "Email draft verification failed; Send was not clicked"
	case CodeSendControlUnverified:
		return "Email Send control could not be verified; Send was not clicked"
	case CodeSendOutcomeUnknown:
		return "Email send outcome is unknown and must not be retried"
	case CodeScriptTimeout:
		return "Email provider script timed out"
	case CodeScriptInvalidOutput:
		return "Email provider script returned invalid output"
	default:
		return "Email provider is unavailable"
	}
}

func validateScriptFile(command []string) error {
	if len(command) < 2 || strings.ToLower(filepath.Ext(command[len(command)-1])) != ".mjs" {
		return nil
	}
	path := command[len(command)-1]
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return codedError(CodeProviderUnavailable, "Email provider script is unavailable")
	}
	return nil
}

type boundedBuffer struct {
	mu       sync.Mutex
	limit    int
	data     []byte
	overflow bool
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - len(b.data)
	if remaining <= 0 {
		b.overflow = true
		return len(value), nil
	}
	if len(value) > remaining {
		b.data = append(b.data, value[:remaining]...)
		b.overflow = true
		return len(value), nil
	}
	b.data = append(b.data, value...)
	return len(value), nil
}

func (b *boundedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...)
}

func (b *boundedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.overflow
}
