package gateway

import (
	"errors"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browsercontrol"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/browserhost"
	"github.com/Chiiz0/SparkClaw/services/link/contract"
)

func TestControllerReverseOutcomeRequiresLocalNotSentProof(t *testing.T) {
	for _, err := range []error{errLinkPeerUnavailable, &contract.Error{Code: contract.CodeBusy, NotSent: true}} {
		if _, got := controllerReverseOutcome(linkPeerResponse{}, err); !errors.Is(got, browsercontrol.ErrControllerNotSent) {
			t.Fatal(got)
		}
	}
	for _, response := range []linkPeerResponse{{Status: 403, Code: "permission_denied"}, {Status: 503, Code: "capability_unavailable"}, {Status: 413, Code: "resource_limit"}} {
		if _, err := controllerReverseOutcome(response, nil); err == nil || errors.Is(err, browsercontrol.ErrControllerNotSent) {
			t.Fatal(response, err)
		}
	}
}

func TestReverseRoutesFindTheInstallationSession(t *testing.T) {
	var routes workbenchBrowserRoutes
	identity := browserhost.Identity{OwnerID: "owner", ClientID: "client", InstallationID: "installation"}
	if _, err := routes.ReverseSession(identity); !errors.Is(err, browserhost.ErrUnavailable) {
		t.Fatal(err)
	}
	removeMissing := routes.add(func(string, string, string) (linkPeer, error) { return linkPeer{}, errLinkPeerUnavailable })
	defer removeMissing()
	done := make(chan struct{})
	remove := routes.add(func(owner, client, installation string) (linkPeer, error) {
		if owner != "owner" || client != "client" || installation != "installation" {
			return linkPeer{}, errLinkPeerUnavailable
		}
		return linkPeer{ID: "session", AuthorizationRevision: 3, Done: done}, nil
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
