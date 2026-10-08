package agent

import (
	"context"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// ExecutionRepository contains execution-owned records. No durable workbench
// repository is implicitly borrowed when a remote request creates a scope.
type ExecutionRepository interface {
	Repository
	toolhub.Repository
}

// WithExecutionScope retains the active runtime's routing, model, policy and
// authority-checking mail services. Only content ownership and explicitly bound
// resources change. Release forgets suspended credential dependencies and closes
// scope-owned resources, including when its execution context was cancelled.
func (r Runtime) WithExecutionScope(st ExecutionRepository, resources toolhub.ExecutionResources) (Runtime, func(context.Context) error, error) {
	tools, err := r.tools.WithExecutionScope(st, resources)
	if err != nil {
		return Runtime{}, nil, err
	}
	r.store = st
	r.tools = tools
	r.artifacts = resources.Artifacts
	r.traces = nil
	r.exposure = newToolExposureEngine(st, tools, r.policy)
	r.messageControl = nil
	if resources.TextOnly {
		// Email admission can probe the live browser before tool dispatch. The
		// text profile cannot borrow that service even though tools are hidden.
		r.emailAdmission = nil
	}
	return r, func(ctx context.Context) error {
		var cleanupErr error
		if r.integrationRuns != nil {
			runs, err := st.ListRuns(context.WithoutCancel(ctx), "")
			cleanupErr = err
			for _, run := range runs {
				r.integrationRuns.Forget(run.ID)
			}
		}
		return errors.Join(cleanupErr, tools.Close())
	}, nil
}
