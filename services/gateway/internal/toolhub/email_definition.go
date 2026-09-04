package toolhub

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

func emailSendDefinition() app.ToolDefinition {
	return app.ToolDefinition{
		Name:        "email.send",
		Description: "Send exactly one approved plain-text email through the Runtime-selected browser provider and account.",
		InputSchema: strictObjectSchema([]string{
			"provider", "account", "account_hint", "recipient", "body", "setting_version", "browser_generation",
			"probe_revision", "send_script_revision", "validated_at", "invocation_id",
		}, map[string]any{
			"provider":             map[string]any{"type": "string", "enum": []any{app.EmailProviderQQMail, app.EmailProviderOutlook, app.EmailProviderGmail}},
			"account":              map[string]any{"type": "string", "enum": []any{app.EmailAccountDefault}},
			"account_hint":         map[string]any{"type": "string", "maxLength": 64},
			"recipient":            map[string]any{"type": "string", "minLength": 3, "maxLength": 320},
			"subject":              map[string]any{"type": "string", "maxLength": 998},
			"body":                 map[string]any{"type": "string", "minLength": 1, "maxLength": 204800},
			"setting_version":      stringSchema(),
			"browser_generation":   stringSchema(),
			"probe_revision":       stringSchema(),
			"send_script_revision": stringSchema(),
			"validated_at":         stringSchema(),
			"invocation_id":        stringSchema(),
		}),
		OutputSchema: strictObjectSchema([]string{"provider", "status", "recipient_digest", "browser_generation", "script_revision"}, map[string]any{
			"provider":            stringSchema(),
			"status":              map[string]any{"type": "string", "enum": []any{"sent"}},
			"recipient_digest":    stringSchema(),
			"provider_message_id": stringSchema(),
			"browser_generation":  integerSchema(),
			"script_revision":     integerSchema(),
		}),
		Risk: app.RiskDangerous, RequiresApproval: true, Idempotent: false,
		TimeoutMS: 90000, Sandbox: "forbidden", Audit: "always",
	}
}
