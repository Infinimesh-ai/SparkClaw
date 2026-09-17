package emailmanagement

import (
	"regexp"
	"strings"
	"unicode"
)

var verificationNoticeMarker = regexp.MustCompile(`(?i)(?:verification[\s_-]*(?:code|number)|security[\s_-]*code|sign[\s_-]*in[\s_-]*code|login[\s_-]*code|authentication[\s_-]*code|one[\s_-]*time[\s_-]*(?:password|code)|otp(?:[\s_-]*(?:password|code))?|passcode|验证码|校验码|动态码|登录码|安全码|认证码|一次性(?:密码|口令))`)
var verificationNoticeToken = regexp.MustCompile(`\b(?:[0-9]{3}[- ][0-9]{3}|[A-Za-z0-9]{4,12})\b`)
var verificationInteractionMarker = regexp.MustCompile(`(?i)(?:\b(?:approve|approval|reply|respond|review|signature|contract|invoice|payment|purchase|order|delivery|schedule|proposal|quotation|document|attachment)\b|请(?:批准|审批|回复|审核|签署|提交|确认(?:合同|订单|报价|交付|付款))|合同|采购|订单|发票|付款|交付|报价|附件)`)

type verificationPatternMatch struct {
	EvidenceRef string
	Stage       string
}

// verificationNoticePattern requires both an explicit verification marker and
// a nearby token containing a digit. Generic order, invoice and phone numbers
// therefore do not bypass semantic classification on their own.
func verificationNoticePattern(input AnalysisInput) (verificationPatternMatch, bool) {
	for _, evidence := range input.Evidence {
		if (strings.HasSuffix(evidence.Ref, ":body") || strings.HasSuffix(evidence.Ref, ":subject")) && verificationInteractionMarker.MatchString(evidence.Text) {
			return verificationPatternMatch{}, false
		}
	}
	for _, evidence := range input.Evidence {
		if !strings.HasSuffix(evidence.Ref, ":body") && !strings.HasSuffix(evidence.Ref, ":subject") {
			continue
		}
		if verificationMarkerAndToken(evidence.Text) {
			stage := "body"
			if strings.HasSuffix(evidence.Ref, ":subject") {
				stage = "subject"
			}
			return verificationPatternMatch{EvidenceRef: evidence.Ref, Stage: stage}, true
		}
	}
	return verificationPatternMatch{}, false
}

func verificationMarkerAndToken(value string) bool {
	markers := verificationNoticeMarker.FindAllStringIndex(value, -1)
	if len(markers) == 0 {
		return false
	}
	for _, token := range verificationNoticeToken.FindAllStringIndex(value, -1) {
		if token[0] > 2048 {
			continue
		}
		candidate := value[token[0]:token[1]]
		hasDigit := false
		for _, r := range candidate {
			hasDigit = hasDigit || unicode.IsDigit(r)
		}
		if !hasDigit {
			continue
		}
		for _, marker := range markers {
			if marker[0] > 2048 {
				continue
			}
			distance := 0
			switch {
			case token[1] < marker[0]:
				distance = marker[0] - token[1]
			case marker[1] < token[0]:
				distance = token[0] - marker[1]
			}
			if distance <= 96 {
				return true
			}
		}
	}
	return false
}
