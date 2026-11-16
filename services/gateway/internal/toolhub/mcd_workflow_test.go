package toolhub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcddining"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
)

type mcdWorkflowProvider struct {
	reads, cartReads, cartWrites, orders, statuses, commits, prepares, aborts int
}

func (*mcdWorkflowProvider) Validate(context.Context, mcdintegration.Envelope) error { return nil }
func (p *mcdWorkflowProvider) Read(context.Context, mcdintegration.Envelope, mcdintegration.Execution, string, json.RawMessage) (json.RawMessage, error) {
	p.reads++
	return json.RawMessage(`{"schema_version":1,"result_encoding":"bounded-json-v1","result_json":"{\"stores\":[]}"}`), nil
}
func (p *mcdWorkflowProvider) Cart(_ context.Context, _ mcdintegration.Envelope, _ mcdintegration.Execution, _ string, _ json.RawMessage, _ string, authority *mcdintegration.CartAuthority) (json.RawMessage, error) {
	if authority != nil {
		p.cartWrites++
	} else {
		p.cartReads++
	}
	return json.Marshal(map[string]any{"schema_version": 1, "result_encoding": "bounded-json-v1", "result_json": `{"state":"MATCHED","cart_matches_requested":true,"items":[],"cart_ref":"fixture-cart"}`, "write_claimed": authority != nil, "order_created": false, "payment_attempted": false, "dispatch_state": "dispatched"})
}
func (p *mcdWorkflowProvider) Prepare(context.Context, json.RawMessage) (map[string]any, error) {
	p.prepares++
	return nil, errors.New("unexpected preparation")
}
func (p *mcdWorkflowProvider) Abort(context.Context, mcdintegration.QuoteScope, string, int64, string) (map[string]any, error) {
	p.aborts++
	return nil, errors.New("unexpected abort")
}
func (p *mcdWorkflowProvider) Commit(context.Context, mcdintegration.QuoteScope, mcdintegration.Handoff, string, map[string]any) (map[string]any, error) {
	p.commits++
	return nil, errors.New("unexpected payment")
}
func (p *mcdWorkflowProvider) Status(context.Context, mcdintegration.QuoteScope, string) (map[string]any, error) {
	p.statuses++
	return nil, errors.New("unexpected status")
}
func (p *mcdWorkflowProvider) Orders(context.Context, mcdintegration.QuoteScope) (map[string]any, error) {
	p.orders++
	return map[string]any{"orders": []any{}, "policy": map[string]any{"max_order_cents": 2000}}, nil
}

func mcdWorkflowHubFixture(t *testing.T) (*ToolHub, *store.MemoryStore, app.Session, *mcdWorkflowProvider) {
	t.Helper()
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["issued_at_ms"] = time.Now().UnixMilli() - 1000
	fields["session_not_after_ms"] = time.Now().Add(15 * time.Minute).UnixMilli()
	fields["max_requests"] = 128
	ops := []string{"stores_favorites", "cart_snapshot", "cart_add_simple", "cart_add_configured"}
	slices.Sort(ops)
	fields["operations"] = ops
	delete(fields, "envelope_digest")
	raw, _ = json.Marshal(fields)
	canonical, err := mcdintegration.Canonical(raw)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(canonical)
	fields["envelope_digest"] = hex.EncodeToString(hash[:])
	raw, _ = json.Marshal(fields)
	envelope, err := mcdintegration.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	provider := &mcdWorkflowProvider{}
	manager, err := mcdintegration.NewManager(credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("53", 32)}), provider, envelope.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	i := envelope.Identity
	principal := mcdintegration.Principal{DeploymentID: i.DeploymentID, OwnerID: i.OwnerID, ClientID: i.ClientID, InstallationID: i.InstallationID}
	if _, err := manager.Provision(t.Context(), principal, raw); err != nil {
		t.Fatal(err)
	}
	selection, err := mcddining.NewSelection(t.Context(), manager, principal, "toolhub-workflow")
	if err != nil {
		t.Fatal(err)
	}
	selection.WithCart(t.TempDir(), provider).WithPurchase(true).WithStatus(true)
	local := store.NewMemoryStore()
	root := t.TempDir()
	session := storetest.MustCreateSessionWithScope(t, local, "MCD boundary", i.OwnerID, root, "web", false)
	cfg := config.Default()
	cfg.MCDSelection.Enabled, cfg.MCDSelection.CartEnabled, cfg.MCDSelection.PurchaseEnabled = true, true, true
	cfg.Storage.ArtifactBackend, cfg.Storage.ArtifactDir = "filesystem", t.TempDir()
	parent := New(cfg, local)
	t.Cleanup(func() { _ = parent.Close() })
	hub, err := parent.WithExecutionScope(local, ExecutionResources{OwnerID: session.OwnerID, WorkspaceRoot: root, Artifacts: artifact.NewStore(cfg.Storage), MCDSelection: selection})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = hub.Close(); _ = selection.Close(t.Context()) })
	if !hub.MCDOrderingAvailable() || !hub.MCDStatusAvailable() {
		t.Fatal("test must supply working read/cart/status/purchase resources")
	}
	return hub, local, session, provider
}

