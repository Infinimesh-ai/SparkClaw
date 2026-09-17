package emailmanagement

import "testing"

func TestVerificationNoticePatternRequiresMarkerAndNearbyCode(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "english otp", body: "Your verification code is 482193. It expires in ten minutes.", want: true},
		{name: "chinese code", body: "您的登录验证码为 739201，请勿转发。", want: true},
		{name: "spaced code", body: "Security code: 123 456", want: true},
		{name: "invoice number", body: "Invoice 482193 is ready for payment.", want: false},
		{name: "order number", body: "Order number AB123456 has shipped.", want: false},
		{name: "marker without token", body: "Never share your verification code with anyone.", want: false},
		{name: "mixed purchase request", body: "Your verification code is 482193. Please approve purchase order 2910.", want: false},
		{name: "distant unrelated number", body: "Verification code" + string(make([]byte, 97)) + " reference 482193", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := AnalysisInput{Evidence: []Evidence{{Ref: "representation:mail:body", Text: test.body}}}
			_, got := verificationNoticePattern(input)
			if got != test.want {
				t.Fatalf("verificationNoticePattern() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestVerificationNoticePatternRejectsMixedEvidence(t *testing.T) {
	input := AnalysisInput{Evidence: []Evidence{
		{Ref: "representation:mail:subject", Text: "Your verification code is 482193"},
		{Ref: "representation:mail:body", Text: "Please review the attached contract."},
	}}
	if _, ok := verificationNoticePattern(input); ok {
		t.Fatal("mixed verification and business-request evidence bypassed semantic classification")
	}
}

func TestVerificationNoticePatternIgnoresNonSourceEvidence(t *testing.T) {
	input := AnalysisInput{Evidence: []Evidence{{Ref: "candidate:event:summary", Text: "Verification code 482193"}}}
	if _, ok := verificationNoticePattern(input); ok {
		t.Fatal("generated candidate evidence triggered the source-only pattern")
	}
}
