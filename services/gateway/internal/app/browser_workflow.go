package app

import "time"

const (
	BrowserWorkflowStateSchemaVersion = 2
	BrowserHandoffSchemaVersion       = 2
	BrowserGoalContractSchemaVersion  = 1
	BrowserWorkflowRevision2          = 2
	BrowserFormDraftMaxActions        = 5
)

type BrowserPresentation string

const (
	BrowserPresentationHidden  BrowserPresentation = "hidden"
	BrowserPresentationVisible BrowserPresentation = "visible"
)

type BrowserTargetKind string

const (
	BrowserTargetExplicitURL           BrowserTargetKind = "explicit_url"
	BrowserTargetRegisteredDestination BrowserTargetKind = "registered_destination"
	BrowserTargetInfoSearch            BrowserTargetKind = "info_search"
	BrowserTargetInfoResolved          BrowserTargetKind = "info_resolved"
	BrowserTargetCurrentTab            BrowserTargetKind = "current_tab"
)

type BrowserQueryProvenance string

const (
	BrowserQueryOwnerSupplied     BrowserQueryProvenance = "owner_supplied"
	BrowserQueryDestinationStatic BrowserQueryProvenance = "destination_static"
	BrowserQueryProviderVolatile  BrowserQueryProvenance = "provider_volatile"
)

type BrowserSessionRef struct {
	OwnerID            string              `json:"owner_id"`
	ProfileID          string              `json:"profile_id"`
	Presentation       BrowserPresentation `json:"presentation"`
	Generation         uint64              `json:"generation"`
	ProviderSessionRef string              `json:"provider_session_ref,omitempty"`
	AcquiredAt         time.Time           `json:"acquired_at,omitempty"`
	LastSeenAt         time.Time           `json:"last_seen_at,omitempty"`
}

type BrowserTargetDescriptor struct {
	TargetKind      BrowserTargetKind      `json:"target_kind"`
	TargetPhrase    string                 `json:"target_phrase,omitempty"`
	CanonicalURL    string                 `json:"canonical_url,omitempty"`
	DestinationID   string                 `json:"destination_id,omitempty"`
	RoutePath       string                 `json:"route_path,omitempty"`
	RouteFragment   string                 `json:"route_fragment,omitempty"`
	QueryProvenance BrowserQueryProvenance `json:"query_provenance,omitempty"`
	RedactedURL     string                 `json:"redacted_url,omitempty"`
}

type BrowserGoalContract struct {
	GoalID                string                  `json:"goal_id"`
	SchemaVersion         int                     `json:"schema_version"`
	OwnerGoal             string                  `json:"owner_goal"`
	RequiredCriteria      []string                `json:"required_criteria"`
	ForbiddenEffects      []string                `json:"forbidden_effects"`
	Target                BrowserTargetDescriptor `json:"target"`
	MaxClicks             int                     `json:"max_clicks"`
	CreatedFromSnapshotID string                  `json:"created_from_snapshot_id"`
	CreatedAt             time.Time               `json:"created_at"`
}

type BrowserResultEvidence struct {
	ID                    string                  `json:"id"`
	SchemaVersion         int                     `json:"schema_version"`
	Target                BrowserTargetDescriptor `json:"target"`
	HiddenSession         BrowserSessionRef       `json:"hidden_session"`
	HiddenPageID          string                  `json:"hidden_page_id"`
	HiddenSnapshotID      string                  `json:"hidden_snapshot_id"`
	HiddenSnapshotDigest  string                  `json:"hidden_snapshot_digest"`
	GoalAssessmentCallID  string                  `json:"goal_assessment_call_id,omitempty"`
	GoalEvidenceRefs      []string                `json:"goal_evidence_refs,omitempty"`
	VisibleSession        BrowserSessionRef       `json:"visible_session,omitempty"`
	VisiblePageID         string                  `json:"visible_page_id,omitempty"`
	VisibleSnapshotID     string                  `json:"visible_snapshot_id,omitempty"`
	VisibleSnapshotDigest string                  `json:"visible_snapshot_digest,omitempty"`
	SourceToolCallIDs     []string                `json:"source_tool_call_ids"`
	VerifiedAt            time.Time               `json:"verified_at,omitempty"`
}

type BrowserPublicTargetEvidence struct {
	EvidenceID         string    `json:"evidence_id"`
	ResolutionSource   string    `json:"resolution_source"`
	OwnerTargetPhrase  string    `json:"owner_target_phrase"`
	RequestedSurface   string    `json:"requested_surface_kind"`
	InfoRequestID      string    `json:"info_request_id,omitempty"`
	InfoResultIndex    *int      `json:"info_result_index,omitempty"`
	SourceResultRef    string    `json:"source_result_ref,omitempty"`
	CanonicalEntryURL  string    `json:"canonical_entry_url"`
	NormalizedFinalURL string    `json:"normalized_final_url"`
	ObservedRedirects  []string  `json:"observed_redirect_chain,omitempty"`
	SafetyGateStatus   string    `json:"safety_gate_status"`
	CreatedAt          time.Time `json:"created_at"`
}

type BrowserDraftAction struct {
	ActionID          string `json:"action_id"`
	SessionGeneration uint64 `json:"session_generation"`
	PageGeneration    uint64 `json:"page_generation"`
	PageID            string `json:"page_id"`
	SnapshotID        string `json:"snapshot_id"`
	SnapshotDigest    string `json:"snapshot_digest"`
	ElementRef        string `json:"element_ref"`
	Role              string `json:"role,omitempty"`
	AccessibleName    string `json:"accessible_name,omitempty"`
	FormContext       string `json:"form_context,omitempty"`
	Operation         string `json:"operation"`
	ValueSource       string `json:"value_source"`
	ValueDigest       string `json:"value_digest"`
	Completed         bool   `json:"completed"`
}

type BrowserVisualEvidence struct {
	EvidenceID        string    `json:"evidence_id"`
	Reason            string    `json:"reason"`
	SessionGeneration uint64    `json:"session_generation"`
	PageGeneration    uint64    `json:"page_generation"`
	PageID            string    `json:"page_id"`
	SnapshotID        string    `json:"snapshot_id"`
	SnapshotDigest    string    `json:"snapshot_digest"`
	ScreenshotRef     string    `json:"screenshot_ref"`
	ScreenshotDigest  string    `json:"screenshot_digest"`
	NormalizedURL     string    `json:"normalized_url"`
	Summary           string    `json:"summary"`
	Model             string    `json:"model"`
	Stale             bool      `json:"stale"`
	CreatedAt         time.Time `json:"created_at"`
}

type BrowserWorkflowState struct {
	SchemaVersion   int                          `json:"schema_version"`
	Target          BrowserTargetDescriptor      `json:"target"`
	Goal            *BrowserGoalContract         `json:"goal,omitempty"`
	Result          *BrowserResultEvidence       `json:"result,omitempty"`
	PublicTarget    *BrowserPublicTargetEvidence `json:"public_target,omitempty"`
	DraftActions    []BrowserDraftAction         `json:"draft_actions,omitempty"`
	VisualEvidence  []BrowserVisualEvidence      `json:"visual_evidence,omitempty"`
	CompletedClicks int                          `json:"completed_clicks,omitempty"`
}
