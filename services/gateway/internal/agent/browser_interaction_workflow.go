package agent

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

const browserInteractionMaxClicks = 3

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
		return browserStageFocusExisting
	case app.OutcomeSignalTargetTabBlank:
		return browserStageNavigateBlank
	default:
		return browserStageOpenNew
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
