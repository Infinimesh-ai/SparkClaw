package localmind

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpclient"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpsafety"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type projectionMode = mcpsafety.Mode

const (
	projectionState   = mcpsafety.State
	projectionArchive = mcpsafety.Archive
)

func (m *Manager) projectionLimits() mcpsafety.Limits {
	return mcpsafety.Limits{
		StateMaxBytes:   m.cfg.StateOutputMaxBytes,
		ArchiveMaxBytes: m.cfg.ArchiveOutputMaxBytes,
	}
}

func (m *Manager) projectToolResult(result mcpclient.ToolResult, remoteName string) toolhub.Result {
	projected := mcpsafety.ProjectToolResult(result, "localmind", remoteName, m.projectionLimits())
	return toolhub.Result{Output: projected.Output, ArchiveOutput: projected.ArchiveOutput}
}

func (m *Manager) projectValue(value any, method string) toolhub.Result {
	normalized := mcpsafety.NormalizeJSONValue(value)
	projected := mcpsafety.ProjectValue(normalized, map[string]any{
		"provider": "localmind", "method": method, "result": normalized, "untrusted": true,
	}, m.projectionLimits())
	return toolhub.Result{Output: projected.Output, ArchiveOutput: projected.ArchiveOutput}
}

func boundedProjection(value any, mode projectionMode, maxBytes int) any {
	return mcpsafety.BoundedProjection(value, mode, maxBytes)
}

func toolResultText(result mcpclient.ToolResult) string {
	return mcpsafety.ToolResultText(result)
}

func safeToolErrorText(value string) string {
	return mcpsafety.SafeErrorText(value, 500)
}
