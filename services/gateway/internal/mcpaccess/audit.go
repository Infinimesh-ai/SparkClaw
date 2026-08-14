package mcpaccess

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *Service) audit(typ, sessionID, runID, actor, summary string, fields map[string]any) {
	if s == nil || s.store == nil {
		return
	}
	s.store.AddAudit(app.AuditEvent{Type: typ, SessionID: sessionID, RunID: runID, Actor: actor, Summary: summary, Fields: fields})
}

func (s *Service) auditPeerDenied(peer app.MCPPeerIdentity, typ, summary string, fields map[string]any) {
	fields = cloneAuditFields(fields)
	fields["domain_id"] = peer.DomainID
	fields["requester_device_id"] = peer.DeviceID
	fields["requester_key_thumbprint"] = peer.KeyThumbprint
	fields["iscp_session_id"] = peer.ISCPSessionID
	s.audit(typ, "", "", "mcp", summary, fields)
}

func (s *Service) auditToolDenied(peer app.MCPPeerIdentity, binding app.MCPBinding, toolName, reason string) {
	s.audit("mcp.tool.denied", binding.LinkedSessionID, "", binding.ActorID, "Denied an MCP tool invocation", map[string]any{
		"binding_id": binding.ID, "binding_revision": binding.AuthorizationRevision,
		"requester_device_id": peer.DeviceID, "tool_name": toolName, "reason": reason,
	})
}

func (s *Service) auditOperation(typ string, operation app.MCPOperation, peer app.MCPPeerIdentity, summary string, extra map[string]any) {
	fields := operationAuditFields(operation, peer)
	for key, value := range extra {
		fields[key] = value
	}
	s.audit(typ, operationSessionID(s.store, operation), operation.Invocation.RunID, operation.Invocation.ActorID, summary, fields)
}

func auditOperationStore(st interface {
	AddAudit(app.AuditEvent)
	GetMCPBinding(string) (app.MCPBinding, bool)
}, typ string, operation app.MCPOperation, summary string, extra map[string]any) {
	fields := operationAuditFields(operation, app.MCPPeerIdentity{
		DeviceID: operation.Invocation.RequesterDeviceID, KeyThumbprint: operation.Invocation.RequesterKeyThumbprint,
		ISCPSessionID: operation.Invocation.ISCPSessionID,
	})
	for key, value := range extra {
		fields[key] = value
	}
	st.AddAudit(app.AuditEvent{
		Type: typ, SessionID: operationSessionID(st, operation), RunID: operation.Invocation.RunID,
		Actor: operation.Invocation.ActorID, Summary: summary, Fields: fields,
	})
}

func operationAuditFields(operation app.MCPOperation, peer app.MCPPeerIdentity) map[string]any {
	return map[string]any{
		"operation_id": operation.ID, "binding_id": operation.BindingID,
		"binding_revision":         operation.Invocation.BindingRevision,
		"requester_device_id":      operation.Invocation.RequesterDeviceID,
		"requester_key_thumbprint": operation.Invocation.RequesterKeyThumbprint,
		"iscp_session_id":          peer.ISCPSessionID, "tool_name": operation.Invocation.ToolName,
	}
}

func operationSessionID(st interface {
	GetMCPBinding(string) (app.MCPBinding, bool)
}, operation app.MCPOperation) string {
	if binding, ok := st.GetMCPBinding(operation.BindingID); ok {
		return binding.LinkedSessionID
	}
	return ""
}

func operationAuditType(toolName string) string {
	switch toolName {
	case "sparkclaw.operation.cancel":
		return "mcp.operation.cancel_requested"
	case "sparkclaw.operation.result":
		return "mcp.operation.result_read"
	default:
		return "mcp.operation.status_read"
	}
}

func cloneAuditFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields)+4)
	for key, value := range fields {
		out[key] = value
	}
	return out
}
