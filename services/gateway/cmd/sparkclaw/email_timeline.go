package main

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

// These adapters passed native server-range and exact-original qualification.
// Outlook's Reader independently gates the consumer origin; M365 cannot inherit
// this capability and never falls back to a locally filtered head page.
func qualifiedEmailTimelineModes() map[string]string {
	return map[string]string{
		app.EmailProviderQQMail:  app.EmailProviderModeTimeRange,
		app.EmailProviderGmail:   app.EmailProviderModeTimeRange,
		app.EmailProviderOutlook: app.EmailProviderModeTimeRange,
	}
}
