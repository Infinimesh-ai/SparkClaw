package gateway

import (
	"encoding/json"
	"testing"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/execution"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

func TestMCDExecutionResourcesRetainRecoveryAfterOrderingDisabled(t *testing.T) {
	server, transport, session := mcdAuthorizationFixture(t)
	envelope := workbenchISCPEnvelope()
	principal := mcdintegration.Principal{DeploymentID: envelope.DeploymentID, OwnerID: envelope.OwnerID, ClientID: envelope.ClientID, InstallationID: envelope.InstallationID}
	raw := mcdFixtureEnvelope(t, transport.Binding)
	var credential mcdintegration.Envelope
	if err := json.Unmarshal(raw, &credential); err != nil {
		t.Fatal(err)
	}
	if _, err := server.mcdSelection.Provision(t.Context(), principal, raw); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	assertResources := func(wantPurchase, wantStatus bool) {
		t.Helper()
		ctx := withExecutionAuthorization(t.Context(), server.workbenchExecutionAuthorization(session))
		selection := server.executionMCDSelection(ctx, envelope, root, execution.NewBudget(execution.TaskBytes))
		if selection == nil {
			t.Fatal("MCD execution resources unavailable")
		}
		defer selection.Close(t.Context())
		if selection.OrderingAvailable() != wantPurchase || selection.StatusAvailable() != wantStatus {
			t.Fatalf("resources purchase/status = %t/%t; want %t/%t", selection.OrderingAvailable(), selection.StatusAvailable(), wantPurchase, wantStatus)
		}
	}
	assertResources(true, true)
	server.cfg.MCDSelection.PurchaseEnabled = false
	assertResources(false, true)
	server.cfg.MCDSelection.CartEnabled = false
	assertResources(false, true)
	session.NegotiatedOperations = []string{iscpworkbench.OperationMCDOperationStatus, iscpworkbench.OperationMCDOrdersQuery}
	session.QualifiedOperations = session.NegotiatedOperations
	assertResources(false, true)
	if _, err := server.mcdSelection.Revoke(t.Context(), principal, credential.Identity, credential.ConnectionID, "resource-recovery-revoke"); err != nil {
		t.Fatal(err)
	}
	// Closed session material cannot be used for a new order, but its original
	// durable identity still selects the status host and bookkeeping.
	assertResources(false, true)
	server.cfg.MCDSelection.CartEnabled, server.cfg.MCDSelection.PurchaseEnabled = true, true
	session.NegotiatedOperations = append(session.NegotiatedOperations, iscpworkbench.OperationMCDSessionProvision, iscpworkbench.OperationMCDCheckoutPrepare, iscpworkbench.OperationMCDCheckoutAbort, iscpworkbench.OperationMCDCheckoutCommit, iscpworkbench.OperationApprovalsDecide)
	session.QualifiedOperations = session.NegotiatedOperations
	assertResources(false, true)
	ctx := withExecutionAuthorization(t.Context(), server.workbenchExecutionAuthorization(session))
	if selection := server.executionMCDSelection(ctx, envelope, root, execution.NewBudget(1)); selection != nil {
		t.Fatal("capacity failure admitted MCD resources")
	}
	server.cfg.MCDSelection.Enabled = false
	ctx = withExecutionAuthorization(t.Context(), server.workbenchExecutionAuthorization(session))
	if selection := server.executionMCDSelection(ctx, envelope, root, execution.NewBudget(execution.TaskBytes)); selection != nil {
		t.Fatal("disabled integration admitted MCD resources")
	}
	if selection := server.executionMCDSelection(t.Context(), envelope, root, execution.NewBudget(execution.TaskBytes)); selection != nil {
		t.Fatal("unauthenticated execution admitted MCD resources")
	}
}
