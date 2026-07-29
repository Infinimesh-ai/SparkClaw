package app

import "time"

const (
	BrowserWorkflowStateSchemaVersion = 2
	BrowserHandoffSchemaVersion       = 2
	BrowserGoalContractSchemaVersion  = 1
	BrowserWorkflowRevision2          = 2
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

type BrowserWorkflowState struct {
	SchemaVersion   int                     `json:"schema_version"`
	Target          BrowserTargetDescriptor `json:"target"`
	Goal            *BrowserGoalContract    `json:"goal,omitempty"`
	Result          *BrowserResultEvidence  `json:"result,omitempty"`
	CompletedClicks int                     `json:"completed_clicks,omitempty"`
}
