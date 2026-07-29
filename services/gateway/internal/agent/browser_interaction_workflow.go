package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

const browserInteractionMaxClicks = 3

func unsupportedBrowserInteractionIntent(lower string) bool {
	return containsEnglishSemanticTerm(lower,
		"type", "fill", "select", "check", "upload", "download", "login", "sign in", "authenticate", "submit",
		"delete", "remove", "publish", "send", "buy", "purchase", "pay", "payment", "checkout", "place order", "confirm order",
		"log out", "logout", "sign out", "authorize", "grant access",
	) || containsAny(lower,
		"输入", "填写", "选择", "勾选", "上传", "下载", "登录", "认证", "验证码", "提交表单",
		"删除", "移除", "发布", "发送", "购买", "付款", "支付", "下单", "确认订单", "退出登录", "注销", "授权",
	)
}

func explicitCurrentBrowserTab(lower string) bool {
	return containsAny(lower, "current page", "current tab", "selected tab", "当前页面", "当前网页", "当前标签页", "当前tab", "这个页面")
}

func selectBrowserInteractionTab(route app.RouteDecision, refs []app.ResourceRef) (*app.ResourceRef, app.OutcomeSignal, string) {
	selected := []app.ResourceRef{}
	matches := []app.ResourceRef{}
	blank := []app.ResourceRef{}
	for _, ref := range refs {
		if ref.Kind != "browser_tab" {
			continue
		}
		if ref.Attributes["selected"] == "true" {
			selected = append(selected, ref)
		}
		url := normalizeBrowserURL(ref.Attributes["url"])
		if route.Slots.TargetKind == "url" && browserTargetMatchesURL(route.Slots.TargetRef, route.Facts["browser_destination"], url) {
			matches = append(matches, ref)
		}
		if ref.Attributes["selected"] == "true" && (url == "about:blank" || url == "chrome://newtab/" || url == "") {
			blank = append(blank, ref)
		}
	}
	if route.Slots.TargetKind == string(app.TargetKindBrowserCurrentTab) {
		if len(selected) == 1 {
			return &selected[0], app.OutcomeSignalTargetTabExists, ""
		}
		return nil, "", "browser_current_tab_unavailable"
	}
	for _, ref := range matches {
		if ref.Attributes["selected"] == "true" {
			return &ref, app.OutcomeSignalTargetTabExists, ""
		}
	}
	if len(matches) == 1 {
		return &matches[0], app.OutcomeSignalTargetTabExists, ""
	}
	if len(matches) > 1 {
		return nil, "", "browser_tab_ambiguous"
	}
	if len(blank) == 1 {
		return &blank[0], app.OutcomeSignalTargetTabBlank, ""
	}
	return nil, app.OutcomeSignalTargetTabMissing, ""
}

func browserInteractionStageForTabSignal(signal app.OutcomeSignal) string {
	switch signal {
	case app.OutcomeSignalTargetTabExists:
		return "focus_existing"
	case app.OutcomeSignalTargetTabBlank:
		return "navigate_blank"
	default:
		return "open_new"
	}
}

func browserSnapshotOutcomeRepeated(refs []app.ResourceRef) bool {
	for _, ref := range refs {
		if ref.Kind == "browser_snapshot" && ref.Attributes["previous_snapshot_id"] != "" && ref.Attributes["repeated"] == "true" {
			return true
		}
	}
	return false
}
