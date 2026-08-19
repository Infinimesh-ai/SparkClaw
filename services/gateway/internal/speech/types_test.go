package speech

import "testing"

func TestErrorDetailsNormalizesEmptyPublicCode(t *testing.T) {
	code, retryable := ErrorDetails(NewError("", "internal upstream error", true, nil))
	if code != CodeInferenceFailed || !retryable {
		t.Fatalf("ErrorDetails() = %q, %t", code, retryable)
	}
}