func mcdWorkflowBoundRun(session, id string, node app.WorkflowNodeID) app.AgentRun {
	return app.AgentRun{ID: id, SessionID: session, State: "running", StartedAt: time.Now().UTC(), Workflow: &app.WorkflowState{
		Plan: app.WorkflowPlan{ProfileID: app.WorkflowMCDOrder}, ActiveNodeIDs: []app.WorkflowNodeID{node},
		Nodes: map[app.WorkflowNodeID]app.WorkflowNodeState{node: {Status: app.WorkflowNodeActive}},
		MCDOrder: &app.MCDOrderState{Store: map[string]any{"store_code": "3330613"}, PickupAt: "2090-01-01T12:00:00+08:00", EatType: "locker-in", LimitCents: 2000,
			Lines: []app.MCDOrderLine{{Quantity: 1, Selection: map[string]any{"product_code": "1001", "offer_id": "official-offer", "quantity": 1}}}},
	}}
}

func mcdWorkflowCartArgs() map[string]any {
	return map[string]any{"operation": "cart_add_simple", "store_code": "3330613", "pickup_at": "2090-01-01T12:00:00+08:00", "eat_type": "locker-in", "max_amount_cny": "20.00", "items": []any{map[string]any{"product_code": "1001", "offer_id": "official-offer", "quantity": 1}}}
}

func mcdWorkflowPaymentArgs() map[string]any {
	return map[string]any{"purchase_id": "fixture-purchase", "purchase_digest": strings.Repeat("a", 64), "currency": "CNY", "payment_method": "ARCHCARD", "store_code": "3330613", "pickup_at": "2090-01-01T12:00:00+08:00", "eat_type": "locker-in", "store_name": "官方门店", "amount_cents": 1500, "credential_revision": 1, "items": []any{map[string]any{"product_code": "1001", "quantity": 1}}, "discount_names": []string{}}
}

func TestMCDWorkflowBoundaryRejectsExternalAndWrongStageWithLiveResources(t *testing.T) {
	hub, local, session, provider := mcdWorkflowHubFixture(t)
	tools := []struct {
		name string
		node app.WorkflowNodeID
		args map[string]any
	}{
		{"mcd.selection.read", "context", map[string]any{"operation": "stores_favorites"}},
		{"mcd.cart.read", "cart", map[string]any{"operation": "cart_snapshot"}},
		{"mcd.cart.write", "cart", mcdWorkflowCartArgs()},
		{"mcd.quote.prepare", "quote", map[string]any{"operation": "checkout_abort"}},
		{"mcd.purchase.status", "guard", map[string]any{}},
		{"mcd.purchase.commit", "pay", mcdWorkflowPaymentArgs()},
	}
	for _, tool := range tools {
		if _, found := hub.Definition(tool.name); !found || hub.requireResource(tool.name) != nil {
			t.Fatal("workflow test would be an indirect resource denial", tool.name)
		}
		for _, failure := range []string{"missing_run", "missing_workflow", "wrong_profile", "wrong_node", "inactive_node", "multiple_nodes", "wrong_session", "missing_order"} {
			t.Run(tool.name+"/"+failure, func(t *testing.T) {
				id := tool.name + "-" + failure
				run := mcdWorkflowBoundRun(session.ID, id, tool.node)
				switch failure {
				case "missing_workflow":
					run.Workflow = nil
				case "wrong_profile":
					run.Workflow.Plan.ProfileID = app.WorkflowBrowserFormDraft
				case "wrong_node":
					run.Workflow.ActiveNodeIDs = []app.WorkflowNodeID{"intent"}
					run.Workflow.Nodes["intent"] = app.WorkflowNodeState{Status: app.WorkflowNodeActive}
				case "inactive_node":
					run.Workflow.Nodes[tool.node] = app.WorkflowNodeState{Status: app.WorkflowNodeSucceeded}
				case "multiple_nodes":
					run.Workflow.ActiveNodeIDs = append(run.Workflow.ActiveNodeIDs, "intent")
				case "wrong_session":
					run.SessionID = "foreign-session"
				case "missing_order":
					run.Workflow.MCDOrder = nil
				}
				if failure != "missing_run" {
					testSaveRun(local, run)
				}
				if _, err := hub.Execute(t.Context(), tool.name, tool.args, session.ID, id); err == nil || err.Error() != "mcd_order_workflow_required" {
					t.Fatalf("%s escaped workflow guard or failed for a different reason: %v", tool.name, err)
				}
			})
		}
	}
	if *provider != (mcdWorkflowProvider{}) {
		t.Fatalf("external/wrong-stage requests reached original provider: %+v", provider)
	}
	// Positive controls show the same authenticated resources work once the
	// correct deterministic stage is active. Payment still needs its approval.
	for _, tool := range tools {
		id := "valid-" + tool.name
		testSaveRun(local, mcdWorkflowBoundRun(session.ID, id, tool.node))
		_, err := hub.Execute(t.Context(), tool.name, tool.args, session.ID, id)
		if tool.name == "mcd.purchase.commit" {
			if err == nil || err.Error() != "mcd_purchase_approval_required" {
				t.Fatalf("payment bypassed immutable approval: %v", err)
			}
		} else if err != nil {
			t.Fatalf("valid %s rejected: %v", tool.name, err)
		}
	}
	if provider.reads != 1 || provider.cartReads != 1 || provider.cartWrites != 1 || provider.orders != 1 || provider.commits != 0 {
		t.Fatalf("positive control dispatches = %+v", provider)
	}
}

