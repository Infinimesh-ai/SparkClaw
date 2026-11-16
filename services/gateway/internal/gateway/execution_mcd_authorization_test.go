package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpauth"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

func mcdAuthorizationFixture(t *testing.T) (*Server, iscpworkbench.Config, iscpworkbench.SessionInfo) {
	t.Helper()
	server, repo, transport, _ := workbenchISCPFixture(t, nil)
	server.cfg.MCDSelection = config.MCDSelectionConfig{Enabled: true, CartEnabled: true, PurchaseEnabled: true, QualifiedEatTypes: []string{"locker-in"}}
	var err error
	server.mcdSelection, err = mcdintegration.NewManager(credential.New(repo, credential.Options{Key: strings.Repeat("09", 32)}), mcdFixtureRunner{}, transport.Binding.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	server.tools = toolhub.New(server.cfg, repo)
	t.Cleanup(func() { _ = server.tools.Close() })
	ops := []string{
		iscpworkbench.OperationMCDSessionProvision,
		iscpworkbench.OperationMCDCheckoutPrepare,
		iscpworkbench.OperationMCDCheckoutAbort,
		iscpworkbench.OperationMCDOperationStatus,
		iscpworkbench.OperationMCDCheckoutCommit,
		iscpworkbench.OperationMCDOrdersQuery,
		iscpworkbench.OperationApprovalsDecide,
	}
	transport.QualifiedCapabilities = slices.Clone(ops)
	session := iscpworkbench.SessionInfo{
		Profile: iscpworkbench.ProfileV2, GrantRevision: 1,
		NegotiatedOperations: slices.Clone(ops), QualifiedOperations: append(slices.Clone(ops), iscpworkbench.OperationToolsInvoke),
		CheckAuthorization: func(context.Context) (iscpauth.Policy, error) {
			return iscpauth.Policy{Version: 2, State: iscpauth.Active, Revision: 1}, nil
		},
	}
	return server, transport, session
}

func TestMCDExecutionAuthorizationSeparatesStatusFromOrderingFlags(t *testing.T) {
	server, _, session := mcdAuthorizationFixture(t)
	for _, enabled := range []bool{false, true} {
		for _, cart := range []bool{false, true} {
			for _, purchase := range []bool{false, true} {
				t.Run(fmt.Sprintf("enabled_%t_cart_%t_purchase_%t", enabled, cart, purchase), func(t *testing.T) {
					server.cfg.MCDSelection.Enabled, server.cfg.MCDSelection.CartEnabled, server.cfg.MCDSelection.PurchaseEnabled = enabled, cart, purchase
					a := server.workbenchExecutionAuthorization(session)
					if a.AllowMCDStatus != enabled || a.AllowMCDSelection != enabled || a.AllowMCDPrepare != (enabled && cart) || a.AllowMCDPurchase != (enabled && cart && purchase) {
						t.Fatalf("wrong MCD flags: %+v", a)
					}
					if slices.Contains(a.AllowedTools, "mcd.purchase.status") != enabled || slices.Contains(a.AllowedTools, "mcd.purchase.commit") != (enabled && cart && purchase) {
						t.Fatalf("status and purchase tool admission not independent: %v", a.AllowedTools)
					}
				})
			}
		}
	}
}

func TestMCDExecutionAuthorizationRequiresEachNegotiatedAndQualifiedOperation(t *testing.T) {
	server, _, session := mcdAuthorizationFixture(t)
	for _, op := range session.NegotiatedOperations {
		for _, qualification := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s_qualified_%t", op, qualification), func(t *testing.T) {
				candidate := session
				without := func(ops []string) []string {
					return slices.DeleteFunc(slices.Clone(ops), func(name string) bool { return name == op })
				}
				if qualification {
					candidate.QualifiedOperations = without(candidate.QualifiedOperations)
				} else {
					candidate.NegotiatedOperations = without(candidate.NegotiatedOperations)
				}
				a := server.workbenchExecutionAuthorization(candidate)
				wantStatus := op != iscpworkbench.OperationMCDOperationStatus && op != iscpworkbench.OperationMCDOrdersQuery
				if a.AllowMCDPurchase || a.AllowMCDStatus != wantStatus || slices.Contains(a.AllowedTools, "mcd.purchase.status") != wantStatus {
					t.Fatalf("missing operation did not limit only the dependent capability: %+v", a)
				}
			})
		}
	}
	statusOps := []string{iscpworkbench.OperationMCDOperationStatus, iscpworkbench.OperationMCDOrdersQuery}
	session.NegotiatedOperations = statusOps
	session.QualifiedOperations = append(slices.Clone(statusOps), iscpworkbench.OperationToolsInvoke)
	if a := server.workbenchExecutionAuthorization(session); !a.AllowMCDStatus || a.AllowMCDSelection || a.AllowMCDPurchase || !slices.Contains(a.AllowedTools, "mcd.purchase.status") {
		t.Fatalf("status-only transport cannot recover: %+v", a)
	}
	session.CheckAuthorization = nil
	if a := server.workbenchExecutionAuthorization(session); a.AllowMCDStatus || len(a.AllowedTools) != 0 {
		t.Fatalf("unauthenticated session admitted status: %+v", a)
	}
}

func TestMCDStatusCapabilitySurvivesRollbackWithoutProvision(t *testing.T) {
	server, transport, session := mcdAuthorizationFixture(t)
	server.cfg.MCDSelection.CartEnabled, server.cfg.MCDSelection.PurchaseEnabled = false, false
	statusOps := []string{iscpworkbench.OperationMCDOperationStatus, iscpworkbench.OperationMCDOrdersQuery}
	session.NegotiatedOperations, session.QualifiedOperations = slices.Clone(statusOps), slices.Clone(statusOps)
	transport.QualifiedCapabilities = slices.Clone(statusOps)
	adapter := &iscpDomainAdapter{server: server, config: transport}
	inspect := func() map[string]iscpDomainCapability {
		t.Helper()
		result := adapter.capabilitiesWithSession(domainTestContext(t), domainTestRequest(iscpworkbench.OperationCapabilitiesGet, nil), session)
		var manifest struct {
			Capabilities []iscpDomainCapability `json:"capabilities"`
		}
		if result.status != 200 || json.Unmarshal(result.body, &manifest) != nil {
			t.Fatalf("capabilities %d %s", result.status, result.body)
		}
		out := map[string]iscpDomainCapability{}
		for _, capability := range manifest.Capabilities {
			out[capability.ID] = capability
		}
		return out
	}
	if got := inspect(); !got["mcd_status"].Enabled || got["mcd_purchase"].Enabled || !slices.Equal(got["mcd_status"].Operations, statusOps) {
		t.Fatalf("rollback status capability = %+v", got)
	}
	for _, missing := range []string{"negotiated", "qualified", "responder", "enabled", "manager"} {
		t.Run(missing, func(t *testing.T) {
			originalSession, originalTransport, manager := session, adapter.config, server.mcdSelection
			defer func() {
				session, adapter.config, server.mcdSelection = originalSession, originalTransport, manager
				server.cfg.MCDSelection.Enabled = true
			}()
			switch missing {
			case "negotiated":
				session.NegotiatedOperations = statusOps[:1]
			case "qualified":
				session.QualifiedOperations = statusOps[:1]
			case "responder":
				adapter.config.QualifiedCapabilities = statusOps[:1]
			case "enabled":
				server.cfg.MCDSelection.Enabled = false
			case "manager":
				server.mcdSelection = nil
			}
			if got := inspect()["mcd_status"]; got.Enabled {
				t.Fatalf("status enabled without %s: %+v", missing, got)
			}
		})
	}
}
