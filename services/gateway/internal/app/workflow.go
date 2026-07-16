package app

type IntentDomain string

const (
	IntentDomainWeb       IntentDomain = "web"
	IntentDomainWorkspace IntentDomain = "workspace"
)

type IntentOperation string

const (
	IntentOperationSearch   IntentOperation = "search"
	IntentOperationRead     IntentOperation = "read"
	IntentOperationAutomate IntentOperation = "automate"
	IntentOperationProcess  IntentOperation = "process"
)

type TargetKind string

const (
	TargetKindNone          TargetKind = "none"
	TargetKindExplicitURL   TargetKind = "explicit_url"
	TargetKindWorkspacePath TargetKind = "workspace_path"
)

type OutputKind string

const (
	OutputKindText  OutputKind = "text"
	OutputKindFile  OutputKind = "file"
	OutputKindImage OutputKind = "image"
)

type DataScope string

const (
	DataScopePublic    DataScope = "public"
	DataScopeWorkspace DataScope = "workspace"
)

type EvidenceDepth string

const (
	EvidenceDepthSummary EvidenceDepth = "summary"
	EvidenceDepthSource  EvidenceDepth = "source"
)

type IntentResolutionStatus string

const (
	IntentResolved           IntentResolutionStatus = "resolved"
	IntentNeedsClarification IntentResolutionStatus = "needs_clarification"
)

type IntentEnvelope struct {
	Version      int               `json:"version"`
	SourceTurnID string            `json:"source_turn_id"`
	Objectives   []Objective       `json:"objectives"`
	Constraints  IntentConstraints `json:"constraints"`
	Resolution   IntentResolution  `json:"resolution"`
}

type Objective struct {
	ID        string          `json:"id"`
	Domain    IntentDomain    `json:"domain"`
	Operation IntentOperation `json:"operation"`
	Target    TargetRef       `json:"target"`
	Output    OutputKind      `json:"output"`
	Explicit  bool            `json:"explicit"`
}

type IntentConstraints struct {
	DataScope     DataScope     `json:"data_scope"`
	EvidenceDepth EvidenceDepth `json:"evidence_depth,omitempty"`
}

type IntentResolution struct {
	Status       IntentResolutionStatus `json:"status"`
	MissingSlots []string               `json:"missing_slots,omitempty"`
}

type TargetRef struct {
	Kind TargetKind `json:"kind"`
	Ref  string     `json:"ref,omitempty"`
}

type WorkflowID string
type WorkflowNodeID string
type TransitionID string
type ToolDirectoryEntryID string
type ToolOutcomeAdapter string
type ToolEffect string
type OutcomeSignal string
type AssessmentStatus string
type CompletionRule string
type WorkflowStatus string
type WorkflowNodeStatus string
type ArgumentBindingSource string

const (
	WorkflowBrowserSearch       WorkflowID = "browser.search"
	WorkflowBrowserAutomation   WorkflowID = "browser.automation"
	WorkflowDocumentInformation WorkflowID = "document.information"
	WorkflowDocumentProcessing  WorkflowID = "document.processing"

	// Legacy identities remain exact so persisted state is never silently
	// reinterpreted as a different contract during migration.
	WorkflowWebPublicResearch WorkflowID = "web.public_research"
	WorkflowWebExplicitURL    WorkflowID = "web.explicit_url_read"
	WorkflowWorkspaceSearch   WorkflowID = "workspace.file_search"
	WorkflowWorkspaceRead     WorkflowID = "workspace.file_read"

	ToolEffectExternalRead     ToolEffect = "external.read"
	ToolEffectExternalInteract ToolEffect = "external.interact"
	ToolEffectWorkspaceRead    ToolEffect = "workspace.read"
	ToolEffectWorkspaceWrite   ToolEffect = "workspace.write"

	OutcomeAdapterGeneric         ToolOutcomeAdapter = "generic"
	OutcomeAdapterWebSearch       ToolOutcomeAdapter = "web.search"
	OutcomeAdapterWebPage         ToolOutcomeAdapter = "web.page"
	OutcomeAdapterWorkspaceSearch ToolOutcomeAdapter = "workspace.search"
	OutcomeAdapterWorkspaceRead   ToolOutcomeAdapter = "workspace.read"

	OutcomeSignalResultsAvailable       OutcomeSignal = "results_available"
	OutcomeSignalNoResults              OutcomeSignal = "no_results"
	OutcomeSignalContentAvailable       OutcomeSignal = "content_available"
	OutcomeSignalSourcePageAvailable    OutcomeSignal = "source_page_available"
	OutcomeSignalStructureRequired      OutcomeSignal = "structure_required"
	OutcomeSignalAuthenticationRequired OutcomeSignal = "authentication_required"

	AssessmentComplete          AssessmentStatus = "complete"
	AssessmentNeedsMoreEvidence AssessmentStatus = "needs_more_evidence"
	AssessmentBlocked           AssessmentStatus = "blocked"

	CompletionEvidence CompletionRule = "evidence"

	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusSucceeded WorkflowStatus = "succeeded"
	WorkflowStatusBlocked   WorkflowStatus = "blocked"

	WorkflowNodePending   WorkflowNodeStatus = "pending"
	WorkflowNodeActive    WorkflowNodeStatus = "active"
	WorkflowNodeSucceeded WorkflowNodeStatus = "succeeded"
	WorkflowNodeBlocked   WorkflowNodeStatus = "blocked"

	ArgumentBindingIntentTarget ArgumentBindingSource = "intent_target"
	ArgumentBindingOutcomeRef   ArgumentBindingSource = "outcome_ref"
)

