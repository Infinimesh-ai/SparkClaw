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

func toolResultText(result mcpclient.ToolResult) string {
	return mcpsafety.ToolResultText(result)
}

func safeToolErrorText(value string) string {
	return mcpsafety.SafeErrorText(value, 500)
}
