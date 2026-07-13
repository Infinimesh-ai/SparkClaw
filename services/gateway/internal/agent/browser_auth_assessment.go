package agent

import (
	"strings"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
)

type browserAuthState string

const (
	browserAuthUnknown       browserAuthState = "unknown"
	browserAuthChallenged    browserAuthState = "challenged"
	browserAuthAuthenticated browserAuthState = "authenticated"
)

type browserAuthAssessment struct {
	State      browserAuthState
	Confidence string
	Signals    []string
}

func assessBrowserAuthentication(call app.ToolCall, fields map[string]any) browserAuthAssessment {
	status := strings.ToLower(strings.TrimSpace(stringValue(fields["browser_auth_status"])))
	pageState := strings.ToLower(strings.TrimSpace(stringValue(fields["browser_page_auth_state"])))
	pageConfidence := strings.TrimSpace(stringValue(fields["browser_page_auth_confidence"]))
	pageSignals := stringSliceValue(fields["browser_page_auth_signals"])
	if pageState == "challenged" {
		return browserAuthAssessment{
			State:      browserAuthChallenged,
			Confidence: firstNonEmptyString(pageConfidence, "provider"),
			Signals:    append([]string(nil), pageSignals...),
		}
	}
	if pageState == "authenticated" {
		return browserAuthAssessment{
			State:      browserAuthAuthenticated,
			Confidence: firstNonEmptyString(pageConfidence, "provider"),
			Signals:    append([]string(nil), pageSignals...),
		}
	}
	if status == "handoff_waiting" || status == "handoff_required" || boolValue(fields["auth_challenge_detected"]) || boolValue(fields["login_handoff_required"]) {
		return browserAuthAssessment{
			State:      browserAuthChallenged,
			Confidence: "provider",
			Signals:    []string{"structured_auth_challenge"},
		}
	}
	if status == "profile_verified" || status == "authenticated" || status == "verified" || status == "signed_in" {
		return browserAuthAssessment{
			State:      browserAuthAuthenticated,
			Confidence: "provider",
			Signals:    []string{"structured_" + status},
		}
	}
	if pageState == "unknown" || status == "profile_inconclusive" {
		return browserAuthAssessment{
			State:      browserAuthUnknown,
			Confidence: firstNonEmptyString(pageConfidence, "insufficient"),
			Signals:    append([]string(nil), pageSignals...),
		}
	}

	text := browserLoginObservationText(call, fields)
	challenge := browserLoginObservationLooksLikeAuthGate(text)
	authenticated := browserLoginObservationLooksAuthenticated(text)
	if challenge && authenticated {
		return browserAuthAssessment{
			State:      browserAuthUnknown,
			Confidence: "conflicting",
			Signals:    []string{"visible_auth_challenge", "visible_authenticated_control"},
		}
	}
	if challenge {
		return browserAuthAssessment{
			State:      browserAuthChallenged,
			Confidence: "visible_ui",
			Signals:    []string{"visible_auth_challenge"},
		}
	}
	if authenticated {
		return browserAuthAssessment{
			State:      browserAuthAuthenticated,
			Confidence: "visible_ui",
			Signals:    []string{"visible_authenticated_control"},
		}
	}
	if value, ok := fields["auth_challenge_detected"]; ok && !boolValue(value) && (boolValue(fields["rendered"]) || status != "") {
		return browserAuthAssessment{
			State:      browserAuthAuthenticated,
			Confidence: "provider_negative",
			Signals:    []string{"structured_no_auth_challenge"},
		}
	}
	return browserAuthAssessment{State: browserAuthUnknown, Confidence: "insufficient"}
}

func browserLoginObservationLooksAuthenticated(text string) bool {
	text = strings.ToLower(strings.TrimSpace(text))
	return containsAny(text,
		"sign out", "log out", "signed in as",
		"退出登录", "安全退出", "注销登录",
	)
}

func addBrowserAuthAssessmentFields(fields map[string]any, assessment browserAuthAssessment) {
	fields["auth_evidence_state"] = string(assessment.State)
	fields["auth_evidence_confidence"] = assessment.Confidence
	if len(assessment.Signals) > 0 {
		fields["auth_evidence_signals"] = append([]string(nil), assessment.Signals...)
	}
}
