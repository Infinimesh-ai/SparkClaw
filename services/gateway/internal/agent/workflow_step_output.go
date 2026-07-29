package agent

type workflowStepAction struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Reason    string         `json:"reason,omitempty"`
}

type workflowStepFinal struct {
	Type   string `json:"type"`
	Answer string `json:"answer"`
}

type workflowStepOutput struct {
	Kind   string
	Action workflowStepAction
	Final  workflowStepFinal
}
