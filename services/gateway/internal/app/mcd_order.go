package app

// MCDOrderIntent contains owner language only. Official identifiers are bound
// later to frozen, execution-scoped provider evidence by the ordering runtime.
type MCDOrderIntent struct {
	Items            []MCDOrderIntentItem `json:"items"`
	StoreText        string               `json:"store_text"`
	PickupMethodText string               `json:"pickup_method_text"`
	PickupTimeText   string               `json:"pickup_time_text"`
	MaxAmountText    string               `json:"max_amount_text"`
}
type MCDOrderIntentItem struct {
	Text     string `json:"text"`
	Quantity *int   `json:"quantity"`
	Notes    string `json:"notes"`
}
type MCDOrderLine struct {
	Text             string         `json:"text"`
	Notes            string         `json:"notes"`
	Quantity         int            `json:"quantity"`
	Product          map[string]any `json:"product,omitempty"`
	Detail           map[string]any `json:"detail,omitempty"`
	Selection        map[string]any `json:"selection,omitempty"`
	RegularSelection map[string]any `json:"regular_selection,omitempty"`
	MenuCents        int64          `json:"menu_cents"`
	CouponName       string         `json:"coupon_name,omitempty"`
	CouponCents      int64          `json:"coupon_cents,omitempty"`
}

// MCDOrderState lives in the existing transient execution store. It contains
// no credentials and is never projected into a model prompt as a whole.
type MCDOrderState struct {
	CartItems       []map[string]any          `json:"cart_items,omitempty"`
	Intent          *MCDOrderIntent           `json:"intent,omitempty"`
	Phase           string                    `json:"phase,omitempty"`
	MaxOrderCents   int64                     `json:"max_order_cents"`
	LimitCents      int64                     `json:"limit_cents"`
	Searches        int                       `json:"searches"`
	Store           map[string]any            `json:"store,omitempty"`
	Stores          []map[string]any          `json:"stores,omitempty"`
	Methods         []map[string]any          `json:"methods,omitempty"`
	Slots           []string                  `json:"slots,omitempty"`
	PickupAt        string                    `json:"pickup_at,omitempty"`
	PickupDefaulted bool                      `json:"pickup_defaulted,omitempty"`
	EatType         string                    `json:"eat_type,omitempty"`
	Lines           []MCDOrderLine            `json:"lines,omitempty"`
	PendingApproval string                    `json:"pending_approval,omitempty"`
	PendingKind     string                    `json:"pending_kind,omitempty"`
	Candidates      map[string]map[string]any `json:"candidates,omitempty"`
	Answers         ApprovalInputValues       `json:"answers,omitempty"`
	QuoteCount      int                       `json:"quote_count"`
	Quote           map[string]any            `json:"quote,omitempty"`
	SourceQuoteRef  string                    `json:"source_quote_ref,omitempty"`
	CartReady       bool                      `json:"cart_ready"`
	PaymentQueued   bool                      `json:"payment_queued"`
	PaymentFinished bool                      `json:"payment_finished"`
	PriorReply      string                    `json:"prior_reply,omitempty"`
	StopReply       string                    `json:"stop_reply,omitempty"`
}
