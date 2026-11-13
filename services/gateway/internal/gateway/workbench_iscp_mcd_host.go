package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/iscpworkbench"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

type mcdHostRoute struct {
	routes    *workbenchBrowserRoutes
	principal mcdintegration.Principal
}

func (h mcdHostRoute) peer() (iscpworkbench.PeerSession, error) {
	h.routes.mu.Lock()
	routes := make([]workbenchPeerRoute, 0, len(h.routes.routes))
	for _, r := range h.routes.routes {
		routes = append(routes, r)
	}
	h.routes.mu.Unlock()
	for _, route := range routes {
		if peer, err := route(h.principal.OwnerID, h.principal.ClientID, h.principal.InstallationID); err == nil {
			return peer, nil
		}
	}
	return iscpworkbench.PeerSession{}, iscpworkbench.ErrPeerUnavailable
}
func (h mcdHostRoute) Prepare(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	if len(raw) > 256<<10 {
		return nil, errors.New("mcd_quote_exceeds_capacity")
	}
	var envelope mcdintegration.QuoteEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.ExpiresAtMS <= time.Now().UnixMilli() {
		return nil, errors.New("mcd_quote_expired")
	}
	peer, err := h.peer()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	key := "prepare-" + envelope.EnvelopeDigest
	count := (len(raw) + 8191) / 8192
	var receipt map[string]any
	for index := 0; index < count; index++ {
		body, _ := json.Marshal(map[string]any{"scope": envelope.Scope, "operation_key": key, "transfer": map[string]any{"digest": hex.EncodeToString(sum[:]), "size": len(raw), "count": count, "index": index, "expires_at_ms": envelope.ExpiresAtMS, "data": base64.StdEncoding.EncodeToString(raw[index*8192 : min(len(raw), (index+1)*8192)])}})
		response, err := peer.Command(ctx, iscpworkbench.OperationMCDCheckoutPrepare, body)
		clear(body)
		if err != nil || response.Status != 200 || len(response.Body) > 16<<10 || json.Unmarshal(response.Body, &receipt) != nil {
			return nil, errors.New("mcd_handoff_requires_original_status")
		}
		if index < count-1 && (receipt["state"] != "chunk_received" || receipt["operation_key"] != key || receipt["index"] != float64(index)) {
			return nil, errors.New("mcd_chunk_receipt_mismatch")
		}
	}
	if receipt["purchase_id"] != envelope.PurchaseID || receipt["handoff_epoch"] != float64(envelope.HandoffEpoch) || receipt["purchase_digest"] != envelope.PurchaseDigest || receipt["expires_at_ms"] != float64(envelope.ExpiresAtMS) {
		return nil, errors.New("mcd_prepare_receipt_mismatch")
	}
	return receipt, nil
}
func (h mcdHostRoute) Abort(ctx context.Context, scope mcdintegration.QuoteScope, purchase string, epoch int64, reason string) (map[string]any, error) {
	peer, err := h.peer()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(purchase + ":" + reason))
	key := "abort-" + hex.EncodeToString(sum[:])
	// Epoch is part of the original key; a refresh's abort is a different action.
	keySum := sha256.Sum256([]byte(key + ":" + jsonNumber(epoch)))
	key = "abort-" + hex.EncodeToString(keySum[:])
	body, _ := json.Marshal(map[string]any{"scope": scope, "operation_key": key, "purchase_id": purchase, "handoff_epoch": epoch, "reason": reason})
	response, err := peer.Command(ctx, iscpworkbench.OperationMCDCheckoutAbort, body)
	clear(body)
	if err != nil || response.Status != 200 || len(response.Body) > 8192 {
		return nil, errors.New("mcd_abort_requires_original_status")
	}
	var r struct {
		State      string `json:"state"`
		PurchaseID string `json:"purchase_id"`
		Epoch      int64  `json:"handoff_epoch"`
		Reason     string `json:"reason"`
		Canceled   bool   `json:"official_order_canceled"`
		Refund     bool   `json:"refund_attempted"`
	}
	if domainDecode(response.Body, &r) != nil || r.Canceled || r.Refund {
		return nil, errors.New("mcd_abort_invalid_receipt")
	}
	return map[string]any{"state": r.State, "purchase_id": r.PurchaseID, "handoff_epoch": r.Epoch, "reason": r.Reason, "official_order_canceled": false, "refund_attempted": false}, nil
}
func jsonNumber(n int64) string { raw, _ := json.Marshal(n); return string(raw) }

func (h mcdHostRoute) purchaseCommand(ctx context.Context, op string, body any) (map[string]any, error) {
	peer, err := h.peer()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	response, err := peer.Command(ctx, op, raw)
	if err != nil || response.Status != 200 || len(response.Body) > 48<<10 {
		return nil, errors.New("mcd_purchase_requires_original_status")
	}
	var receipt map[string]any
	if json.Unmarshal(response.Body, &receipt) != nil {
		return nil, errors.New("mcd_purchase_receipt_invalid")
	}
	if epoch, ok := receipt["handoff_epoch"].(float64); ok {
		if epoch < 1 || epoch > 1<<53-1 || epoch != float64(int64(epoch)) {
			return nil, errors.New("mcd_purchase_receipt_invalid")
		}
		receipt["handoff_epoch"] = int64(epoch)
	}
	return receipt, nil
}
func (h mcdHostRoute) Commit(ctx context.Context, scope mcdintegration.QuoteScope, q mcdintegration.Handoff, approval string, review map[string]any) (map[string]any, error) {
	return h.purchaseCommand(ctx, iscpworkbench.OperationMCDCheckoutCommit, map[string]any{"scope": scope, "operation_key": mcdintegration.CommitKey(q), "purchase_id": q.PurchaseID, "handoff_epoch": q.Epoch, "approval_id": approval, "review": review})
}
func (h mcdHostRoute) Status(ctx context.Context, scope mcdintegration.QuoteScope, key string) (map[string]any, error) {
	return h.purchaseCommand(ctx, iscpworkbench.OperationMCDOperationStatus, map[string]any{"scope": scope, "operation_key": key})
}
func (h mcdHostRoute) Orders(ctx context.Context, scope mcdintegration.QuoteScope) (map[string]any, error) {
	receipt, err := h.purchaseCommand(ctx, iscpworkbench.OperationMCDOrdersQuery, map[string]any{"scope": scope, "limit": 20, "offset": 0})
	if err != nil {
		return nil, err
	}
	encoded, ok := receipt["result_json"].(string)
	if !ok || len(encoded) > 40<<10 {
		return nil, errors.New("mcd_orders_receipt_invalid")
	}
	var result map[string]any
	if json.Unmarshal([]byte(encoded), &result) != nil {
		return nil, errors.New("mcd_orders_receipt_invalid")
	}
	return result, nil
}
