package agent

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

// WithTransientRepositories reuses the compiled routing graph and model client,
// but gives this execution a fresh data plane. External connectors, persistent
// traces and incumbent approval/run registries cannot receive its content.
func (r Runtime) WithTransientRepositories(st Repository, tools *toolhub.ToolHub, policies policy.Engine, artifacts artifact.Store) Runtime {
	r.store = st
	r.tools = tools
	r.policy = policies
	r.artifacts = artifacts
	r.traces = nil
	r.exposure = newToolExposureEngine(st, tools, policies)
	r.messageControl = nil
	r.integrationRuns = nil
	r.emailAdmission = nil
	return r
}
