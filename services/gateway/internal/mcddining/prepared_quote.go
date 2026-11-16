package mcddining

import (
	"encoding/json"
	"errors"
)

// PreparedQuote returns the display fields from the same imported envelope as
// PurchaseArguments. It does not extend the native immutable review protocol.
func (s *Selection) PreparedQuote() (map[string]any, error) {
	if s == nil || s.cart == nil {
		return nil, errors.New("mcd_purchase_unavailable")
	}
	s.cart.mu.Lock()
	defer s.cart.mu.Unlock()
	if s.cart.handoff == nil || s.cart.review == nil || s.cart.preparedQuote == nil {
		return nil, errors.New("mcd_purchase_requires_prepared_quote")
	}
	raw, err := json.Marshal(s.cart.preparedQuote)
	if err != nil {
		return nil, err
	}
	var quote map[string]any
	err = json.Unmarshal(raw, &quote)
	return quote, err
}
