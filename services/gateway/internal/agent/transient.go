package agent

import (
	"context"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// WithTransientRepositories reuses the compiled routing graph and model client,
// but gives this execution a fresh data plane. External connectors, persistent
// traces and incumbent repositories cannot receive its content. Info adapters
// and their in-memory cancellation registry follow the active credentials.
// Release must run after execution/approval continuation ends to forget any
// suspended transient runs before discarding their repositories.
func (r Runtime) WithTransientRepositories(st Repository, tools *toolhub.ToolHub, policies policy.Engine, artifacts artifact.Store) (Runtime, func(context.Context) error) {
	tools.WithSharedInfoRuntime(r.tools)
	r.store = st
	r.tools = tools
	r.policy = policies
	r.artifacts = artifacts
	r.traces = nil
	r.exposure = newToolExposureEngine(st, tools, policies)
	r.messageControl = nil
	r.emailAdmission = nil
	return r, func(ctx context.Context) error {
		if r.integrationRuns == nil {
			return nil
		}
		runs, err := st.ListRuns(context.WithoutCancel(ctx), "")
		if err != nil {
			return err
		}
		for _, run := range runs {
			r.integrationRuns.Forget(run.ID)
		}
		return nil
	}
}
