package agent

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// requestSplit carries only verbatim spans of the owner request and typed metadata.
type requestSplit struct {
	Deferred           bool   `json:"deferred"`
	BusinessSpan       string `json:"business_span"`
	DeliveryMode       string `json:"delivery_mode"`
	DeliveryObjectKind string `json:"delivery_object_kind"`
	ObjectSpan         string `json:"object_span"`
	ProviderSpan       string `json:"provider_span"`
	RecipientSpan      string `json:"recipient_span"`
	// WholeRequest marks an external send kept whole because a constraint did
	// not fit one business span; it is never part of the model output.
	WholeRequest bool `json:"-"`
}

var errInvalidRequestSplit = errors.New("invalid request split")

// validateRequestSplit accounts for all original text without deriving a split.
func validateRequestSplit(text string, s requestSplit, externalMCP bool) error {
	if s.Deferred && (s.BusinessSpan != strings.TrimSpace(text) || s.DeliveryMode != "none" || s.ObjectSpan != "" || s.ProviderSpan != "" || s.RecipientSpan != "") {
		return fmt.Errorf("%w: deferred must retain the ENTIRE original owner request verbatim in business_span, including all file and delivery words; mode must be none and all other spans must be empty", errInvalidRequestSplit)
	}
	mode := s.DeliveryMode == "none" || s.DeliveryMode == "current_route" || s.DeliveryMode == "explicit_external"
	object := s.DeliveryObjectKind == "business_result" || s.DeliveryObjectKind == "current_message" || s.DeliveryObjectKind == "conversation_file" || s.DeliveryObjectKind == "assistant_text" || s.DeliveryObjectKind == "named_locator"
	business, provider, recipient := strings.TrimSpace(s.BusinessSpan), strings.TrimSpace(s.ProviderSpan), strings.TrimSpace(s.RecipientSpan)
	if !utf8.ValidString(text) || !mode || !object || (externalMCP && (s.DeliveryObjectKind == "conversation_file" || s.DeliveryObjectKind == "assistant_text")) ||
		(s.DeliveryMode != "explicit_external" && (provider != "" || recipient != "")) ||
		(s.DeliveryMode == "explicit_external" && provider == "" && recipient == "") ||
		(business != "" && s.DeliveryObjectKind != "business_result") ||
		(strings.TrimSpace(s.ObjectSpan) != "" && splitObjectContainsConstraint(s.ObjectSpan)) ||
		(business == "" && (s.DeliveryMode == "none" || s.DeliveryObjectKind == "business_result")) ||
		(s.Deferred && (business != strings.TrimSpace(text) || s.DeliveryMode != "none")) {
		return errInvalidRequestSplit
	}
	spans := []string{business, strings.TrimSpace(s.ObjectSpan), provider, recipient}
	// Bound work even for adversarial repeated spans; fail closed when exhausted.
	budget := 4096
	if splitSpanAssignments(text, spans, 0, make([]bool, len(text)), &budget) != 1 || budget < 0 {
		return errInvalidRequestSplit
	}
	return nil
}

// Count at most two valid disjoint assignments. Ambiguous repeated spans are
// not silently assigned to an arbitrary occurrence.
func splitSpanAssignments(text string, spans []string, index int, used []bool, budget *int) int {
	*budget--
	if *budget < 0 {
		return 0
	}
	if index == len(spans) {
		var residual strings.Builder
		for i := range len(text) {
			if used[i] {
				residual.WriteByte(' ')
			} else {
				residual.WriteByte(text[i])
			}
		}
		if isDeliveryResidual(residual.String()) {
			return 1
		}
		return 0
	}
	span := spans[index]
	if span == "" {
		return splitSpanAssignments(text, spans, index+1, used, budget)
	}
	if !utf8.ValidString(span) {
		return 0
	}
	matches := 0
	for start := 0; start <= len(text)-len(span); {
		*budget--
		if *budget < 0 {
			return 0
		}
		offset := strings.Index(text[start:], span)
		if offset < 0 {
			break
		}
		at := start + offset
		free := utf8.RuneStart(text[at]) && (at+len(span) == len(text) || utf8.RuneStart(text[at+len(span)]))
		for i := at; free && i < at+len(span); i++ {
			if used[i] {
				free = false
			}
		}
		if free {
			for i := at; i < at+len(span); i++ {
				used[i] = true
			}
			matches += splitSpanAssignments(text, spans, index+1, used, budget)
			for i := at; i < at+len(span); i++ {
				used[i] = false
			}
			if matches > 1 {
				return 2
			}
		}
		start = at + 1
	}
	return matches
}

func splitObjectContainsConstraint(object string) bool {
	lower := strings.ToLower(object)
	if lower == "不" || containsAny(lower, "不要", "不得", "不能", "不准", "不可", "别", "勿", "禁止", "只读", "先确认", "不发送", "不删除") {
		return true
	}
	return containsEnglishSemanticTerm(lower, "not", "no", "never", "without", "only", "cannot") || strings.Contains(lower, "don't") || strings.Contains(lower, "can't") || strings.Contains(lower, "read only") || strings.Contains(lower, "confirm first")
}

// This allowlist validates residual text; it is never used to split requests.
var deliveryResidualWords = []string{
	"发送给", "发送到", "转发给", "投递给", "发给", "发到", "发送", "转发", "投递", "传给", "出去", "发", "传", "给", "交回",
	"我这里", "我", "然后", "之后", "通过", "经由", "再", "并", "后", "用", "到", "把", "将", "一下", "吧", "呢",
	"send", "deliver", "forward", "share", "return", "me", "it", "back", "and", "then", "to", "via", "please",
}

func isDeliveryResidual(value string) bool {
	value = strings.ToLower(value)
	valid := make([]bool, len(value)+1)
	valid[len(value)] = true
	for i := len(value) - 1; i >= 0; i-- {
		if !utf8.RuneStart(value[i]) {
			continue
		}
		r, size := utf8.DecodeRuneInString(value[i:])
		if (unicode.IsSpace(r) || unicode.IsPunct(r)) && valid[i+size] {
			valid[i] = true
			continue
		}
		for _, word := range deliveryResidualWords {
			end := i + len(word)
			if end > len(value) || !strings.HasPrefix(value[i:], word) || !valid[end] {
				continue
			}
			if word[0] < utf8.RuneSelf && ((i > 0 && asciiLetter(value[i-1])) || (end < len(value) && asciiLetter(value[end]))) {
				continue
			}
			valid[i] = true
			break
		}
	}
	return valid[0]
}
func asciiLetter(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' }
