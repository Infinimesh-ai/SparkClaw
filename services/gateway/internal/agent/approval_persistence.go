package agent

import (
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpsafety"
)

func validateApprovalArgumentPersistence(def app.ToolDefinition, args map[string]any) error {
	if !isExternalMCPToolDefinition(def) {
		return nil
	}
	if mcpsafety.UnsafeForPersistence(args) {
		return &app.CodedToolError{
			Code: app.ToolErrorMCPPersistenceUnsafe,
			Err:  errors.New("external MCP tool arguments contain secret, signed URL, or large base64 data that cannot be persisted for approval"),
		}
	}
	return nil
}

func isExternalMCPToolDefinition(def app.ToolDefinition) bool {
	for _, capability := range def.Capabilities {
		switch capability.Name {
		case app.ToolCapabilityExternalMCPWorkspace, app.ToolCapabilityMCPExternal, app.ToolCapabilityMCPApprovalResolve:
			return true
		}
	}
	return false
}

func redactedRejectedApprovalArguments(map[string]any) map[string]any {
	return map[string]any{"persistence_rejected": true}
}
