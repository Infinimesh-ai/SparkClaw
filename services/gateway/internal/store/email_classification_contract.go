package store

import "github.com/Chiiz0/SparkClaw/services/gateway/internal/app"

type EmailClassificationCommand struct {
	EmailCommand
	Lease          EmailJobLease
	MailID         string
	Generation     int64
	Verification   *app.EmailVerification
	Classification app.EmailClassification
}
type EmailClassificationOverride struct {
	EmailCommand
	MailID              string
	Entry               string
	ExpectedVersion     int64
	RememberSender      bool
	ExpectedRuleVersion int64
}
type EmailSenderRuleCommand struct {
	EmailCommand
	RuleID          string
	Entry           string
	Enabled         bool
	ExpectedVersion int64
}
