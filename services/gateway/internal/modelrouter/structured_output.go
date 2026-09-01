package modelrouter

import (
	"errors"
	"fmt"
	"strings"
)

// StrictJSONSchema describes one OpenAI-compatible strict structured-output
// contract. Runtime validation remains authoritative after model generation.
type StrictJSONSchema struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ChatOptions applies to one chat invocation without changing the selected
// profile's behavior for other model consumers.
type ChatOptions struct {
	ForceDisableThinking bool
	StrictJSONSchema     *StrictJSONSchema
}

func (options ChatOptions) validate() error {
	if options.StrictJSONSchema == nil {
		return nil
	}
	return validateStrictJSONSchema(*options.StrictJSONSchema)
}

func (options ChatOptions) applyToRequest(body map[string]any, globallyDisableThinking bool) error {
	if globallyDisableThinking || options.ForceDisableThinking {
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": false}
	}
	if options.StrictJSONSchema == nil {
		return nil
	}
	if err := options.validate(); err != nil {
		return err
	}
	jsonSchema := map[string]any{
		"name":   strings.TrimSpace(options.StrictJSONSchema.Name),
		"strict": true,
		"schema": options.StrictJSONSchema.Schema,
	}
	if description := strings.TrimSpace(options.StrictJSONSchema.Description); description != "" {
		jsonSchema["description"] = description
	}
	body["response_format"] = map[string]any{
		"type":        "json_schema",
		"json_schema": jsonSchema,
	}
	return nil
}

func validateStrictJSONSchema(schema StrictJSONSchema) error {
	name := strings.TrimSpace(schema.Name)
	if name == "" {
		return errors.New("strict JSON schema name is required")
	}
	if len(name) > 64 {
		return errors.New("strict JSON schema name exceeds 64 bytes")
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			continue
		}
		return fmt.Errorf("strict JSON schema name contains invalid character %q", char)
	}
	if len(schema.Schema) == 0 {
		return errors.New("strict JSON schema body is required")
	}
	return nil
}
