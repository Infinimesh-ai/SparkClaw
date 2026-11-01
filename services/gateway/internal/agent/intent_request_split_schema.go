package agent

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/modelrouter"
)

func requestSplitChatOptions() modelrouter.ChatOptions {
	stringField := func() map[string]any {
		return map[string]any{"type": "string", "description": "An exact disjoint substring of the owner request, or the empty string when absent. Never copy the entire request into object_span."}
	}
	return modelrouter.ChatOptions{ForceDisableThinking: true, StrictJSONSchema: &modelrouter.StrictJSONSchema{
		Name: "intent_request_split", Description: "Verbatim spans and delivery metadata for one owner request.",
		Schema: map[string]any{"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"deferred": map[string]any{"type": "boolean"}, "business_span": stringField(),
				"delivery_mode":        map[string]any{"type": "string", "enum": []string{"none", "current_route", "explicit_external"}},
				"delivery_object_kind": map[string]any{"type": "string", "enum": []string{"business_result", "current_message", "conversation_file", "assistant_text", "named_locator"}},
				"object_span":          stringField(), "provider_span": stringField(), "recipient_span": stringField(),
			}, "required": []string{"deferred", "business_span", "delivery_mode", "delivery_object_kind", "object_span", "provider_span", "recipient_span"}},
	}}
}

// parseRequestSplit rejects missing, duplicate, unknown or null fields even if
// a model provider fails to enforce its advertised strict schema.
func parseRequestSplit(content string) (requestSplit, error) {
	fail := func() (requestSplit, error) { return requestSplit{}, errInvalidRequestSplit }
	if !validRequestSplitEncoding(content) {
		return fail()
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fail()
	}
	values := map[string]json.RawMessage{}
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return fail()
		}
		key, ok := token.(string)
		if !ok {
			return fail()
		}
		if _, exists := values[key]; exists {
			return fail()
		}
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return fail()
		}
		values[key] = raw
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return fail()
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fail()
	}
	keys := []string{"deferred", "business_span", "delivery_mode", "delivery_object_kind", "object_span", "provider_span", "recipient_span"}
	if len(values) != len(keys) {
		return fail()
	}
	for _, key := range keys {
		if raw, ok := values[key]; !ok || string(raw) == "null" {
			return fail()
		}
	}
	var result requestSplit
	// Re-encoding the unique, complete field map allows the typed decoder to
	// reject wrong field types without trusting provider schema enforcement.
	raw, err := json.Marshal(values)
	if err != nil || json.Unmarshal(raw, &result) != nil {
		return fail()
	}
	return result, nil
}

// encoding/json replaces malformed surrogate escapes with U+FFFD. A verbatim
// span protocol must reject those spellings instead of normalizing them.
func validRequestSplitEncoding(content string) bool {
	if !utf8.ValidString(content) {
		return false
	}
	for i := 0; i < len(content); i++ {
		if content[i] != '\\' {
			continue
		}
		i++
		if i >= len(content) {
			return false
		}
		if content[i] != 'u' {
			continue
		}
		if i+4 >= len(content) {
			return false
		}
		n, err := strconv.ParseUint(content[i+1:i+5], 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(content) || content[i+1] != '\\' || content[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(content[i+3:i+7], 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}
