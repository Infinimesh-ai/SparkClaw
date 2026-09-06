package jingsiruntime

import "time"

const (
	Protocol  = "jingsi-sparkclaw/v1"
	MediaType = "application/vnd.infinimesh.sparkclaw-runtime.v1+json"
)

type Authorization struct {
	SpaceID        string    `json:"space_id"`
	TaskID         string    `json:"task_id"`
	Purpose        Purpose   `json:"purpose"`
	Grant          OpaqueRef `json:"grant"`
	ToolScope      []string  `json:"tool_scope"`
	DataScope      []string  `json:"data_scope"`
	NetworkScope   []string  `json:"network_scope"`
	ApprovalPolicy string    `json:"approval_policy"`
	DeadlineAt     time.Time `json:"deadline_at"`
}

type Purpose struct {
	Name string `json:"name"`
}

type OpaqueRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

type ArtifactRef struct {
	ID        string `json:"id"`
	Version   string `json:"version"`
	Kind      string `json:"kind"`
	MediaType string `json:"media_type,omitempty"`
}

type MemoryContext struct {
	Summary    string      `json:"summary"`
	Confidence float64     `json:"confidence"`
	MemoryRefs []OpaqueRef `json:"memory_refs"`
	SourceRefs []OpaqueRef `json:"source_refs,omitempty"`
}

type Budget struct {
	MaxRuntimeMS   int64 `json:"max_runtime_ms"`
	MaxToolCalls   int   `json:"max_tool_calls"`
	MaxOutputBytes int   `json:"max_output_bytes"`
}

type SubmitPayload struct {
	RequestKey    string         `json:"request_key"`
	Goal          string         `json:"goal"`
	Target        string         `json:"target"`
	MemoryContext *MemoryContext `json:"memory_context,omitempty"`
	Budget        Budget         `json:"budget"`
}

type SubmitRequest struct {
	Protocol      string        `json:"protocol"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Authorization Authorization `json:"authorization"`
	Payload       SubmitPayload `json:"payload"`
}

type LookupRequest struct {
	Protocol      string        `json:"protocol"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Authorization Authorization `json:"authorization"`
	Payload       struct {
		RequestKey string `json:"request_key"`
	} `json:"payload"`
}

type ExecutionRequest struct {
	Protocol      string        `json:"protocol"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Authorization Authorization `json:"authorization"`
	Payload       struct {
		ExecutionID string `json:"execution_id"`
	} `json:"payload"`
}

type CancelRequest struct {
	Protocol      string        `json:"protocol"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Authorization Authorization `json:"authorization"`
	Payload       struct {
		ExecutionID string `json:"execution_id"`
		ReasonCode  string `json:"reason_code"`
	} `json:"payload"`
}

type EventsRequest struct {
	Protocol      string        `json:"protocol"`
	Kind          string        `json:"kind"`
	RequestID     string        `json:"request_id"`
	Authorization Authorization `json:"authorization"`
	Payload       struct {
		ExecutionID   string `json:"execution_id"`
		AfterSequence uint64 `json:"after_sequence"`
		Limit         int    `json:"limit"`
	} `json:"payload"`
}

type ExecutionRef struct {
	ExecutionID string     `json:"execution_id"`
	State       string     `json:"state"`
	AcceptedAt  time.Time  `json:"accepted_at"`
	TraceRef    *OpaqueRef `json:"trace_ref,omitempty"`
}

type ExecutionEvent struct {
	Sequence    uint64       `json:"sequence"`
	EventID     string       `json:"event_id"`
	At          time.Time    `json:"at"`
	Type        string       `json:"type"`
	State       string       `json:"state,omitempty"`
	SummaryCode string       `json:"summary_code,omitempty"`
	ArtifactRef *ArtifactRef `json:"artifact_ref,omitempty"`
	TraceRef    *OpaqueRef   `json:"trace_ref,omitempty"`
}

type ExecutionResult struct {
	Outcome      string        `json:"outcome"`
	CompletedAt  time.Time     `json:"completed_at"`
	Summary      string        `json:"summary,omitempty"`
	ArtifactRefs []ArtifactRef `json:"artifact_refs"`
	TraceRef     OpaqueRef     `json:"trace_ref"`
}

type ExecutionInput struct {
	ExecutionID   string
	Authorization Authorization
	Goal          string
	Memory        *MemoryContext
	Budget        Budget
}

type ExecutionOutput struct {
	State        string
	Summary      string
	ArtifactRefs []ArtifactRef
	TraceRef     OpaqueRef
}
