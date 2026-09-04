package emailautomation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserautomation"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
)

const browserdControlMaxBytes = 64 << 10

type BrowserController interface {
	EnsureHeadless(context.Context) (browserautomation.HostCDPEndpoint, error)
	OpenLogin(context.Context, string) error
}

type BrowserdClient struct {
	cfg config.HostCDPConfig
	mu  sync.Mutex
}

func NewBrowserdClient(cfg config.HostCDPConfig) *BrowserdClient {
	return &BrowserdClient{cfg: cfg}
}

func (c *BrowserdClient) EnsureHeadless(ctx context.Context) (browserautomation.HostCDPEndpoint, error) {
	return c.ensure(ctx, "ensure-hidden", "", "headless")
}

func (c *BrowserdClient) OpenLogin(ctx context.Context, targetURL string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	response, err := c.control(ctx, browserdControlRequest{Operation: "open-login", URL: strings.TrimSpace(targetURL)})
	if err != nil {
		return err
	}
	if !response.OK || response.Error != "" || response.Presentation != "manual-login" ||
		response.ProfileID != strings.TrimSpace(c.cfg.ProfileID) || response.BrowserPID <= 0 ||
		response.Generation == 0 || strings.TrimSpace(response.BrowserVersion) == "" {
		return codedError(CodeProviderUnavailable, "SparkClaw manual login browser is unavailable")
	}
	return nil
}

func (c *BrowserdClient) ensure(ctx context.Context, operation, targetURL, presentation string) (browserautomation.HostCDPEndpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	previous, _ := browserautomation.ReadHostCDPEndpoint(c.cfg)
	request := browserdControlRequest{Operation: operation}
	if operation == "ensure-headed" {
		request.URL = targetURL
	}
	response, err := c.control(ctx, request)
	if err != nil {
		return browserautomation.HostCDPEndpoint{}, err
	}
	if !response.OK || response.Error != "" || response.Presentation != presentation ||
		response.ProfileID != strings.TrimSpace(c.cfg.ProfileID) || response.BrowserPID <= 0 ||
		response.Generation == 0 || strings.TrimSpace(response.BrowserVersion) == "" {
		return browserautomation.HostCDPEndpoint{}, codedError(CodeProviderUnavailable, "SparkClaw browser presentation is unavailable")
	}

	deadline := time.Now().Add(c.connectTimeout())
	for {
		endpoint, readErr := browserautomation.ReadHostCDPEndpoint(c.cfg)
		if readErr == nil && endpoint.Presentation == presentation &&
			endpoint.Generation == response.Generation &&
			(previous.Presentation == presentation || previous.Generation == 0 || endpoint.Generation != previous.Generation) {
			return endpoint, nil
		}
		if time.Now().After(deadline) {
			return browserautomation.HostCDPEndpoint{}, codedError(CodeProviderUnavailable, "SparkClaw browser did not enter the required presentation")
		}
		select {
		case <-ctx.Done():
			return browserautomation.HostCDPEndpoint{}, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

type browserdControlRequest struct {
	Operation string `json:"operation"`
	URL       string `json:"url,omitempty"`
}

type browserdControlResponse struct {
	OK             bool   `json:"ok"`
	Error          string `json:"error,omitempty"`
	BrowserPID     int    `json:"browserPID,omitempty"`
	Presentation   string `json:"presentation,omitempty"`
	ProfileID      string `json:"profileID,omitempty"`
	BrowserVersion string `json:"browserVersion,omitempty"`
	Generation     uint64 `json:"generation,omitempty"`
}

func (c *BrowserdClient) control(ctx context.Context, request browserdControlRequest) (browserdControlResponse, error) {
	socketPath := filepath.Join(filepath.Dir(c.cfg.EndpointFile), "control.sock")
	if err := validateControlSocket(socketPath); err != nil {
		return browserdControlResponse{}, err
	}
	dialer := net.Dialer{Timeout: c.connectTimeout()}
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control is unavailable")
	}
	defer connection.Close()
	deadline := time.Now().Add(c.connectTimeout())
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	_ = connection.SetDeadline(deadline)
	payload, err := json.Marshal(request)
	if err != nil {
		return browserdControlResponse{}, err
	}
	if len(payload) > browserdControlMaxBytes {
		return browserdControlResponse{}, errors.New("browserd control request is too large")
	}
	if _, err := connection.Write(append(payload, '\n')); err != nil {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control request failed")
	}
	reader := bufio.NewReader(io.LimitReader(connection, browserdControlMaxBytes+1))
	line, err := reader.ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control response failed")
	}
	if len(line) == 0 || len(line) > browserdControlMaxBytes {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control returned an invalid response")
	}
	var response browserdControlResponse
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control returned an invalid response")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil || !errors.Is(err, io.EOF) {
		return browserdControlResponse{}, codedError(CodeProviderUnavailable, "SparkClaw browser control returned trailing data")
	}
	return response, nil
}

func (c *BrowserdClient) connectTimeout() time.Duration {
	timeout := time.Duration(c.cfg.ConnectTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		return 10 * time.Second
	}
	return timeout
}

func validateControlSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return codedError(CodeProviderUnavailable, "SparkClaw browser control is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return codedError(CodeProviderUnavailable, "SparkClaw browser control socket is invalid")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return codedError(CodeProviderUnavailable, "SparkClaw browser control socket permissions are invalid")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return codedError(CodeProviderUnavailable, "SparkClaw browser control socket owner is invalid")
	}
	return nil
}

func browserEndpointSummary(endpoint browserautomation.HostCDPEndpoint) string {
	return fmt.Sprintf("%s:%d", endpoint.Presentation, endpoint.Generation)
}