type CapabilityRequirement struct {
	Name       string            `json:"name"`
	Qualifiers map[string]string `json:"qualifiers,omitempty"`
}

type CapabilityDescriptor struct {
	Name       string            `json:"name"`
	Qualifiers map[string]string `json:"qualifiers,omitempty"`
}

type CapabilityScope struct {
	Requirements  []CapabilityRequirement `json:"requirements"`
	DeniedEffects []ToolEffect            `json:"denied_effects,omitempty"`
}

type NodeGoal struct {
	ObjectiveIDs []string       `json:"objective_ids"`
	Summary      string         `json:"summary"`
	Completion   CompletionRule `json:"completion"`
}

type TransitionPredicate struct {
	OutcomeSignals []OutcomeSignal    `json:"outcome_signals,omitempty"`
	Assessments    []AssessmentStatus `json:"assessments,omitempty"`
}

type ScopeTransition struct {
	ID             TransitionID            `json:"id"`
	On             TransitionPredicate     `json:"on"`
	Add            []CapabilityRequirement `json:"add"`
	Replace        *CapabilityScope        `json:"replace,omitempty"`
	MaxActivations int                     `json:"max_activations"`
}

type ArgumentBinding struct {
	Capability   string                `json:"capability"`
	Argument     string                `json:"argument"`
	ResourceKind string                `json:"resource_kind"`
	Source       ArgumentBindingSource `json:"source"`
	TargetKinds  []TargetKind          `json:"target_kinds,omitempty"`
}

type WorkflowNode struct {
	ID               WorkflowNodeID    `json:"id"`
	DependsOn        []WorkflowNodeID  `json:"depends_on,omitempty"`
	Goal             NodeGoal          `json:"goal"`
	InitialScope     CapabilityScope   `json:"initial_scope"`
	Transitions      []ScopeTransition `json:"transitions,omitempty"`
	ArgumentBindings []ArgumentBinding `json:"argument_bindings,omitempty"`
	AllowedRisks     []RiskLevel       `json:"allowed_risks"`
	MaxAttempts      int               `json:"max_attempts"`
}

type WorkflowPlan struct {
	SchemaVersion   int              `json:"schema_version"`
	ProfileID       WorkflowID       `json:"profile_id"`
	ProfileRevision int              `json:"profile_revision"`
	SkillIDs        []string         `json:"skill_ids,omitempty"`
	InitialNodeIDs  []WorkflowNodeID `json:"initial_node_ids"`
	Nodes           []WorkflowNode   `json:"nodes"`
	Completion      CompletionRule   `json:"completion"`
}

type ResourceRef struct {
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	Provenance string `json:"provenance"`
}

type ToolOutcome struct {
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	Tool       string          `json:"tool"`
	NodeID     WorkflowNodeID  `json:"node_id"`
	Status     string          `json:"status"`
	Signals    []OutcomeSignal `json:"signals,omitempty"`
	Refs       []ResourceRef   `json:"refs,omitempty"`
	Retryable  bool            `json:"retryable,omitempty"`
}

type NodeAssessment struct {
	OutcomeID  string           `json:"outcome_id"`
	NodeID     WorkflowNodeID   `json:"node_id"`
	Status     AssessmentStatus `json:"status"`
	Signals    []OutcomeSignal  `json:"signals,omitempty"`
	ReasonCode string           `json:"reason_code,omitempty"`
}

