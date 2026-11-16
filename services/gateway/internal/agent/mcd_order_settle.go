package agent

import (
	"errors"
	"strings"
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

func (s *mcdOrderStage) guard() error {
	if s.run.MessageContext == nil || s.run.MessageContext.Source.Kind != app.MessageSourceWeb || s.runtime.tools.ExecutionTextOnly() {
		s.state.StopReply = "请在 SparkX 客户端点餐"
		return nil
	}
	if s.runtime.tools.MCDStatusAvailable() {
		status, err := s.tool("mcd.purchase.status", nil)
		if err != nil {
			s.state.StopReply = "上一单付款结果尚未确认，暂不能再次下单"
			if err.Error() == "mcd_status_unavailable_without_pending_purchase" {
				s.state.StopReply = mcdUnavailable
			}
			return nil
		}
		state := mcdText(status["state"])
		if state != "" {
			if reply, verified := mcdTerminalReply(status); verified {
				s.state.PriorReply = "上一单已确认：" + reply
			} else if status["non_dispatch_proven"] != true {
				s.state.StopReply = "上一单付款结果尚未确认，暂不能再次下单"
				return nil
			}
		}
		policy := mcdMap(status["policy"])
		if len(policy) == 0 {
			snapshot, err := s.tool("mcd.purchase.status", map[string]any{"operation": "policy"})
			if err != nil {
				s.state.StopReply = mcdUnavailable
				return nil
			}
			policy = mcdMap(snapshot["policy"])
		}
		s.state.MaxOrderCents = mcdInt(policy["max_order_cents"])
	}
	if !s.runtime.tools.MCDOrderingAvailable() || s.state.MaxOrderCents <= 0 {
		s.state.StopReply = mcdUnavailable
	}
	return nil
}

// mcdMaxPaymentApprovals is the original approval plus one new approval for
// each of the two allowed changed expiry refreshes.
const mcdMaxPaymentApprovals = 3

// mcdSettleDelays spaces the bounded receipt queries: at most three, within
// the remaining execution deadline. Tests shorten it.
var mcdSettleDelays = []time.Duration{0, 3 * time.Second, 8 * time.Second}

func (s *mcdOrderStage) lastPaymentCall() (*app.ToolCall, int, error) {
	calls, err := s.runtime.store.ListToolCalls(s.ctx, s.run.SessionID)
	if err != nil {
		return nil, 0, err
	}
	var last *app.ToolCall
	count := 0
	for i := range calls {
		c := &calls[i]
		if c.RunID == s.run.ID && c.WorkflowNodeID == "pay" && c.Tool == "mcd.purchase.commit" {
			count++
			if last == nil || c.StartedAt.After(last.StartedAt) {
				last = c
			}
		}
	}
	return last, count, nil
}
func (s *mcdOrderStage) pay() error {
	if s.state.PaymentQueued {
		last, count, err := s.lastPaymentCall()
		if err != nil {
			return err
		}
		if last == nil {
			return errors.New("mcd_payment_call_missing")
		}
		if last.ApprovalID != "" {
			approval, ok, err := s.runtime.store.GetApproval(s.ctx, last.ApprovalID)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("mcd_payment_approval_missing")
			}
			if approval.Status == app.ApprovalStatusRejected {
				s.state.StopReply = mcdCancelled
				return nil
			}
			if approval.Status == app.ApprovalStatusPending {
				return errors.New("mcd_payment_pending")
			}
		}
		if last.Error == "mcd_changed_quote_requires_new_approval" && count < mcdMaxPaymentApprovals {
			// Refresh has aborted the old epoch and changed the immutable review. Only
			// a newly displayed approval can authorize this new content.
			s.state.PaymentQueued = false
		} else {
			s.state.PaymentFinished = true
			return nil
		}
	}
	args, err := s.runtime.tools.MCDPurchaseArguments()
	if err != nil {
		return err
	}
	quote, err := s.runtime.tools.MCDPreparedQuote()
	if err != nil || !mcdQuoteMatchesPlan(s.state, quote) || !mcdCouponQuoteValid(s.state.Lines, quote, s.state.LimitCents) {
		return errors.New("mcd_payment_quote_changed")
	}
	s.state.Quote = quote
	if args["payment_method"] != "ARCHCARD" || mcdInt(args["amount_cents"]) > s.state.LimitCents {
		return errors.New("mcd_payment_limit_changed")
	}
	s.state.PaymentQueued = true
	_, err = s.tool("mcd.purchase.commit", args)
	return err
}
func (s *mcdOrderStage) settle() error {
	if !s.state.PaymentQueued || s.state.StopReply == mcdCancelled {
		if s.state.QuoteCount > 0 {
			result, err := s.tool("mcd.quote.prepare", map[string]any{"operation": "checkout_abort"})
			if err != nil || result["non_dispatch_proven"] != true {
				s.state.StopReply = "订单准备状态尚未确认，暂不能再次下单"
				return nil
			}
		}
		if s.state.StopReply == "" {
			s.state.StopReply = mcdCancelled
		}
		return nil
	}
	if reply, settled, err := s.settleUndispatchedPayment(); err != nil || settled {
		s.state.StopReply = reply
		return err
	}
	var paid map[string]any
	for i, delay := range mcdSettleDelays {
		if i > 0 {
			if deadline, ok := s.ctx.Deadline(); ok && time.Until(deadline) <= delay {
				break
			}
			timer := time.NewTimer(delay)
			select {
			case <-s.ctx.Done():
				timer.Stop()
				s.state.StopReply = mcdUnknown
				return nil
			case <-timer.C:
			}
		}
		status, err := s.tool("mcd.purchase.status", nil)
		if err != nil {
			continue
		}
		if status["non_dispatch_proven"] == true {
			s.state.StopReply = mcdUserActionReason(mcdText(status["stop_reason"]))
			return nil
		}
		if reply, verified := mcdTerminalReply(status); verified {
			if status["state"] == "CANCELLED" || mcdText(status["pickup_code"]) != "" {
				s.state.StopReply = reply
				return nil
			}
			paid = status
		}
	}
	if paid != nil {
		s.state.StopReply, _ = mcdTerminalReply(paid)
	} else {
		s.state.StopReply = mcdUnknown
	}
	return nil
}

