package agent

import (
	"fmt"
	"strings"
)

func mcdPaymentSummary(args, quote map[string]any) string {
	items := []string{}
	for _, item := range mcdRows(args["items"]) {
		name := mcdText(item["name"])
		if name == "" {
			name = mcdText(item["product_code"])
		}
		items = append(items, fmt.Sprintf("%s × %d", name, mcdInt(item["quantity"])))
	}
	discounts := strings.Join(mcdStrings(args["discount_names"]), "、")
	if discounts == "" {
		discounts = "无"
	}
	method := mcdText(args["eat_type"])
	switch method {
	case "locker-in":
		method = "取餐柜"
	case "take-in-store":
		method = "到店取餐"
	}
	return fmt.Sprintf("确认麦当劳付款：%s；%s；%s；%s。优惠：%s（共 %s 元）。实付 %s 元，麦钱包 ARCHCARD。", mcdText(args["store_name"]), mcdText(args["pickup_at"]), method, strings.Join(items, "、"), discounts, mcdYuan(mcdInt(quote["discount_cents"])), mcdYuan(mcdInt(args["amount_cents"])))
}
