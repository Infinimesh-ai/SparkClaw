package agent

type TaskHint struct {
	TaskType           string   `json:"task_type"`
	EvidenceNeed       string   `json:"evidence_need"`
	ToolMode           string   `json:"tool_mode"`
	BrowserMode        string   `json:"browser_mode,omitempty"`
	EstimatedRisk      string   `json:"estimated_risk"`
	ModelLaneHint      string   `json:"model_lane_hint"`
	CandidateSkills    []string `json:"candidate_skills"`
	CandidateTools     []string `json:"candidate_tools"`
	NeedsClarification bool     `json:"needs_clarification"`
	Reason             string   `json:"reason"`
}

type reactAction struct {
	Type      string         `json:"type"`
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	Reason    string         `json:"reason,omitempty"`
}

type reactFinal struct {
	Type   string `json:"type"`
	Answer string `json:"answer"`
}

type reactOutput struct {
	Kind   string
	Action reactAction
	Final  reactFinal
}
