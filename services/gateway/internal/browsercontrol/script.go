package browsercontrol

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

var scriptIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func (s *Service) RunScript(ctx context.Context, input RunScriptRequest) (ScriptExecutionResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	input.TaskID = strings.TrimSpace(input.TaskID)
	input.Provider = strings.TrimSpace(input.Provider)
	input.Operation = strings.TrimSpace(input.Operation)
	input.ScriptID = strings.TrimSpace(input.ScriptID)
	if input.ProfileID != "" && input.ProfileID != s.profileID || input.TaskID == "" ||
		!scriptIDPattern.MatchString(input.Provider) ||
		input.Operation != "probe" && input.Operation != "send" ||
		!scriptIDPattern.MatchString(input.ScriptID) || input.Revision <= 0 || input.Input == nil ||
		input.CredentialGeneration <= 0 || input.WaitTimeoutMS < 0 ||
		input.WaitTimeoutMS > maxRuntimeWaitTimeout.Milliseconds() {
		return ScriptExecutionResult{}, newError(CodeInvalidRequest, false, errors.New("browser script input is invalid"))
	}
	if s.vault == nil || s.vault.Ready() != nil {
		return ScriptExecutionResult{}, newError(CodeVaultUnavailable, false, errors.New("credential vault is unavailable"))
	}
	if s.client == nil {
		return ScriptExecutionResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
	}
	s.mu.RLock()
	active := s.active
	status := s.state
	s.mu.RUnlock()
	if active != nil {
		return ScriptExecutionResult{}, newError(CodeBusy, true, errors.New("browser profile already has an active runtime session"))
	}
	if !status.Configured {
		return ScriptExecutionResult{}, newError(CodeNotConfigured, false, errors.New("browser extension credential is missing"))
	}

	token, credentialGeneration, found, err := s.vault.OpenBindingVersion(ctx, credentialBinding, credentialKind)
	defer zero(token)
	if err != nil {
		return ScriptExecutionResult{}, mapVaultError(err)
	}
	if !found {
		return ScriptExecutionResult{}, newError(CodeNotConfigured, false, errors.New("browser extension credential is missing"))
	}
	if credentialGeneration <= 0 {
		return ScriptExecutionResult{}, newError(CodeVaultUnavailable, false, errors.New("browser credential generation is unavailable"))
	}
	if input.CredentialGeneration != status.CredentialGeneration ||
		status.CredentialGeneration != credentialGeneration {
		return ScriptExecutionResult{}, newError(CodeCredentialStale, false, errors.New("browser credential generation changed"))
	}
	input.ProfileID = s.profileID
	return s.client.RunScript(ctx, input, token)
}

func (s *Service) OpenProviderLogin(ctx context.Context, input OpenProviderLoginRequest) (OpenProviderLoginResult, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	input.TaskID = strings.TrimSpace(input.TaskID)
	input.Provider = strings.TrimSpace(input.Provider)
	if input.ProfileID != "" && input.ProfileID != s.profileID || input.TaskID == "" ||
		!scriptIDPattern.MatchString(input.Provider) || input.WaitTimeoutMS < 0 ||
		input.WaitTimeoutMS > maxRuntimeWaitTimeout.Milliseconds() {
		return OpenProviderLoginResult{}, newError(CodeInvalidRequest, false, errors.New("browser login input is invalid"))
	}
	if s.client == nil {
		return OpenProviderLoginResult{}, newError(CodeControllerUnavailable, true, errors.New("browser controller client is unavailable"))
	}
	s.mu.RLock()
	active := s.active
	s.mu.RUnlock()
	if active != nil {
		return OpenProviderLoginResult{}, newError(CodeBusy, true, errors.New("browser profile already has an active runtime session"))
	}
	input.ProfileID = s.profileID
	return s.client.OpenProviderLogin(ctx, input)
}