type DirectoryViewRef struct {
	ViewID            string                 `json:"view_id"`
	DirectoryRevision string                 `json:"directory_revision"`
	EntryIDs          []ToolDirectoryEntryID `json:"entry_ids"`
}

type ExposureRequest struct {
	RunID         string         `json:"run_id"`
	WorkflowID    WorkflowID     `json:"workflow_id"`
	NodeID        WorkflowNodeID `json:"node_id"`
	ScopeRevision int            `json:"scope_revision"`
	ActorRef      string         `json:"actor_ref"`
	Limit         int            `json:"limit"`
}

type ToolDirectoryEntry struct {
	ID            ToolDirectoryEntryID `json:"id"`
	Capability    CapabilityDescriptor `json:"capability"`
	Summary       string               `json:"summary"`
	WhenToUse     string               `json:"when_to_use"`
	WhenNotToUse  string               `json:"when_not_to_use,omitempty"`
	Effects       []ToolEffect         `json:"effects"`
	Risk          RiskLevel            `json:"risk"`
	RelevanceRank int                  `json:"relevance_rank"`
}

type DirectoryView struct {
	ViewID            string               `json:"view_id"`
	RunID             string               `json:"run_id"`
	WorkflowID        WorkflowID           `json:"workflow_id"`
	NodeID            WorkflowNodeID       `json:"node_id"`
	ActorRef          string               `json:"actor_ref"`
	ScopeRevision     int                  `json:"scope_revision"`
	DirectoryRevision string               `json:"directory_revision"`
	Entries           []ToolDirectoryEntry `json:"entries"`
}

type MaterializeRequest struct {
	ViewID        string                 `json:"view_id"`
	RunID         string                 `json:"run_id"`
	WorkflowID    WorkflowID             `json:"workflow_id"`
	NodeID        WorkflowNodeID         `json:"node_id"`
	ScopeRevision int                    `json:"scope_revision"`
	EntryIDs      []ToolDirectoryEntryID `json:"entry_ids"`
	ActorRef      string                 `json:"actor_ref"`
}

type ExposureView struct {
	ViewID            string           `json:"view_id"`
	RunID             string           `json:"run_id"`
	WorkflowID        WorkflowID       `json:"workflow_id"`
	NodeID            WorkflowNodeID   `json:"node_id"`
	ActorRef          string           `json:"actor_ref"`
	ScopeRevision     int              `json:"scope_revision"`
	DirectoryRevision string           `json:"directory_revision"`
	Definitions       []ToolDefinition `json:"definitions"`
}

type WorkflowNodeState struct {
	Status                WorkflowNodeStatus     `json:"status"`
	Attempts              int                    `json:"attempts"`
	CurrentScope          CapabilityScope        `json:"current_scope"`
	ScopeRevision         int                    `json:"scope_revision"`
	LastDirectory         *DirectoryViewRef      `json:"last_directory,omitempty"`
	SelectedEntries       []ToolDirectoryEntryID `json:"selected_entries,omitempty"`
	ToolCallIDs           []string               `json:"tool_call_ids,omitempty"`
	OutcomeRefs           []ResourceRef          `json:"outcome_refs,omitempty"`
	AppliedOutcomeIDs     []string               `json:"applied_outcome_ids,omitempty"`
	TransitionActivations map[TransitionID]int   `json:"transition_activations,omitempty"`
	LastAssessment        *NodeAssessment        `json:"last_assessment,omitempty"`
}

type WorkflowState struct {
	SchemaVersion int                                  `json:"schema_version"`
	Route         RouteDecision                        `json:"route"`
	ReturnRoute   ReturnRoute                          `json:"return_route"`
	Intent        IntentEnvelope                       `json:"intent"`
	Plan          WorkflowPlan                         `json:"plan"`
	PlanDigest    string                               `json:"plan_digest"`
	Status        WorkflowStatus                       `json:"status"`
	ActiveNodeIDs []WorkflowNodeID                     `json:"active_node_ids"`
	Nodes         map[WorkflowNodeID]WorkflowNodeState `json:"nodes"`
}

type ToolDirectoryMetadata struct {
	Summary      string       `json:"summary"`
	WhenToUse    string       `json:"when_to_use"`
	WhenNotToUse string       `json:"when_not_to_use,omitempty"`
	InputKinds   []TargetKind `json:"input_kinds,omitempty"`
	OutputKinds  []OutputKind `json:"output_kinds,omitempty"`
	Effects      []ToolEffect `json:"effects"`
}
