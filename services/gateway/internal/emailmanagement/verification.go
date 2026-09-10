package emailmanagement

import (
	"errors"
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var verificationCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 -]{2,30}[A-Za-z0-9]$`)

func verificationFromOutput(input AnalysisInput, out AnalysisOutput) (*app.EmailVerification, error) {
	invalid := errors.New("email_verification_invalid")
	if out.VerificationCode == "" {
		if out.VerificationPurpose != "" || out.VerificationEvidenceRef != "" || out.VerificationExpiryEvidence != "" {
			return nil, invalid
		}
		return nil, nil
	}
	if input.Kind != app.EmailJobClassification || out.Category != "notification" || out.NotificationSubtype != "verification" || out.Uncertainty || !verificationCodePattern.MatchString(out.VerificationCode) || len(out.VerificationPurpose) > 200 || strings.TrimSpace(out.VerificationPurpose) == "" || len(out.VerificationExpiryEvidence) > 300 {
		return nil, invalid
	}
	for _, e := range input.Evidence {
		if e.Ref != out.VerificationEvidenceRef || !strings.HasSuffix(e.Ref, ":body") {
			continue
		}
		// Suppress quick copy if the bounded body appears quoted or exposes multiple
		// instances. Full source remains available for explicit user judgment.
		if strings.Count(e.Text, out.VerificationCode) != 1 || strings.Contains(e.Text, "\n>") || strings.Contains(e.Text, "-----Original Message-----") || strings.Contains(e.Text, "---------- Forwarded message") {
			return nil, invalid
		}
		if out.VerificationExpiryEvidence != "" && !strings.Contains(e.Text, out.VerificationExpiryEvidence) {
			return nil, invalid
		}
		v := &app.EmailVerification{Code: out.VerificationCode, Purpose: out.VerificationPurpose, EvidenceRef: e.Ref, ExpiryEvidence: out.VerificationExpiryEvidence}
		v.ExpiresAt = verificationExpiry(input.SourceTime, out.VerificationExpiryEvidence)
		return v, nil
	}
	return nil, invalid
}

var verificationLifetime = regexp.MustCompile(`(?i)^(?:valid for|有效期(?:为)?)\s*([1-9][0-9]{0,3})\s*(minutes?|hours?|分钟|小时)\s*(?:after sending|from sending|自发送时起|从发送时起)[.!。]?$`)

func verificationExpiry(source time.Time, evidence string) *time.Time {
	if at, err := time.Parse(time.RFC3339, strings.TrimSpace(evidence)); err == nil {
		return &at
	}
	parts := verificationLifetime.FindStringSubmatch(strings.TrimSpace(evidence))
	if len(parts) != 3 || source.IsZero() || source.After(time.Now().Add(time.Minute)) {
		return nil
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil
	}
	unit := time.Minute
	if strings.HasPrefix(strings.ToLower(parts[2]), "hour") || parts[2] == "小时" {
		unit = time.Hour
	}
	duration := time.Duration(n) * unit
	if duration > 24*time.Hour {
		return nil
	}
	deadline := source.Add(duration)
	return &deadline
}
