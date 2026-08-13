package iscppairing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Infinimesh-ai/ISCP/pkg/iscp/provisioning"
)

const maxAuthorityTokenFileBytes = int64(64 << 10)

type HTTPAuthorityOptions struct {
	Endpoint         string
	TokenEnv         string
	TokenFile        string
	Timeout          time.Duration
	ResponseMaxBytes int64
	Client           *http.Client
}

type HTTPAuthority struct {
	endpoint         string
	tokenEnv         string
	tokenFile        string
	timeout          time.Duration
	responseMaxBytes int64
	client           *http.Client
}

func NewHTTPAuthority(options HTTPAuthorityOptions) (*HTTPAuthority, error) {
	parsed, err := url.Parse(strings.TrimSpace(options.Endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("ISCP pairing authority endpoint must be an absolute URL")
	}
	if options.Timeout <= 0 || options.ResponseMaxBytes <= 0 {
		return nil, errors.New("ISCP pairing authority timeout and response limit must be positive")
	}
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: options.Timeout}
	}
	return &HTTPAuthority{
		endpoint: parsed.String(), tokenEnv: strings.TrimSpace(options.TokenEnv), tokenFile: strings.TrimSpace(options.TokenFile),
		timeout: options.Timeout, responseMaxBytes: options.ResponseMaxBytes, client: client,
	}, nil
}

func (a *HTTPAuthority) Ready(_ context.Context) error {
	if a == nil {
		return ErrUnavailable
	}
	_, err := a.token()
	return err
}

func (a *HTTPAuthority) IssuePairingTicket(ctx context.Context, request AuthorityRequest) (AuthorityResult, error) {
	if err := a.Ready(ctx); err != nil {
		return AuthorityResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	token, _ := a.token()
	body, err := json.Marshal(request)
	if err != nil {
		return AuthorityResult{}, err
	}
	requestCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	httpRequest, err := http.NewRequestWithContext(requestCtx, http.MethodPost, a.endpoint, bytes.NewReader(body))
	if err != nil {
		return AuthorityResult{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("Idempotency-Key", request.RequestRef)
	response, err := a.client.Do(httpRequest)
	if err != nil {
		return AuthorityResult{}, fmt.Errorf("%w: %v", ErrAuthority, err)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, a.responseMaxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return AuthorityResult{}, fmt.Errorf("%w: read response", ErrAuthority)
	}
	if int64(len(raw)) > a.responseMaxBytes {
		return AuthorityResult{}, fmt.Errorf("%w: response exceeded configured limit", ErrAuthority)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return AuthorityResult{}, fmt.Errorf("%w: HTTP %d", ErrAuthority, response.StatusCode)
	}
	var output struct {
		AuthorityRef string                     `json:"authority_ref"`
		Ticket       provisioning.PairingTicket `json:"ticket"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&output); err != nil {
		return AuthorityResult{}, fmt.Errorf("%w: invalid response", ErrAuthority)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return AuthorityResult{}, fmt.Errorf("%w: invalid response", ErrAuthority)
	}
	return AuthorityResult{AuthorityRef: strings.TrimSpace(output.AuthorityRef), Ticket: output.Ticket}, nil
}

func (a *HTTPAuthority) token() (string, error) {
	if a == nil || (a.tokenEnv == "" && a.tokenFile == "") || (a.tokenEnv != "" && a.tokenFile != "") {
		return "", errors.New("exactly one authority token source is required")
	}
	if a.tokenEnv != "" {
		value := strings.TrimSpace(os.Getenv(a.tokenEnv))
		if value == "" {
			return "", errors.New("authority token environment variable is empty")
		}
		return value, nil
	}
	info, err := os.Lstat(a.tokenFile)
	if err != nil {
		return "", errors.New("authority token file is unavailable")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("authority token file must be a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("authority token file must not be accessible by group or others")
	}
	if info.Size() < 0 || info.Size() > maxAuthorityTokenFileBytes {
		return "", errors.New("authority token file exceeds the size limit")
	}
	file, err := os.Open(a.tokenFile)
	if err != nil {
		return "", errors.New("authority token file is unavailable")
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() || openedInfo.Mode().Perm()&0o077 != 0 {
		return "", errors.New("authority token file changed while opening")
	}
	if openedInfo.Size() < 0 || openedInfo.Size() > maxAuthorityTokenFileBytes {
		return "", errors.New("authority token file exceeds the size limit")
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxAuthorityTokenFileBytes+1))
	if err != nil {
		return "", errors.New("authority token file is unavailable")
	}
	if int64(len(raw)) > maxAuthorityTokenFileBytes {
		return "", errors.New("authority token file exceeds the size limit")
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", errors.New("authority token file is empty")
	}
	return value, nil
}
