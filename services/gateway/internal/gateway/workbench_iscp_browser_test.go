package gateway

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	wb "github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
)

func TestISCPBrowserHasNoPollingHostOperations(t *testing.T) {
	for _, name := range wb.Operations() {
		if strings.HasPrefix(name, "browser.host.") {
			t.Fatalf("polling host operation %s remains", name)
		}
	}
	spec, ok := wb.LookupOperation(wb.OperationBrowserCommand)
	if !ok || spec.Direction != "reverse" {
		t.Fatal("reverse browser.command is missing")
	}
	server, f, cfg := newMailPopupFixture(t, "file")
	adapter := &iscpDomainAdapter{server: server, config: cfg}
	result := adapter.capabilitiesWithSession(domainTestContext(t), domainTestRequest(wb.OperationCapabilitiesGet, nil), wb.SessionInfo{Profile: wb.ProfileV2})
	var body struct {
		Capabilities []iscpDomainCapability `json:"capabilities"`
		Limits       map[string]any         `json:"limits"`
	}
	f.must(json.Unmarshal(result.body, &body))
	if body.Limits["browser_reply_json_bytes"] != float64(browserhost.MaxResultBytes) {
		t.Fatalf("browser result budget %v", body.Limits["browser_reply_json_bytes"])
	}
	for _, capability := range body.Capabilities {
		if capability.ID == "browser" {
			if !slices.Equal(capability.Operations, []string{wb.OperationBrowserCommand, wb.OperationBrowserReceipt, wb.OperationBrowserReconcile}) || !capability.Supported {
				t.Fatalf("browser capability %+v", capability)
			}
			return
		}
	}
	t.Fatal("browser capability missing")
}

func TestISCPBrowserRoutesFindTheInstallationSession(t *testing.T) {
	var routes workbenchBrowserRoutes
	identity := browserhost.Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}
	if _, err := routes.ReverseSession(identity); !errors.Is(err, browserhost.ErrUnavailable) {
		t.Fatal(err)
	}
	removeMissing := routes.add(func(string, string, string) (wb.PeerSession, error) { return wb.PeerSession{}, wb.ErrPeerUnavailable })
	defer removeMissing()
	done := make(chan struct{})
	remove := routes.add(func(owner, client, installation string) (wb.PeerSession, error) {
		if owner != "owner" || client != "client" || installation != "installation" {
			return wb.PeerSession{}, wb.ErrPeerUnavailable
		}
		return wb.PeerSession{ID: "session", AuthorizationRevision: 3, Done: done}, nil
	})
	session, err := routes.ReverseSession(identity)
	if err != nil || session.SessionID() != "session" || session.AuthorizationRevision() != 3 || session.(browserPeerSession).session.InstallationID != "installation" {
		t.Fatal(session, err)
	}
	remove()
	if _, err := routes.ReverseSession(identity); !errors.Is(err, browserhost.ErrUnavailable) {
		t.Fatal("removed transport still routes", err)
	}
}

func TestISCPBrowserCommandOutcomeOnlyProvesRefusalsBeforeExecution(t *testing.T) {
	body := json.RawMessage(`{"schema_version":1}`)
	if out, err := reverseCommandOutcome(wb.Response{Status: 200, Body: body}, nil); err != nil || string(out) != string(body) {
		t.Fatal(out, err)
	}
	for _, refused := range []wb.Response{{Status: 403, Code: wb.ErrorPermissionDenied}, {Status: 503, Code: wb.ErrorCapabilityUnavailable}} {
		if _, err := reverseCommandOutcome(refused, nil); !errors.Is(err, browserhost.ErrNotDispatched) {
			t.Fatal(refused, err)
		}
	}
	if _, err := reverseCommandOutcome(wb.Response{}, wb.ErrPeerUnavailable); !errors.Is(err, browserhost.ErrNotDispatched) {
		t.Fatal(err)
	}
	for _, uncertain := range []wb.Response{{Status: 504, Code: wb.ErrorOutcomeUnknown}, {Status: 503, Code: wb.ErrorOutcomeUnknown}, {Status: 413, Code: wb.ErrorResourceLimit}, {Status: 500}} {
		if _, err := reverseCommandOutcome(uncertain, nil); err == nil || errors.Is(err, browserhost.ErrNotDispatched) {
			t.Fatal(uncertain, err)
		}
	}
	if _, err := reverseCommandOutcome(wb.Response{}, errors.New("workbench session disconnected")); err == nil || errors.Is(err, browserhost.ErrNotDispatched) {
		t.Fatal(err)
	}
}
