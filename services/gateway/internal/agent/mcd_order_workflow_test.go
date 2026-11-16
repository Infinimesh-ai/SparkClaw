package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/artifact"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/credential"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcddining"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/policy"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/store"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/storetest"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/toolhub"
)

type mcdFlowFixture struct {
	policyMaxCents                                     int
	offline                                            bool
	pickup                                             string
	calls                                              []string
	cart                                               []map[string]any
	quote                                              mcdintegration.QuoteEnvelope
	commits, aborts, statuses, models, quotes          int
	discountMissing, unknown, noFavorites, unsupported bool
	noPickupCode, cancelled                            bool
	maxAmountText                                      string
	quoteTTL                                           time.Duration
	changedPrice                                       bool
	favorites, items, ttlQuote                         int
	unsupportedStores                                  map[string]bool
}

func mcdTestHash(v any) string {
	raw, _ := json.Marshal(v)
	canonical, _ := mcdintegration.Canonical(raw)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}
func mcdTestJSON(v any) json.RawMessage                                           { raw, _ := json.Marshal(v); return raw }
func (f *mcdFlowFixture) Validate(context.Context, mcdintegration.Envelope) error { return nil }
func (f *mcdFlowFixture) Read(_ context.Context, _ mcdintegration.Envelope, _ mcdintegration.Execution, op string, args json.RawMessage) (json.RawMessage, error) {
	f.calls = append(f.calls, op)
	var a map[string]any
	_ = json.Unmarshal(args, &a)
	var out map[string]any
	switch op {
	case "stores_favorites":
		rows := []map[string]any{}
		if !f.noFavorites {
			rows = append(rows, map[string]any{"storeCode": "3330613", "storeName": "测试店", "storeAddress": "测试路1号"})
			for i := 2; i <= f.favorites; i++ {
				rows = append(rows, map[string]any{"storeCode": fmt.Sprintf("fav-%d", i), "storeName": fmt.Sprintf("收藏店%d", i), "storeAddress": "测试路"})
			}
		}
		out = map[string]any{"list": rows}
	case "cities_list":
		out = map[string]any{"cities": []map[string]any{{"city_code": "330100", "name": "杭州"}}}
	case "stores_search":
		out = map[string]any{"stores": []map[string]any{{"store_code": "3330613", "name": "测试店", "address": "测试路1号", "opening_time": "08:00", "closing_time": "23:00"}}, "has_more": false}
	case "pickup_slots":
		out = map[string]any{"dates": []any{map[string]any{"periods": []any{map[string]any{"pickup_times": []string{f.pickup}}}}}}
	case "pickup_methods":
		out = map[string]any{"methods": []map[string]any{{"code": "locker-in", "name": "取餐柜", "checkout_supported": true}, {"code": "take-in-store", "name": "到店取餐", "checkout_supported": true}}}
		if f.unsupported || f.unsupportedStores[mcdText(mcdMap(a["request"])["store_code"])] {
			out["methods"] = []map[string]any{{"code": "eat-in", "name": "堂食", "checkout_supported": false}}
		}
	case "menu_list":
		out = map[string]any{"products": []map[string]any{{"product_code": "1001", "name": "麦辣鸡腿堡套餐", "offer_id": "menu-1", "display_price_cny": "18.00", "product_type": "7"}}}
	case "product_detail":
		out = map[string]any{"product_code": "1001", "name": "麦辣鸡腿堡套餐", "product_type": "7", "offer_id": a["offer_id"], "max_quantity": 20, "standard_configuration_supported": true, "modifiers": map[string]any{"supported": true, "groups": []any{}}, "groups": []map[string]any{{"round": 1, "name": "饮料", "min_quantity": 1, "max_quantity": 1, "choices": []map[string]any{{"product_code": "2001", "name": "可乐", "max_quantity": 1, "available": true, "extra_cny": "0.00", "standard_configuration_supported": true, "modifiers": map[string]any{"supported": true, "groups": []any{}}}}}}}
	case "coupons_list":
		out = map[string]any{"coupons": []map[string]any{{"coupon_ref": "coupon-1", "name": "套餐优惠", "available_for_request": true, "configuration_path": "coupon_products", "remaining_today": 1, "remaining_total": 1}}}
	case "coupon_products":
		out = map[string]any{"products": []map[string]any{{"product_code": "1001", "offer_id": "coupon-offer-1", "display_price_cny": "15.00", "cart_configuration_supported": true, "max_quantity_before_detail_check": 1}}}
	default:
		return nil, errors.New("unexpected read " + op)
	}
	return mcdTestJSON(map[string]any{"schema_version": 1, "result_encoding": "bounded-json-v1", "result_json": string(mcdTestJSON(out))}), nil
}
func (f *mcdFlowFixture) Cart(_ context.Context, _ mcdintegration.Envelope, _ mcdintegration.Execution, op string, raw json.RawMessage, _ string, auth *mcdintegration.CartAuthority) (json.RawMessage, error) {
	f.calls = append(f.calls, op)
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	out := map[string]any{}
	switch op {
	case "cart_snapshot":
		out = map[string]any{"items": f.cart, "cart_ref": "cart-" + mcdTestHash([]any{f.cart, len(f.calls)})[:32]}
	case "cart_clear":
		f.cart = []map[string]any{}
		out["state"] = "CLEARED"
		out["original_clear_verified"] = true
	case "cart_add_configured":
		f.cart = []map[string]any{}
		for _, selected := range mcdRows(args["items"]) {
			// Match the pinned provider's public cart_items projection, rather
			// than echoing request selections into an unrealistic receipt.
			components := []map[string]any{}
			for _, group := range mcdRows(selected["combo"]) {
				parts := []map[string]any{}
				for _, part := range mcdRows(group["items"]) {
					parts = append(parts, map[string]any{"code": part["product_code"], "name": "可乐", "quantity": part["quantity"], "customization": map[string]any{"options": []any{}, "items": []any{}}})
				}
				components = append(components, map[string]any{"round": group["round"], "comboProducts": parts})
			}
			item := map[string]any{"product_code": selected["product_code"], "name": "麦辣鸡腿堡套餐", "quantity": selected["quantity"], "product_type": "7", "components": components, "configuration": map[string]any{"customization": map[string]any{"options": []any{}, "items": []any{}}}, "coupon_fingerprint": nil, "amount_cents": 1800}
			if selected["coupon_ref"] != nil {
				item["coupon_fingerprint"] = mcdTestHash(selected["coupon_ref"])
				item["amount_cents"] = 1500
			}
			f.cart = append(f.cart, item)
		}
		out = map[string]any{"state": "MATCHED", "items": f.cart, "cart_matches_requested": true, "write_receipt_verified": true}
	case "checkout_quote_pickup":
		out = map[string]any{"state": "READY", "quote_id": "fixture-quote"}
	default:
		return nil, errors.New("unexpected cart " + op)
	}
	dispatch := "not_dispatched"
	if auth != nil {
		dispatch = "dispatched"
	}
	return mcdTestJSON(mcdintegration.CartResult{SchemaVersion: 1, Encoding: "bounded-json-v1", ResultJSON: string(mcdTestJSON(out)), DispatchState: dispatch, WriteClaimed: auth != nil}), nil
}
func (f *mcdFlowFixture) Quote(_ context.Context, _ mcdintegration.Envelope, x mcdintegration.Execution, q mcdintegration.QuoteRequest, _ string) (json.RawMessage, error) {
	f.quotes++
	f.calls = append(f.calls, "checkout_quote")
	amount, discount := 1800, 0
	names := []string{}
	items := []map[string]any{}
	for _, item := range f.cart {
		items = append(items, mcdClone(item))
		if item["coupon_fingerprint"] != nil {
			amount = 1500
			discount = 300
			names = []string{"套餐优惠"}
		}
	}
	if f.discountMissing && f.quotes == 1 {
		names = nil
		discount = 0
		amount = 1800
	}
	fields := map[string]any{"schema_version": 1, "canonicalization": "mcd-json-v1", "adapter_revision": "fixture", "scope": mcdintegration.QuoteScope{Identity: x.Identity, CredentialRevision: x.CredentialRevision, ExecutionID: x.ExecutionID}, "source_quote_ref": q.SourceQuoteRef, "purchase_id": q.PurchaseID, "quote_revision": q.QuoteRevision, "handoff_epoch": q.HandoffEpoch, "generated_at_ms": time.Now().UnixMilli(), "expires_at_ms": time.Now().Add(time.Minute).UnixMilli(), "currency": "CNY", "payment_method": "ARCHCARD", "cart_digest": strings.Repeat("a", 64), "quote": map[string]any{"store_code": "3330613", "store_name": "测试店", "pickup_at": f.pickup, "eat_type": "locker-in", "items": items, "discount_names": names, "amount_cents": amount, "regular_cents": 1800, "discount_cents": discount}, "pickup_receipt": nil, "purchase_digest": mcdTestHash(amount)}
	if f.changedPrice && f.quotes > 1 {
		quote := fields["quote"].(map[string]any)
		quote["amount_cents"], quote["discount_cents"] = 1400, 400
		for _, item := range items {
			item["amount_cents"] = 1400
		}
		fields["purchase_digest"] = mcdTestHash(1400)
	}
	if f.quoteTTL > 0 && f.quotes == max(f.ttlQuote, 1) {
		fields["expires_at_ms"] = time.Now().Add(f.quoteTTL).UnixMilli()
	}
	fields["envelope_digest"] = mcdTestHash(fields)
	return mcdTestJSON(fields), nil
}
func (f *mcdFlowFixture) Prepare(_ context.Context, raw json.RawMessage) (map[string]any, error) {
	_ = json.Unmarshal(raw, &f.quote)
	return map[string]any{"state": "prepared", "purchase_digest": f.quote.PurchaseDigest}, nil
}
func (f *mcdFlowFixture) Abort(_ context.Context, _ mcdintegration.QuoteScope, id string, epoch int64, reason string) (map[string]any, error) {
	f.aborts++
	return map[string]any{"state": "aborted_before_write", "purchase_id": id, "handoff_epoch": epoch, "reason": reason, "non_dispatch_proven": true}, nil
}
func (f *mcdFlowFixture) Commit(_ context.Context, _ mcdintegration.QuoteScope, h mcdintegration.Handoff, _ string, _ map[string]any) (map[string]any, error) {
	f.commits++
	if f.unknown {
		return nil, errors.New("lost reply")
	}
	if f.cancelled {
		return map[string]any{"state": "CANCELLED", "purchase_id": h.PurchaseID, "handoff_epoch": h.Epoch, "terminal_verified": true}, nil
	}
	if f.noPickupCode {
		return map[string]any{"state": "PAID", "purchase_id": h.PurchaseID, "handoff_epoch": h.Epoch, "terminal_verified": true, "order_no": "order-1"}, nil
	}
	return map[string]any{"state": "PAID", "purchase_id": h.PurchaseID, "handoff_epoch": h.Epoch, "terminal_verified": true, "pickup_code": "A123", "order_no": "order-1"}, nil
}
func (f *mcdFlowFixture) Status(context.Context, mcdintegration.QuoteScope, string) (map[string]any, error) {
	f.statuses++
	if f.unknown {
		return map[string]any{"state": "UNKNOWN", "purchase_id": f.quote.PurchaseID, "handoff_epoch": f.quote.HandoffEpoch}, nil
	}
	if f.cancelled {
		return map[string]any{"state": "CANCELLED", "purchase_id": f.quote.PurchaseID, "handoff_epoch": f.quote.HandoffEpoch, "terminal_verified": true}, nil
	}
	if f.noPickupCode {
		return map[string]any{"state": "PAID", "purchase_id": f.quote.PurchaseID, "handoff_epoch": f.quote.HandoffEpoch, "terminal_verified": true, "order_no": "order-1"}, nil
	}
	return map[string]any{"state": "PAID", "purchase_id": f.quote.PurchaseID, "handoff_epoch": f.quote.HandoffEpoch, "terminal_verified": true, "pickup_code": "A123", "order_no": "order-1"}, nil
}
func (f *mcdFlowFixture) Orders(context.Context, mcdintegration.QuoteScope) (map[string]any, error) {
	if f.offline {
		return nil, errors.New("offline")
	}
	limit := f.policyMaxCents
	if limit == 0 {
		limit = 2000
	}
	return map[string]any{"orders": []any{}, "policy": map[string]any{"max_order_cents": limit}}, nil
}
func newMCDFlowRuntime(t *testing.T, f *mcdFlowFixture, enabled bool) (Runtime, *store.MemoryStore, app.Session) {
	t.Helper()
	// Keep the production three-query schedule, only shortened for the suite.
	delays := mcdSettleDelays
	mcdSettleDelays = []time.Duration{0, time.Millisecond, 2 * time.Millisecond}
	t.Cleanup(func() { mcdSettleDelays = delays })
	f.pickup = time.Now().Add(2 * time.Hour).In(time.FixedZone("CST", 8*3600)).Format(time.RFC3339)
	if f.cart == nil {
		f.cart = []map[string]any{}
	}
	cfg := agentTestConfig()
	cfg.MCDSelection.Enabled = enabled
	cfg.MCDSelection.CartEnabled = enabled
	cfg.MCDSelection.PurchaseEnabled = enabled
	cfg.MCDSelection.QualifiedEatTypes = []string{"locker-in"}
	st := store.NewMemoryStore()
	root := t.TempDir()
	session := storetest.MustCreateSessionWithScope(t, st, "MCD workflow", "owner", root, "web", false)
	hub := toolhub.New(cfg, st)
	t.Cleanup(func() { _ = hub.Close() })
	raw, err := os.ReadFile("../../../../tools/ezmcd-adapter/tests/session-envelope-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	_ = json.Unmarshal(raw, &fields)
	fields["issued_at_ms"] = time.Now().UnixMilli() - 1000
	fields["session_not_after_ms"] = time.Now().Add(15 * time.Minute).UnixMilli()
	fields["max_requests"] = 128
	fields["operations"] = []string{"stores_favorites", "cities_list", "stores_search", "pickup_slots", "pickup_methods", "menu_list", "product_detail", "coupons_list", "coupon_products", "cart_snapshot", "cart_add_configured", "cart_clear", "checkout_quote_pickup", "checkout_quote"}
	sort.Strings(fields["operations"].([]string))
	delete(fields, "envelope_digest")
	fields["envelope_digest"] = mcdTestHash(fields)
	raw = mcdTestJSON(fields)
	e, err := mcdintegration.DecodeEnvelope(raw)
	if err != nil {
		t.Fatal(err)
	}
	m, err := mcdintegration.NewManager(credential.New(store.NewMemoryStore(), credential.Options{Key: strings.Repeat("42", 32)}), f, e.Identity.DeploymentID)
	if err != nil {
		t.Fatal(err)
	}
	p := mcdintegration.Principal{DeploymentID: e.Identity.DeploymentID, OwnerID: e.Identity.OwnerID, ClientID: e.Identity.ClientID, InstallationID: e.Identity.InstallationID}
	if _, err = m.Provision(t.Context(), p, raw); err != nil {
		t.Fatal(err)
	}
	selection, err := mcddining.NewSelection(t.Context(), m, p, "workflow-execution")
	if err != nil {
		t.Fatal(err)
	}
	selection.WithCart(t.TempDir(), f).WithPurchase(enabled).WithStatus(enabled)
	scoped, err := hub.WithExecutionScope(st, toolhub.ExecutionResources{OwnerID: session.OwnerID, WorkspaceRoot: root, Artifacts: artifact.NewStore(cfg.Storage), MCDSelection: selection})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.models++
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		schema := mcdMap(mcdMap(body["response_format"])["json_schema"])
		var content string
		if schema["name"] == "mcd_order_intent_v1" {
			content = `{"items":[{"text":"麦辣鸡腿堡套餐","quantity":null,"notes":""}],"store_text":"","pickup_method_text":"","pickup_time_text":"","max_amount_text":""}`
			if f.maxAmountText != "" || f.items > 1 {
				var intent map[string]any
				_ = json.Unmarshal([]byte(content), &intent)
				intent["max_amount_text"] = f.maxAmountText
				items := []any{}
				for i := 0; i < max(f.items, 1); i++ {
					items = append(items, map[string]any{"text": "麦辣鸡腿堡套餐", "quantity": nil, "notes": ""})
				}
				intent["items"] = items
				content = string(mcdTestJSON(intent))
			}
		} else if schema["name"] == "mcd_order_rank_v1" {
			content = `{"ranked_ids":["menu-1"],"unique_match":true}`
		} else {
			t.Errorf("unexpected model schema %v", schema)
			content = "{}"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": content}}}})
	}))
	t.Cleanup(server.Close)
	runtime := NewRuntime(st, scoped, policy.New(cfg), modelrouter.New(cfg), nil)
	cfg.Model.Mock = false
	cfg.Model.Fast.BaseURL = server.URL
	cfg.Model.Fast.Model = "test-fast"
	runtime.models = modelrouter.New(cfg)
	return runtime, st, session
}
func dispatchMCDTest(t *testing.T, r Runtime, st *store.MemoryStore, session app.Session, source app.MessageSourceKind) (app.AgentRun, workflowExecutionResult) {
	t.Helper()
	text := "帮我点一份麦辣鸡腿堡套餐"
	now := time.Now().UTC()
	msg := app.Message{ID: app.NewID("msg"), SessionID: session.ID, Role: "user", Content: text, CreatedAt: now}
	_, _ = st.AddMessage(t.Context(), msg)
	run := app.AgentRun{ID: app.NewID("run"), SessionID: session.ID, StartedAt: now, State: "executing", MessageContext: &app.MessageRunContext{OwnerID: session.OwnerID, Authorization: app.MessageAuthorization{PrincipalID: session.OwnerID}, Source: app.MessageSourceContext{Kind: source}}}
	route := app.RouteDecision{SchemaVersion: 1, Status: app.RouteMatched, CatalogRevision: r.capabilities.Revision(), CapabilityPath: []app.CapabilityID{"dining", app.CapabilityMCDOrder}, Slots: app.RouteSlots{Operation: app.RouteOperationDraft, Query: text}}
	d, err := r.dispatchMatchedWorkflow(t.Context(), run, route, app.ReturnRoute{Mode: app.ReturnToSource}, msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := r.runWorkflow(t.Context(), session.ID, d.Run, text, d.Profile, d.Context, d.Tools)
	stored, _, _ := st.GetRun(t.Context(), run.ID)
	if len(result.Approvals) > 0 {
		stored.State = "approval_pending"
		stored, _ = st.SaveRun(t.Context(), stored)
	}
	return stored, result
}
func answerMCD(t *testing.T, r Runtime, st *store.MemoryStore, run app.AgentRun, pending app.Approval, values app.ApprovalInputValues, reject bool) Result {
	t.Helper()
	status := app.ApprovalStatusApproved
	if reject {
		status = app.ApprovalStatusRejected
	}
	var approved app.Approval
	var err error
	if pending.InputForm != nil && !reject {
		approved, err = st.ResolveApprovalWithInputs(t.Context(), pending.ID, status, "", app.ApprovalInputSubmission{Digest: pending.InputFormDigest, SchemaVersion: 1, Inputs: values})
	} else {
		approved, err = st.ResolveApproval(t.Context(), pending.ID, status, "")
	}
	if err != nil {
		t.Fatal(err)
	}
	if !reject {
		if _, err = r.ExecuteApprovedToolCall(t.Context(), approved); err != nil {
			t.Fatal(err)
		}
	}
	result, resumed, err := r.ResumeRunAfterApproval(t.Context(), run.SessionID, run.ID)
	if err != nil || !resumed {
		t.Fatalf("resume=%v err=%v", resumed, err)
	}
	for _, call := range testListToolCalls(st, run.SessionID) {
		if call.Error != "" {
			t.Logf("%s: %s", call.Tool, call.Error)
		}
	}
	return result
}
func mcdPending(t *testing.T, st *store.MemoryStore, run string) app.Approval {
	t.Helper()
	rows, _ := st.ListApprovals(t.Context(), app.ApprovalStatusPending)
	for _, a := range rows {
		if a.RunID == run {
			return a
		}
	}
	t.Fatal("pending approval missing")
	return app.Approval{}
}
func TestMCDOrderEntireFlowKeepsOriginalExecutionAndNoCartApprovals(t *testing.T) {
	f := &mcdFlowFixture{}
	r, st, session := newMCDFlowRuntime(t, f, true)
	run, result := dispatchMCDTest(t, r, st, session, app.MessageSourceWeb)
	if len(result.Approvals) != 1 {
		t.Fatalf("first form: %+v state=%+v calls=%v", result, run.Workflow, f.calls)
	}
	first := result.Approvals[0]
	if first.InputForm == nil || first.InputForm.Entries[0].Fields[0].Name != "store" {
		t.Fatalf("not store confirmation %+v", first)
	}
	result2 := answerMCD(t, r, st, run, first, app.ApprovalInputValues{"context": {"store": "3330613"}}, false)
	config := mcdPending(t, st, run.ID)
	if config.Tool != "notify.ask_approval" {
		t.Fatal("missing meal configuration")
	}
	answerMCD(t, r, st, result2.Run, config, app.ApprovalInputValues{"item_1": {"group_1_0": "2001"}}, false)
	payment := mcdPending(t, st, run.ID)
	if payment.Tool != "mcd.purchase.commit" || !payment.ArgumentsImmutable || mcdInt(payment.Arguments["amount_cents"]) != 1500 {
		t.Fatalf("payment %+v calls=%v", payment, f.calls)
	}
	for _, text := range []string{"测试店", "取餐柜", "套餐优惠", "3.00", "15.00", "ARCHCARD", "× 1"} {
		if !strings.Contains(payment.Summary, text) {
			t.Fatalf("payment summary omits %q: %s", text, payment.Summary)
		}
	}
	final := answerMCD(t, r, st, run, payment, nil, false)
	if final.Run.ID != run.ID || final.Message.Content != "已下单，取餐码：A123" || f.commits != 1 || f.quotes != 1 || f.models != 2 || f.statuses < 1 {
		t.Fatalf("final=%+v fixture=%+v", final, f)
	}
	approvals, _ := st.ListApprovals(t.Context(), "")
	if len(approvals) != 3 {
		t.Fatalf("unexpected approvals %v", approvals)
	}
	for _, call := range testListToolCalls(st, session.ID) {
		if strings.Contains(call.ObservationSummary, "A123") {
			t.Fatal("pickup code leaked into observation")
		}
	}
}
func TestMCDOrderUnavailableAndNonFormNeverCallModel(t *testing.T) {
	for _, tc := range []struct {
		enabled bool
		source  app.MessageSourceKind
		want    string
	}{{false, app.MessageSourceWeb, mcdUnavailable}, {true, app.MessageSourceKind("wechat"), "请在 SparkX 客户端点餐"}} {
		t.Run(tc.want, func(t *testing.T) {
			f := &mcdFlowFixture{}
			r, st, session := newMCDFlowRuntime(t, f, tc.enabled)
			run, result := dispatchMCDTest(t, r, st, session, tc.source)
			if result.FinalAnswer != tc.want || f.models != 0 || f.commits != 0 || run.Workflow.Status != app.WorkflowStatusSucceeded {
				t.Fatalf("result=%+v workflow=%+v calls=%v", result, run.Workflow, f.calls)
			}
		})
	}
}
