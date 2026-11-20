package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

const (
	browserControlRoutePrefix        = "/api/browser/extension"
	maxBrowserControlRequestBytes    = 8 << 10
	browserControlUnavailableMessage = "browser control configuration is unavailable"
)

func (s *Server) getBrowserExtension(w http.ResponseWriter, r *http.Request) {
	if !browserControlRequestAllowed(w, r) {
		return
	}
	s.browserExtension(r.Context()).write(w)
}

func (s *Server) putBrowserExtensionToken(w http.ResponseWriter, r *http.Request) {
	if !browserControlRequestAllowed(w, r) || !s.requireBrowserControl(w) {
		return
	}
	var input struct {
		Token string `json:"token"`
	}
	if err := readBrowserControlJSON(w, r, &input, true); err != nil {
		writeBrowserControlError(w, browsercontrolInvalidRequest())
		return
	}
	token := []byte(input.Token)
	input.Token = ""
	defer zeroBrowserControlBytes(token)
	s.browserExtensionSave(r.Context(), token).write(w)
}

func (s *Server) checkBrowserExtension(w http.ResponseWriter, r *http.Request) {
	if !browserControlRequestAllowed(w, r) || !s.requireBrowserControl(w) {
		return
	}
	var input struct{}
	if err := readBrowserControlJSON(w, r, &input, false); err != nil {
		writeBrowserControlError(w, browsercontrolInvalidRequest())
		return
	}
	s.browserExtensionCheck(r.Context()).write(w)
}

func (s *Server) deleteBrowserExtensionToken(w http.ResponseWriter, r *http.Request) {
	if !browserControlRequestAllowed(w, r) || !s.requireBrowserControl(w) {
		return
	}
	var input struct{}
	if err := readBrowserControlJSON(w, r, &input, false); err != nil {
		writeBrowserControlError(w, browsercontrolInvalidRequest())
		return
	}
	s.browserExtensionRemove(r.Context()).write(w)
}

// The browser extension settings: its status, saving and removing the
// pairing token, and checking the connection.

func (s *Server) browserExtension(ctx context.Context) domainReply {
	if s.browserControl == nil {
		return browserControlUnavailableReply()
	}
	return browserControlStatusReply(http.StatusOK, s.browserControl.Status(ctx))
}

func (s *Server) browserExtensionSave(ctx context.Context, token []byte) domainReply {
	if s.browserControl == nil {
		return browserControlUnavailableReply()
	}
	status, err := s.browserControl.SaveToken(ctx, token)
	if err != nil {
		return browserControlErrorReply(err)
	}
	return browserControlStatusReply(http.StatusOK, status)
}

func (s *Server) browserExtensionCheck(ctx context.Context) domainReply {
	if s.browserControl == nil {
		return browserControlUnavailableReply()
	}
	status, err := s.browserControl.Check(ctx)
	if err != nil {
		return browserControlErrorReply(err)
	}
	return browserControlStatusReply(http.StatusOK, status)
}

func (s *Server) browserExtensionRemove(ctx context.Context) domainReply {
	if s.browserControl == nil {
		return browserControlUnavailableReply()
	}
	status, err := s.browserControl.Remove(ctx)
	if err != nil {
		return browserControlErrorReply(err)
	}
	return browserControlStatusReply(http.StatusOK, status)
}

func browserControlUnavailableReply() domainReply {
	return reply(http.StatusServiceUnavailable, map[string]any{
		"error": browserControlUnavailableMessage, "code": browsercontrol.CodeControllerUnavailable, "retryable": true,
	})
}

func (s *Server) requireBrowserControl(w http.ResponseWriter) bool {
	if s.browserControl != nil {
		return true
	}
	browserControlUnavailableReply().write(w)
	return false
}

func browserControlRequestAllowed(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.RawQuery == "" {
		return true
	}
	writeBrowserControlError(w, browsercontrolInvalidRequest())
	return false
}

func readBrowserControlJSON(w http.ResponseWriter, r *http.Request, output any, required bool) error {
	if r.Body == nil {
		if required {
			return io.EOF
		}
		return nil
	}
	defer r.Body.Close()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBrowserControlRequestBytes))
	defer zeroBrowserControlBytes(body)
	if err != nil {
		return err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		if required {
			return io.EOF
		}
		return nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.SplitN(r.Header.Get("Content-Type"), ";", 2)[0]))
	if contentType != "application/json" || trimmed[0] != '{' {
		return errors.New("request body must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request must contain one JSON value")
	}
	return nil
}

func writeBrowserControlStatus(w http.ResponseWriter, statusCode int, status browsercontrol.Status) {
	w.Header().Set("Cache-Control", "no-store")
	browserControlStatusReply(statusCode, status).write(w)
}

func browserControlStatusReply(statusCode int, status browsercontrol.Status) domainReply {
	versions := map[string]string{}
	if status.Versions.Client != "" {
		versions["client"] = status.Versions.Client
	}
	if status.Versions.ClientVersion != "" {
		versions["client_version"] = status.Versions.ClientVersion
	}
	if status.Versions.PlaywrightVersion != "" {
		versions["playwright_version"] = status.Versions.PlaywrightVersion
	}
	if status.Versions.BrowserChannel != "" {
		versions["browser_channel"] = status.Versions.BrowserChannel
	}
	if status.Versions.CLI != "" {
		versions["cli"] = status.Versions.CLI
	}
	if status.Versions.CLIVersion != "" {
		versions["cli_version"] = status.Versions.CLIVersion
	}
	output := map[string]any{
		"configured":            status.Configured,
		"state":                 status.State,
		"profile_id":            status.ProfileID,
		"credential_generation": status.CredentialGeneration,
		"versions":              versions,
	}
	if status.ControllerGeneration > 0 {
		output["controller_generation"] = status.ControllerGeneration
	}
	if status.SessionGeneration > 0 {
		output["session_generation"] = status.SessionGeneration
	}
	if status.PageGeneration > 0 {
		output["page_generation"] = status.PageGeneration
	}
	if !status.LastValidatedAt.IsZero() {
		output["last_validated_at"] = status.LastValidatedAt.UTC().Format(time.RFC3339Nano)
	}
	if status.ErrorCode != "" {
		output["error_code"] = status.ErrorCode
	}
	return reply(statusCode, output)
}

func writeBrowserControlError(w http.ResponseWriter, err error) {
	w.Header().Set("Cache-Control", "no-store")
	browserControlErrorReply(err).write(w)
}

func browserControlErrorReply(err error) domainReply {
	code := browsercontrol.ErrorCode(err)
	retryable := browsercontrol.ErrorRetryable(err)
	message := "browser control operation failed"
	status := http.StatusInternalServerError
	if code != "" {
		message = err.Error()
	}
	switch code {
	case browsercontrol.CodeInvalidRequest:
		status = http.StatusBadRequest
	case browsercontrol.CodeExtensionRejected:
		status = http.StatusUnprocessableEntity
	case browsercontrol.CodeNotConfigured, browsercontrol.CodeBusy:
		status = http.StatusConflict
	case browsercontrol.CodeControllerUnavailable, browsercontrol.CodeExtensionUnavailable, browsercontrol.CodeVaultUnavailable:
		status = http.StatusServiceUnavailable
	}
	return reply(status, map[string]any{"error": message, "code": code, "retryable": retryable})
}

func browsercontrolInvalidRequest() error {
	return &browsercontrol.Error{Code: browsercontrol.CodeInvalidRequest}
}

func zeroBrowserControlBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func isBrowserControlRoute(path string) bool {
	return path == browserControlRoutePrefix || strings.HasPrefix(path, browserControlRoutePrefix+"/")
}
