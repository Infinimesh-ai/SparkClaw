package main

import (
	"time"

	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/config"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/emailmanagement"
)

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

// Direct fetch and the relaxed sweep stay off unless the deployment opts in.
func emailPushDirectFetch(intake config.EmailIntakeConfig) emailmanagement.PushDirectFetchOptions {
	return emailmanagement.PushDirectFetchOptions{Enabled: intake.PushDirectFetch, Threshold: intake.PushQualificationThreshold,
		QualifiedPollInterval: time.Duration(intake.QualifiedPollIntervalSeconds) * time.Second}
}
