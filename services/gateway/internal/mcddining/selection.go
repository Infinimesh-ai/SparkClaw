// Package mcddining owns bounded dining Workflow projections. It never accepts
// credentials, an account identity, an endpoint or purchase authority from a model.
package mcddining

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/appstream"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/mcdintegration"
)

type Input struct {
	Operation    string `json:"operation"`
	StoreCode    string `json:"store_code,omitempty"`
	PickupAt     string `json:"pickup_at,omitempty"`
	EatType      string `json:"eat_type,omitempty"`
	MaxAmountCNY string `json:"max_amount_cny,omitempty"`
	Keyword      string `json:"keyword,omitempty"`
	CityCode     string `json:"city_code,omitempty"`
	Latitude     string `json:"latitude,omitempty"`
	Longitude    string `json:"longitude,omitempty"`
	ProductCode  string `json:"product_code,omitempty"`
	OfferID      string `json:"offer_id,omitempty"`
	CouponRef    string `json:"coupon_ref,omitempty"`
	VariantOf    string `json:"variant_of,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Page         int    `json:"page,omitempty"`
}
type Selection struct {
	manager   *mcdintegration.Manager
	principal mcdintegration.Principal
	execution mcdintegration.Execution
	cart      *cartResources
}

func NewSelection(ctx context.Context, m *mcdintegration.Manager, p mcdintegration.Principal, id string) (*Selection, error) {
	x, err := m.Selection(ctx, p, id)
	if err != nil {
		return nil, err
	}
	return &Selection{manager: m, principal: p, execution: x}, nil
}
func NewRecoverySelection(ctx context.Context, m *mcdintegration.Manager, p mcdintegration.Principal, id string) (*Selection, error) {
	x, err := m.Recovery(ctx, p, id)
	if err != nil {
		return nil, err
	}
	return &Selection{manager: m, principal: p, execution: x}, nil
}
func (s *Selection) Read(ctx context.Context, raw []byte) (map[string]any, error) {
	var input Input
	if len(raw) > 8192 || appstream.DecodeStrict(raw, &input) != nil {
		return nil, errors.New("mcd_invalid_selection")
	}
	args := map[string]any{}
	request := map[string]any{"account": s.execution.Identity.AccountRef, "store_code": input.StoreCode, "pickup_at": input.PickupAt, "eat_type": input.EatType, "max_amount_cny": input.MaxAmountCNY}
	switch input.Operation {
	case "membership_assets", "stores_favorites":
	case "cities_list":
		args["keyword"] = input.Keyword
	case "store_get", "pickup_slots":
		args["store_code"] = input.StoreCode
	case "stores_search":
		args["account"] = s.execution.Identity.AccountRef
		args["keyword"] = input.Keyword
		if input.CityCode != "" {
			args["city"] = input.CityCode
		}
		if input.Latitude != "" {
			args["latitude"] = input.Latitude
		}
		if input.Longitude != "" {
			args["longitude"] = input.Longitude
		}
		if input.Limit != 0 {
			args["limit"] = input.Limit
		}
		if input.Page != 0 {
			args["page"] = input.Page
		}
	case "pickup_methods", "cart_get":
		args["request"] = request
	case "menu_list":
		args["request"] = request
		args["keyword"] = input.Keyword
		args["offset"] = input.Offset
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		args["limit"] = limit
	case "product_detail":
		args["request"] = request
		args["product_code"] = input.ProductCode
		args["offer_id"] = input.OfferID
		args["coupon_ref"] = input.CouponRef
		args["variant_of"] = input.VariantOf
	case "coupons_list":
		args["request"] = request
		args["keyword"] = input.Keyword
	case "coupon_products":
		args["request"] = request
		args["coupon_ref"] = input.CouponRef
	default:
		return nil, errors.New("mcd_read_operation_required")
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	x := s.execution
	x.MaxRequests = readRequestBudget(input, x.MaxRequests)
	result, err := s.manager.Read(ctx, s.principal, x, input.Operation, encoded)
	if err != nil {
		return nil, errors.New("mcd_selection_unavailable")
	}
	defer clear(result)
	var response struct {
		Version  int    `json:"schema_version"`
		Encoding string `json:"result_encoding"`
		JSON     string `json:"result_json"`
	}
	if appstream.DecodeStrict(result, &response) != nil || response.Version != 1 || response.Encoding != "bounded-json-v1" || len(response.JSON) > 64<<10 {
		return nil, errors.New("mcd_invalid_result")
	}
	var value map[string]any
	if json.Unmarshal([]byte(response.JSON), &value) != nil || value == nil {
		return nil, errors.New("mcd_invalid_result")
	}
	return map[string]any{"provider": "mcd-cn", "operation": input.Operation, "result": value, "checkout_enabled": false}, nil
}