func TestMCDCartApprovalExemptionCannotChangeFrozenPlan(t *testing.T) {
	hub, local, session, provider := mcdWorkflowHubFixture(t)
	definition, found := hub.Definition("mcd.cart.write")
	if !found || definition.RequiresApproval {
		t.Fatal("test must exercise the reversible cart approval exemption")
	}
	run := mcdWorkflowBoundRun(session.ID, "frozen-cart", "cart")
	// Bookkeeping is not part of the official cart body and must not make the
	// valid original plan fail the exact comparison.
	run.Workflow.MCDOrder.Lines[0].Selection["_answered"] = map[string]any{"modifier_1": true}
	testSaveRun(local, run)
	for _, mutation := range []string{"product", "quantity", "offer", "coupon", "store", "time", "method"} {
		t.Run(mutation, func(t *testing.T) {
			args := mcdWorkflowCartArgs()
			item := args["items"].([]any)[0].(map[string]any)
			switch mutation {
			case "product":
				item["product_code"] = "different-product"
			case "quantity":
				item["quantity"] = 2
			case "offer":
				item["offer_id"] = "different-offer"
			case "coupon":
				item["coupon_ref"] = "unselected-coupon"
			case "store":
				args["store_code"] = "another-store"
			case "time":
				args["pickup_at"] = "2090-01-01T13:00:00+08:00"
			case "method":
				args["eat_type"] = "take-in-store"
			}
			if _, err := hub.Execute(t.Context(), "mcd.cart.write", args, session.ID, run.ID); err == nil || err.Error() != "mcd_cart_outside_frozen_plan" {
				t.Fatalf("changed %s reached reversible write: %v", mutation, err)
			}
		})
	}
	if provider.cartWrites != 0 {
		t.Fatal("invalid plan reached cart writer", provider.cartWrites)
	}
	if _, err := hub.Execute(t.Context(), "mcd.cart.write", mcdWorkflowCartArgs(), session.ID, run.ID); err != nil || provider.cartWrites != 1 {
		t.Fatalf("original frozen cart not dispatchable: %v, writes=%d", err, provider.cartWrites)
	}
}

// The cart node may re-read pickup slots for a stale default time, but it
// cannot use the read tool for menus, coupons or any other selection evidence.
func TestMCDCartNodeReadsOnlyPickupSlots(t *testing.T) {
	hub, local, session, provider := mcdWorkflowHubFixture(t)
	for _, op := range []string{"menu_list", "coupons_list", "stores_favorites", "product_detail"} {
		id := "cart-read-" + op
		testSaveRun(local, mcdWorkflowBoundRun(session.ID, id, "cart"))
		if _, err := hub.Execute(t.Context(), "mcd.selection.read", map[string]any{"operation": op}, session.ID, id); err == nil || err.Error() != "mcd_order_workflow_required" {
			t.Fatalf("cart node read %s: %v", op, err)
		}
	}
	if provider.reads != 0 {
		t.Fatalf("rejected cart-node reads reached the provider: %+v", provider)
	}
	// This fixture's envelope does not grant pickup_slots; passing the workflow
	// guard is the point here. The ordering flow test covers the full re-read.
	testSaveRun(local, mcdWorkflowBoundRun(session.ID, "cart-read-slots", "cart"))
	if _, err := hub.Execute(t.Context(), "mcd.selection.read", map[string]any{"operation": "pickup_slots", "store_code": "3330613"}, session.ID, "cart-read-slots"); err != nil && err.Error() == "mcd_order_workflow_required" {
		t.Fatalf("cart node pickup slot re-read was rejected by the workflow guard: %v", err)
	}
}
