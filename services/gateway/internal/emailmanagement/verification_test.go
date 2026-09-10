package emailmanagement

import (
	"github.com/Chiiz0/SparkClaw/services/gateway/internal/app"
	"testing"
	"time"
)

func TestVerificationRequiresCurrentBodyAndSupportedLifetime(t *testing.T) {
	at := time.Date(2026, 9, 9, 1, 0, 0, 0, time.UTC)
	input := AnalysisInput{Kind: app.EmailJobClassification, SourceTime: at, Evidence: []Evidence{{Ref: "representation:r:body", Text: "Your login verification code: 482951. Valid for 5 minutes after sending"}}}
	out := AnalysisOutput{Category: "notification", NotificationSubtype: "verification", VerificationCode: "482951", VerificationPurpose: "Login", VerificationEvidenceRef: "representation:r:body", VerificationExpiryEvidence: "Valid for 5 minutes after sending"}
	got, err := verificationFromOutput(input, out)
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(at.Add(5*time.Minute)) {
		t.Fatal("supported expiry not anchored", err)
	}
	out.VerificationExpiryEvidence = ""
	got, err = verificationFromOutput(input, out)
	if err != nil || got.ExpiresAt != nil {
		t.Fatal("invented lifetime")
	}
	out.VerificationEvidenceRef = "representation:r:headers"
	if _, err = verificationFromOutput(input, out); err == nil {
		t.Fatal("header accepted as current code body")
	}
	out.VerificationEvidenceRef = "representation:r:body"
	input.Evidence[0].Text += "\n> Previous code 482951"
	if _, err = verificationFromOutput(input, out); err == nil {
		t.Fatal("quoted repeated code accepted")
	}
	if verificationExpiry(time.Time{}, "Valid for 5 minutes after sending") != nil || verificationExpiry(at, "Valid for a short time") != nil || verificationExpiry(at, "Valid for 5 minutes") != nil {
		t.Fatal("invented timing")
	}
}
