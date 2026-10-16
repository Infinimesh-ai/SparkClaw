package emailautomation

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
)

// LifecycleRunner is implemented by runners whose controller hosts the
// App-CLI lifecycle 3.0 product control route.
type LifecycleRunner interface {
	ApplicationLifecycle(context.Context, browsercontrol.LifecycleRequest) (browsercontrol.LifecycleResult, error)
}

type lifecycleController interface {
	ApplicationLifecycle(context.Context, browsercontrol.LifecycleRequest) (browsercontrol.LifecycleResult, error)
}

var ErrLifecycleUnavailable = errors.New("application lifecycle is unavailable")

func (r *PlaywrightRunner) ApplicationLifecycle(ctx context.Context, request browsercontrol.LifecycleRequest) (browsercontrol.LifecycleResult, error) {
	controller, ok := r.controller.(lifecycleController)
	if r == nil || !ok {
		return browsercontrol.LifecycleResult{}, ErrLifecycleUnavailable
	}
	return controller.ApplicationLifecycle(ctx, request)
}

func (c *Controller) lifecycle(ctx context.Context, request browsercontrol.LifecycleRequest) (browsercontrol.LifecycleResult, error) {
	runner, ok := c.runner.(LifecycleRunner)
	if !ok {
		return browsercontrol.LifecycleResult{}, ErrLifecycleUnavailable
	}
	if _, registered := c.registry.Get(request.Provider); !registered {
		return browsercontrol.LifecycleResult{}, codedError(app.ToolErrorEmailInvalidInput, "Email provider is not registered")
	}
	return runner.ApplicationLifecycle(ctx, request)
}

// OperationEpoch reports the runtime's open request epoch for new sends.
func (c *Controller) OperationEpoch(ctx context.Context, provider string) (browsercontrol.EpochState, error) {
	result, err := c.lifecycle(ctx, browsercontrol.LifecycleRequest{Action: "epoch", Provider: provider})
	if err != nil {
		return browsercontrol.EpochState{}, err
	}
	return *result.EpochState, nil
}

// SealOperationEpoch confirms that no identity issued in epoch remains unsubmitted.
func (c *Controller) SealOperationEpoch(ctx context.Context, provider string, epoch int64, drained string) (browsercontrol.EpochState, error) {
	result, err := c.lifecycle(ctx, browsercontrol.LifecycleRequest{Action: "seal", Provider: provider, Epoch: epoch, DrainedDigest: drained})
	if err != nil {
		return browsercontrol.EpochState{}, err
	}
	return *result.EpochState, nil
}

// LookupSend asks whether the original send operation was ever admitted.
func (c *Controller) LookupSend(ctx context.Context, provider, invocationID string) (outcome, reason string, err error) {
	result, err := c.lifecycle(ctx, browsercontrol.LifecycleRequest{Action: "lookup", Provider: provider, Operation: "send", TaskID: invocationID})
	if err != nil {
		return "", "", err
	}
	return result.Outcome, result.Reason, nil
}

// AcknowledgeSend records durable product receipt of a terminal send result.
func (c *Controller) AcknowledgeSend(ctx context.Context, provider string, lifecycle app.EmailRuntimeLifecycle, receiptID string) error {
	_, err := c.lifecycle(ctx, browsercontrol.LifecycleRequest{Action: "ack", Provider: provider, Operation: "send",
		TaskID: lifecycle.OperationID, TerminalRevision: lifecycle.TerminalRevision, ResultDigest: lifecycle.ResultDigest, ReceiptID: receiptID})
	return err
}

// ResolveSend records an explicit user conclusion for an unknown send. It
// never dispatches; the runtime only closes the original task.
func (c *Controller) ResolveSend(ctx context.Context, provider, invocationID, resolution string) (string, error) {
	result, err := c.lifecycle(ctx, browsercontrol.LifecycleRequest{Action: "resolve", Provider: provider, Operation: "send",
		TaskID: invocationID, Resolution: resolution})
	if err != nil {
		return "", err
	}
	return result.Task.Status, nil
}
