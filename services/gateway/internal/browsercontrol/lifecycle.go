package browsercontrol

import (
	"context"
	"errors"
	"regexp"
)

// ScriptLifecycle is the App-CLI lifecycle 3.0 identity of a script task and,
// once terminal, the exact revision the Gateway echoes back in an ACK.
type ScriptLifecycle struct {
	OperationID      string `json:"operation_id,omitempty"`
	TaskID           string `json:"task_id"`
	Status           string `json:"status"`
	TerminalRevision int64  `json:"terminal_revision,omitempty"`
	ResultDigest     string `json:"result_digest,omitempty"`
	Attempt          int64  `json:"attempt,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
}

// EpochState reports the request epochs in which new operation identities
// may be issued (open) or are still accepted (draining).
type EpochState struct {
	Generation              int64  `json:"generation"`
	OpenEpoch               int64  `json:"open_epoch"`
	DrainingEpoch           *int64 `json:"draining_epoch"`
	SealedThrough           int64  `json:"sealed_through"`
	AuthorizationGeneration int64  `json:"authorization_generation"`
}

type LifecycleRequest struct {
	Action           string `json:"action"`
	Provider         string `json:"provider"`
	Operation        string `json:"operation,omitempty"`
	TaskID           string `json:"task_id,omitempty"`
	TerminalRevision int64  `json:"terminal_revision,omitempty"`
	ResultDigest     string `json:"result_digest,omitempty"`
	ReceiptID        string `json:"receipt_id,omitempty"`
	Resolution       string `json:"resolution,omitempty"`
	Epoch            int64  `json:"epoch,omitempty"`
	DrainedDigest    string `json:"drained_digest,omitempty"`
}

type LifecycleTask struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type LifecycleResult struct {
	SchemaVersion int              `json:"schema_version"`
	ProfileID     string           `json:"profile_id"`
	Action        string           `json:"action"`
	Provider      string           `json:"provider"`
	EpochState    *EpochState      `json:"epoch_state,omitempty"`
	Outcome       string           `json:"outcome,omitempty"`
	Reason        string           `json:"reason,omitempty"`
	Task          *LifecycleTask   `json:"task,omitempty"`
	Lifecycle     *ScriptLifecycle `json:"lifecycle,omitempty"`
}

var (
	lifecycleActions = map[string]bool{"epoch": true, "seal": true, "lookup": true, "ack": true, "resolve": true}
	lifecycleOutcome = map[string]bool{"known": true, "not_admitted": true, "retired": true, "unresolved": true}
	digestPattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ApplicationLifecycle performs product-side lifecycle control over the
// owner-only controller socket: epoch state and drained seal, original
// operation lookup, durable result ACK and explicit user resolution.
func (s *Service) ApplicationLifecycle(ctx context.Context, input LifecycleRequest) (LifecycleResult, error) {
	if !lifecycleActions[input.Action] || !scriptIDPattern.MatchString(input.Provider) ||
		(input.Action == "seal" && (input.Epoch <= 0 || !digestPattern.MatchString(input.DrainedDigest))) ||
		(input.Action == "ack" && (input.TerminalRevision <= 0 || !digestPattern.MatchString(input.ResultDigest) || input.ReceiptID == "")) ||
		((input.Action == "lookup" || input.Action == "ack" || input.Action == "resolve") && input.TaskID == "") {
		return LifecycleResult{}, newError(CodeInvalidRequest, false, errors.New("application lifecycle request is invalid"))
	}
	release, err := s.acquireOperations(ctx, false)
	if err != nil {
		return LifecycleResult{}, err
	}
	defer release()
	client, ok := s.client.(*HTTPControllerClient)
	if !ok {
		return LifecycleResult{}, newError(CodeControllerUnavailable, true, errors.New("application lifecycle transport unavailable"))
	}
	payload := struct {
		ProfileID string `json:"profile_id"`
		LifecycleRequest
	}{s.profileID, input}
	var out LifecycleResult
	if err := client.postJSON(ctx, "/v1/application-lifecycle", payload, maxControllerResponseBytes, &out); err != nil {
		return LifecycleResult{}, err
	}
	if out.SchemaVersion != 1 || out.ProfileID != s.profileID || out.Action != input.Action || out.Provider != input.Provider ||
		(input.Action == "epoch" || input.Action == "seal") && !validEpochState(out.EpochState) ||
		input.Action == "lookup" && !lifecycleOutcome[out.Outcome] ||
		(input.Action == "ack" || input.Action == "resolve") && (out.Task == nil || out.Task.ID == "") {
		return LifecycleResult{}, invalidControllerResponse()
	}
	return out, nil
}

func validEpochState(state *EpochState) bool {
	return state != nil && state.Generation > 0 && state.OpenEpoch > 0 && state.SealedThrough >= 0 &&
		state.SealedThrough < state.OpenEpoch && state.AuthorizationGeneration > 0 &&
		(state.DrainingEpoch == nil || *state.DrainingEpoch > state.SealedThrough && *state.DrainingEpoch < state.OpenEpoch)
}
