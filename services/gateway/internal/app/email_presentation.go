package app

const EmailJobPresentation = "presentation"
const EmailPresentationPromptVersion = "email-presentation-v7"

// EmailLocalizedPresentation is a language-specific projection, never an
// assignment or classification decision. Its ID includes all input revisions.
type EmailLocalizedPresentation struct {
	Purpose             string                        `json:"purpose,omitempty"`
	ServiceLabel        string                        `json:"service_label,omitempty"`
	RequestedResponse   string                        `json:"requested_response,omitempty"`
	Evidence            []EmailClassificationEvidence `json:"evidence,omitempty"`
	ID                  string                        `json:"id"`
	TargetKind          string                        `json:"target_kind"`
	TargetID            string                        `json:"target_id"`
	Language            string                        `json:"presentation_language"`
	State               string                        `json:"state"`
	AnalysisRevision    string                        `json:"analysis_revision"`
	Revision            int64                         `json:"revision"`
	PromptVersion       string                        `json:"prompt_version"`
	Title               string                        `json:"title,omitempty"`
	Summary             string                        `json:"summary,omitempty"`
	Explanation         string                        `json:"explanation,omitempty"`
	ConcernExplanations map[string]string             `json:"concern_explanations,omitempty"`
	ErrorCode           string                        `json:"error_code,omitempty"`
}
