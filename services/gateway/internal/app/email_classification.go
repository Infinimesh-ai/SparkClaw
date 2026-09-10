package app

import "time"

const EmailJobClassification = "classification"

type EmailClassification struct {
	ID            string `json:"id,omitempty"`
	InputPath     string `json:"input_path,omitempty"`
	InputSHA256   string `json:"input_sha256,omitempty"`
	OutputPath    string `json:"output_path,omitempty"`
	OutputSHA256  string `json:"output_sha256,omitempty"`
	ModelVersion  string `json:"model_version,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
	Stage         string `json:"stage,omitempty"`

	RequestedResponse   string                        `json:"requested_response,omitempty"`
	Purpose             string                        `json:"purpose,omitempty"`
	ServiceLabel        string                        `json:"service_label,omitempty"`
	Reason              string                        `json:"reason,omitempty"`
	Evidence            []EmailClassificationEvidence `json:"evidence,omitempty"`
	Category            string                        `json:"category"`
	EffectiveEntry      string                        `json:"effective_entry"`
	Source              string                        `json:"source"`
	NotificationSubtype string                        `json:"notification_subtype"`
	State               string                        `json:"state"`
	Revision            int64                         `json:"revision"`
	RuleRevision        int64                         `json:"rule_revision"`
	SenderAddress       string                        `json:"sender_address"`
	ReasonCode          string                        `json:"reason_code"`
	EvidenceRefs        []string                      `json:"evidence_refs"`
	Uncertainty         bool                          `json:"uncertainty"`
	InputFingerprint    string                        `json:"input_fingerprint"`
	UpdatedAt           time.Time                     `json:"updated_at"`
}
type EmailSenderRule struct {
	ID                     string    `json:"id"`
	Address                string    `json:"address"`
	Entry                  string    `json:"entry"`
	Enabled                bool      `json:"enabled"`
	Revision               int64     `json:"revision"`
	EffectiveAfterSequence int64     `json:"effective_after_sequence"`
	UpdatedAt              time.Time `json:"updated_at"`
}

type EmailVerification struct {
	Code           string     `json:"code"`
	Purpose        string     `json:"purpose"`
	EvidenceRef    string     `json:"evidence_ref"`
	ExpiryEvidence string     `json:"expiry_evidence"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Revision       int64      `json:"revision"`
}

type EmailClassificationEvidence struct {
	Ref  string `json:"ref"`
	Text string `json:"text"`
}