// settleUndispatchedPayment handles a commit call that failed before it could
// dispatch: an expired quote whose refresh was not possible, changed content
// after the last allowed approval, or an authority mismatch. The selection
// refuses to abort once a commit started, and an abort succeeds only after
// both ledgers record the preparation as aborted before any write. Any other
// outcome falls through to bounded status reconciliation.
func (s *mcdOrderStage) settleUndispatchedPayment() (string, bool, error) {
	last, _, err := s.lastPaymentCall()
	if err != nil || last == nil || last.Error == "" || toolCallCompleted(*last) {
		return "", false, err
	}
	result, err := s.tool("mcd.quote.prepare", map[string]any{"operation": "checkout_abort"})
	if err != nil || result["non_dispatch_proven"] != true {
		return "", false, nil
	}
	switch last.Error {
	case "mcd_quote_refresh_limit_or_deadline", "mcd_pickup_refresh_requires_recovery", "mcd_refresh_snapshot_missing", "mcd_refresh_receipt_missing", "mcd_pickup_refresh_invalid":
		return "未下单：报价已过期，请重新点餐", true, nil
	case "mcd_changed_quote_requires_new_approval":
		return "未下单：报价已变化，请重新点餐", true, nil
	}
	return "未下单：付款未发起，请重新点餐", true, nil
}
func mcdTerminalReply(status map[string]any) (string, bool) {
	if status["terminal_verified"] != true {
		return "", false
	}
	if status["state"] == "CANCELLED" {
		return "订单已取消，未扣款", true
	}
	if status["state"] != "PAID" {
		return "", false
	}
	if code := mcdSafeReceiptText(status["pickup_code"]); code != "" {
		return "已下单，取餐码：" + code, true
	}
	if order := mcdSafeReceiptText(status["order_no"]); order != "" {
		return "已下单，订单号：" + order + "，取餐码生成后可在麦当劳 App 查看", true
	}
	// A verified PAID receipt is final even before the official order number or
	// pickup code appears; never report a paid order as unconfirmed.
	return "已下单，取餐码生成后可在麦当劳 App 查看", true
}
func mcdSafeReceiptText(value any) string {
	text := mcdText(value)
	if len(text) > 128 || strings.ContainsAny(text, "\r\n\x00") {
		return ""
	}
	return text
}
func mcdUserActionReason(code string) string {
	switch code {
	case "wallet_insufficient_balance", "insufficient_wallet_balance", "insufficient_balance", "wallet_balance_insufficient":
		return "未下单：麦钱包余额不足，请充值后重试"
	case "order_outside_authorized_policy", "policy_order_amount_exceeded", "daily_budget_exceeded", "daily_order_limit_exceeded", "checkout_disabled_by_policy":
		return "未下单：超出账号的下单限额，请检查麦当劳账号授权"
	case "account_revoked_reconnect_locally", "account_session_expired", "session_expired":
		return "未下单：请在 SparkX 重新连接麦当劳账号"
	default:
		return "未下单：请在麦当劳 App 检查账号状态后重试"
	}
}
