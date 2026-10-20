package gateway

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscppairing"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcpaccess"
)

// mcpSettings shares the owner-scoped business handlers with HTTPS. Every
// operation selects its handler and method here; peers cannot supply a route,
// method, principal, or arbitrary path parameters.
func (a *iscpDomainAdapter) mcpSettings(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if len(request.Body) > 4096 {
		return domainError(http.StatusBadRequest, "invalid_input")
	}
	if len(request.Body) == 0 {
		request.Body = []byte(`{}`)
	}
	body := bytes.TrimSpace(request.Body)
	if len(body) == 0 || body[0] != '{' {
		return domainError(http.StatusBadRequest, "invalid_input")
	}
	method := http.MethodGet
	var handler http.HandlerFunc
	var pathParams map[string]string
	switch request.Operation {
	case iscpworkbench.OperationSettingsMCPCatalog:
		handler = a.server.listMCPAccessCatalog
	case iscpworkbench.OperationSettingsMCPTicketsList:
		handler = a.server.listMCPAccessTickets
	case iscpworkbench.OperationSettingsMCPBindingsList:
		handler = a.server.listMCPBindings
	case iscpworkbench.OperationSettingsMCPPairingStatus:
		handler = a.server.getISCPPairingStatus
	case iscpworkbench.OperationSettingsMCPPairingOnboardings:
		handler = a.server.listISCPOnboardings
	case iscpworkbench.OperationSettingsMCPRecordsDelete:
		method, handler = http.MethodDelete, a.server.deleteMCPAccessRecords
	case iscpworkbench.OperationSettingsMCPTicketsRevoke, iscpworkbench.OperationSettingsMCPTicketsDelete,
		iscpworkbench.OperationSettingsMCPBindingsRevoke, iscpworkbench.OperationSettingsMCPBindingsDelete:
		param := "ticket_id"
		if strings.HasPrefix(request.Operation, "settings.mcp.bindings.") {
			param = "binding_id"
		}
		if len(request.Params) != 1 || !mcpSettingsRecordID(request.Params[param]) || !domainEmpty(request.Body) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		pathParams = map[string]string{"id": request.Params[param]}
		switch request.Operation {
		case iscpworkbench.OperationSettingsMCPTicketsRevoke:
			method, handler = http.MethodPost, a.server.revokeMCPAccessTicket
		case iscpworkbench.OperationSettingsMCPTicketsDelete:
			method, handler = http.MethodDelete, a.server.deleteMCPAccessTicket
		case iscpworkbench.OperationSettingsMCPBindingsRevoke:
			method, handler = http.MethodPost, a.server.revokeMCPBinding
		case iscpworkbench.OperationSettingsMCPBindingsDelete:
			method, handler = http.MethodDelete, a.server.deleteMCPBinding
		}
		return domainHTTP(ctx, request, method, handler, pathParams)
	case iscpworkbench.OperationSettingsMCPTransports:
		var input struct {
			ISCPEnabled      *bool  `json:"iscp_enabled"`
			LANAccessEnabled *bool  `json:"lan_access_enabled"`
			ExpectedVersion  *int64 `json:"expected_version"`
		}
		if len(request.Params) != 0 || domainDecode(request.Body, &input) != nil ||
			input.ISCPEnabled == nil || input.LANAccessEnabled == nil || input.ExpectedVersion == nil || *input.ExpectedVersion < 0 {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPatch, a.server.updateMCPTransports, nil)
	case iscpworkbench.OperationSettingsMCPTicketsIssue:
		var input mcpaccess.IssueTicketRequest
		if len(request.Params) != 0 || domainDecode(request.Body, &input) != nil ||
			len(input.DomainID) > 256 || strings.ContainsAny(input.DomainID, "\x00\r\n") ||
			(input.TTLSeconds != 0 && (input.TTLSeconds < 60 || input.TTLSeconds > 86400)) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.issueMCPAccessTicket, nil)
	case iscpworkbench.OperationSettingsMCPPairingStart:
		var input iscppairing.StartRequest
		// Match iscppairing.Service.Start's existing 120-byte limit.
		if len(request.Params) != 0 || domainDecode(request.Body, &input) != nil ||
			strings.TrimSpace(input.DisplayName) == "" || len(strings.TrimSpace(input.DisplayName)) > 120 ||
			strings.ContainsAny(input.DisplayName, "\x00\r\n") ||
			(input.TTLSeconds != 0 && (input.TTLSeconds < 60 || input.TTLSeconds > 1800)) {
			return domainError(http.StatusBadRequest, "invalid_input")
		}
		return domainHTTP(ctx, request, http.MethodPost, a.server.startISCPPairing, nil)
	default:
		return domainError(http.StatusNotImplemented, "capability_unavailable")
	}
	if len(request.Params) != 0 || !domainEmpty(request.Body) {
		return domainError(http.StatusBadRequest, "invalid_input")
	}
	return domainHTTP(ctx, request, method, handler, nil)
}

func mcpSettingsRecordID(value string) bool {
	if len(value) == 0 || len(value) > 256 {
		return false
	}
	for _, char := range value {
		if char != '-' && char != '_' && (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
			return false
		}
	}
	return true
}
