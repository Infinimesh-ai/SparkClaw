package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpnodeissuer"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

type nodeClientManagement struct {
	http              *http.Client
	token, deployment string
}
type nodeManagedDevices struct {
	SchemaVersion int                           `json:"schema_version"`
	Deployment    string                        `json:"deployment_id"`
	Devices       []iscpnodeissuer.DeviceRecord `json:"devices"`
}

var nodeClientID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,159}$`)

func newNodeClientManagement(cfg iscpworkbench.Config, deployment string) (*nodeClientManagement, error) {
	info, err := os.Lstat(cfg.NodeIssuerTokenFile)
	real, realErr := filepath.EvalSymlinks(cfg.NodeIssuerTokenFile)
	if err != nil || realErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || real != filepath.Clean(cfg.NodeIssuerTokenFile) || info.Size() > 1024 {
		return nil, errors.New("node issuer management requires a private regular token file")
	}
	raw, err := os.ReadFile(cfg.NodeIssuerTokenFile)
	token := strings.TrimSpace(string(raw))
	if err != nil || len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \t\r\n\x00") {
		return nil, errors.New("node issuer management token is invalid")
	}
	if !filepath.IsAbs(cfg.NodeIssuerSocket) || deployment == "" {
		return nil, errors.New("node issuer management socket binding is invalid")
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		info, err := os.Lstat(cfg.NodeIssuerSocket)
		if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("node issuer management socket is unavailable")
		}
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", cfg.NodeIssuerSocket)
	}}
	return &nodeClientManagement{http: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("node issuer redirects are forbidden") }}, token: token, deployment: deployment}, nil
}
func (m *nodeClientManagement) call(ctx context.Context, method, path string, input, output any) int {
	raw, err := json.Marshal(input)
	if err != nil {
		return 400
	}
	if input == nil {
		raw = nil
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://issuer"+path, bytes.NewReader(raw))
	if err != nil {
		return 400
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := m.http.Do(req)
	if err != nil {
		return 503
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		if res.StatusCode >= 400 && res.StatusCode <= 499 {
			return res.StatusCode
		}
		return 503
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (128<<10)+1))
	if err != nil || len(body) > 128<<10 || domainDecode(body, output) != nil {
		return 503
	}
	return 200
}
func (m *nodeClientManagement) devices(ctx context.Context) ([]iscpnodeissuer.DeviceRecord, int) {
	var result nodeManagedDevices
	status := m.call(ctx, "GET", "/v1/devices", nil, &result)
	if status != 200 || result.SchemaVersion != 1 || result.Deployment != m.deployment || len(result.Devices) > 1024 {
		return nil, 503
	}
	return result.Devices, 200
}
func (m *nodeClientManagement) ready(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_, status := m.devices(ctx)
	return status == 200
}

// Only the ISCP domain path selects this coordinator. Normal /api/clients stays
// the Gateway's management primitive, so the private issuer cannot recurse here.
func (a *iscpDomainAdapter) clients(ctx context.Context, request iscpworkbench.Request) iscpDomainResult {
	if a.nodeClients == nil || a.config.Mode != "node-managed" {
		return domainError(501, "capability_unavailable")
	}
	principal := principalForRequest((&http.Request{}).WithContext(ctx))
	if a.config.Binding == nil || principal.OwnerID != a.config.Binding.OwnerID || a.config.Binding.DeploymentID != a.server.cfg.Gateway.DeploymentID {
		return domainError(403, "permission_denied")
	}
	if request.Operation == iscpworkbench.OperationClientsList {
		if len(request.Params) != 0 || !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, "GET", a.server.listClients, nil)
	}
	if !execution.UUID(request.OperationID) {
		return domainError(400, "operation_id_required")
	}
	if request.Operation == iscpworkbench.OperationClientsDelete {
		if len(request.Params) != 1 || !nodeClientID.MatchString(request.Params["id"]) || !domainEmpty(request.Body) {
			return domainError(400, "invalid_input")
		}
		return domainHTTP(ctx, request, "DELETE", a.server.deleteClient, map[string]string{"id": request.Params["id"]})
	}
	if request.Operation == iscpworkbench.OperationClientsIssue {
		var input struct {
			ClientName string `json:"client_name"`
		}
		if len(request.Params) != 0 || domainDecode(request.Body, &input) != nil {
			return domainError(400, "invalid_input")
		}
		name := strings.TrimSpace(input.ClientName)
		if name == "" {
			name = "SparkClaw Web"
		}
		if len([]rune(name)) > 80 || strings.ContainsAny(name, "\x00\r\n") {
			return domainError(400, "invalid_input")
		}
		var result struct {
			Credential   string `json:"credential"`
			ClientID     string `json:"client_id"`
			InvitationID string `json:"invitation_id"`
			Revision     uint64 `json:"revision"`
			State        string `json:"state"`
		}
		status := a.nodeClients.call(ctx, "POST", "/v1/invitations", map[string]any{"client_name": name, "operation_id": request.OperationID, "owner_id": principal.OwnerID, "deployment_id": a.server.cfg.Gateway.DeploymentID}, &result)
		if status != 200 {
			return domainError(status, "client_issuance_unavailable")
		}
		credential, err := iscpnodeissuer.ParseCredential(result.Credential, time.Now().UTC(), false)
		if err != nil || credential.ClientID != result.ClientID || credential.OwnerID != principal.OwnerID || credential.DeploymentID != a.server.cfg.Gateway.DeploymentID {
			return domainError(503, "client_issuance_unavailable")
		}
		client, found, err := a.server.store.GetClient(ctx, result.ClientID)
		if err != nil || !found || client.OwnerID != principal.OwnerID || client.Name != name || client.RevokedAt != nil {
			return domainError(503, "client_issuance_unavailable")
		}
		return domainJSON(200, map[string]any{"client": client, "token": result.Credential})
	}
	if request.Operation != iscpworkbench.OperationClientsRevoke || len(request.Params) != 1 || !nodeClientID.MatchString(request.Params["id"]) || !domainEmpty(request.Body) {
		return domainError(400, "invalid_input")
	}
	id := request.Params["id"]
	client, found, err := a.server.store.GetClient(ctx, id)
	if err != nil {
		return domainError(503, "clients_unavailable")
	}
	if !found || strings.TrimSpace(client.OwnerID) != principal.OwnerID {
		return domainError(404, "client_not_found")
	}
	devices, status := a.nodeClients.devices(ctx)
	if status != 200 {
		return domainError(status, "issuer_unavailable")
	}
	for _, device := range devices {
		if device.ClientID != id {
			continue
		}
		if device.Revision == 0 {
			return domainError(503, "issuer_unavailable")
		}
		if !device.RevocationComplete {
			expected := device.Revision
			if device.State == "revoked" {
				expected--
			}
			var result struct {
				State    string `json:"state"`
				Revision uint64 `json:"revision"`
				Complete bool   `json:"revocation_complete"`
			}
			status = a.nodeClients.call(ctx, "POST", "/v1/devices/"+id+"/revoke", map[string]any{"operation_id": request.OperationID, "expected_revision": expected}, &result)
			if status != 200 || result.State != "revoked" || !result.Complete || result.Revision != expected+1 {
				return domainError(503, "client_revocation_requires_reconciliation")
			}
		}
		current, found, err := a.server.store.GetClient(ctx, id)
		if err != nil || !found || current.OwnerID != principal.OwnerID || current.RevokedAt == nil {
			return domainError(503, "client_revocation_requires_reconciliation")
		}
		a.server.cancelClientConnections(id)
		return domainJSON(200, current)
	}
	return domainHTTP(ctx, request, "POST", a.server.revokeClient, map[string]string{"id": id})
}
