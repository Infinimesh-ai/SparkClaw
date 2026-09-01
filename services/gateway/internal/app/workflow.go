package app

const (
	CapabilityConversationAnswer    CapabilityID = "conversation.answer"
	CapabilityBrowserInternetSearch CapabilityID = "browser.internet_search"
	CapabilityBrowserWeather        CapabilityID = "browser.weather"
	CapabilityBrowserPageRead       CapabilityID = "browser.page_read"
	CapabilityBrowserInteraction    CapabilityID = "browser.interaction"
	CapabilityBrowserFormDraft      CapabilityID = "browser.form_draft"
	CapabilityDocumentRead          CapabilityID = "document.read"
	CapabilityDocumentEdit          CapabilityID = "document.edit"
	CapabilityLocalMindRead         CapabilityID = "localmind.read"
	CapabilityLocalMindWrite        CapabilityID = "localmind.write"
	CapabilityLocalMindQuery        CapabilityID = "localmind.query"
	CapabilityLocalMindCancel       CapabilityID = "localmind.cancel"
	CapabilityScheduleManage        CapabilityID = "schedule.manage"
	CapabilityCodingAgentManage     CapabilityID = "coding.agent_manage"
)

type IntentDomain string

const (
	IntentDomainConversation IntentDomain = "conversation"
	IntentDomainWeb          IntentDomain = "web"
	IntentDomainWorkspace    IntentDomain = "workspace"
	IntentDomainSchedule     IntentDomain = "schedule"
	IntentDomainCoding       IntentDomain = "coding"
)

type IntentOperation string

const (
	IntentOperationAnswer   IntentOperation = "answer"
	IntentOperationPublish  IntentOperation = "publish"
	IntentOperationSearch   IntentOperation = "search"
	IntentOperationRender   IntentOperation = "render"
	IntentOperationRead     IntentOperation = "read"
	IntentOperationDraft    IntentOperation = "draft"
	IntentOperationAutomate IntentOperation = "automate"
	IntentOperationProcess  IntentOperation = "process"
	IntentOperationCreate   IntentOperation = "create"
	IntentOperationEdit     IntentOperation = "edit"
	IntentOperationDelete   IntentOperation = "delete"
)

type TargetKind string

const (
	TargetKindNone              TargetKind = "none"
	TargetKindExplicitURL       TargetKind = "explicit_url"
	TargetKindBrowserCurrentTab TargetKind = "browser_current_tab"
	TargetKindPublicNamedTarget TargetKind = "public_named_target"
	TargetKindWorkspacePath     TargetKind = "workspace_path"
	TargetKindLocation          TargetKind = "location"
	TargetKindLocalMindTask     TargetKind = "localmind_task"
)

type OutputKind string

const (
	OutputKindText    OutputKind = "text"
	OutputKindMessage OutputKind = "message"
	OutputKindFile    OutputKind = "file"
	OutputKindImage   OutputKind = "image"
)

type DataScope string

const (
	DataScopePublic    DataScope = "public"
	DataScopeWorkspace DataScope = "workspace"
	DataScopeLocal     DataScope = "local"
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
type WorkflowResultProjection string
type WorkflowInvocationMode string

const (
	WorkflowConversationAnswer    WorkflowID = "conversation.answer"
	WorkflowBrowserInternetSearch WorkflowID = "browser.internet_search"
	WorkflowBrowserWeather        WorkflowID = "browser.weather"
	WorkflowBrowserAutomation     WorkflowID = "browser.automation"
	WorkflowBrowserPageRead       WorkflowID = "browser.page_read"
	WorkflowBrowserInteraction    WorkflowID = "browser.interaction"
	WorkflowBrowserFormDraft      WorkflowID = "browser.form_draft"
	WorkflowDocumentRead          WorkflowID = "document.read"
	WorkflowDocumentEdit          WorkflowID = "document.edit"
	WorkflowLocalMindRead         WorkflowID = "localmind.read"
	WorkflowLocalMindWrite        WorkflowID = "localmind.write"
	WorkflowLocalMindQuery        WorkflowID = "localmind.query"
	WorkflowLocalMindCancel       WorkflowID = "localmind.cancel"
	WorkflowScheduleManage        WorkflowID = "schedule.manage"
	WorkflowCodingAgentManage     WorkflowID = "coding.agent_manage"

	WorkflowBrowserSearch       = WorkflowBrowserInternetSearch
	WorkflowDocumentInformation = WorkflowDocumentRead
	WorkflowDocumentProcessing  = WorkflowDocumentEdit

	// Legacy identities remain exact so persisted state is never silently
	// reinterpreted as a different contract during migration.
	WorkflowLegacyBrowserSearch       WorkflowID = "browser.search"
	WorkflowLegacyDocumentInformation WorkflowID = "document.information"
	WorkflowLegacyDocumentProcessing  WorkflowID = "document.processing"
	WorkflowWebPublicResearch         WorkflowID = "web.public_research"
	WorkflowWebExplicitURL            WorkflowID = "web.explicit_url_read"
	WorkflowWorkspaceSearch           WorkflowID = "workspace.file_search"
	WorkflowWorkspaceRead             WorkflowID = "workspace.file_read"

	ToolEffectExternalRead     ToolEffect = "external.read"
	ToolEffectExternalInteract ToolEffect = "external.interact"
	ToolEffectLocalCompute     ToolEffect = "local.compute"
	ToolEffectLocalRead        ToolEffect = "local.read"
	ToolEffectLocalWrite       ToolEffect = "local.write"
	ToolEffectWorkspaceRead    ToolEffect = "workspace.read"
	ToolEffectWorkspaceWrite   ToolEffect = "workspace.write"

	ToolCapabilityWebDiscovery              = "web.discovery"
	ToolCapabilityInfoQuestion              = "info.question.read"
	ToolCapabilityWeatherRender             = "weather.card.render"
	ToolCapabilityMCPExternal               = "mcp.external"
	ToolCapabilityMCPApprovalResolve        = "mcp.approval.resolve"
	ToolCapabilityBrowserListTabs           = "browser.tab.list"
	ToolCapabilityBrowserFocus              = "browser.tab.focus"
	ToolCapabilityBrowserOpen               = "browser.tab.open"
	ToolCapabilityBrowserClose              = "browser.tab.close"
	ToolCapabilityBrowserHealth             = "browser.health.read"
	ToolCapabilityBrowserNavigate           = "browser.tab.navigate"
	ToolCapabilityBrowserSnapshot           = "browser.page.snapshot"
	ToolCapabilityBrowserPageRead           = "browser.page.read"
	ToolCapabilityBrowserPublicTarget       = "browser.public_target.identify"
	ToolCapabilityBrowserVisualInspect      = "browser.page.visual_inspect"
	ToolCapabilityBrowserWait               = "browser.page.wait"
	ToolCapabilityBrowserClick              = "browser.element.click"
	ToolCapabilityBrowserFormType           = "browser.form.type"
	ToolCapabilityBrowserFormSelect         = "browser.form.select"
	ToolCapabilityBrowserTransitionValidate = "browser.interaction.transition_validate"
	ToolCapabilityBrowserGoalAssess         = "browser.interaction.goal_assess"
	ToolCapabilityDocumentRead              = "document.read"
	ToolCapabilityDocumentEdit              = "document.edit"
	ToolCapabilityScheduleManage            = "schedule.manage"
	ToolCapabilityObservationRead           = "observation.read"
	ToolWorkspaceDataAccess                 = "workspace.data.access"
	ToolCapabilityExternalMCPWorkspace      = "external.mcp.workspace"
	ToolCapabilityLocalMindDelegateRead     = "localmind.task.delegate.read"
	ToolCapabilityLocalMindDelegateWrite    = "localmind.task.delegate.write"
	ToolCapabilityLocalMindTaskStatus       = "localmind.task.status"
	ToolCapabilityLocalMindTaskCancel       = "localmind.task.cancel"

	CapabilityQualifierFormat           = "format"
	CapabilityQualifierProvider         = "provider"
	CapabilityQualifierMode             = "mode"
	CapabilityQualifierEndpointID       = "endpoint_id"
	CapabilityQualifierSnapshotRevision = "snapshot_revision"

	CapabilityProviderInfo      = "info"
	CapabilityProviderLocalMind = "localmind"
	DocumentFormatText          = "text"
	DocumentFormatDOCX          = "docx"
	DocumentFormatXLSX          = "xlsx"
	DocumentFormatPPTX          = "pptx"
	DocumentFormatPDF           = "pdf"
	DocumentFormatImage         = "image"

	OutcomeAdapterGeneric             ToolOutcomeAdapter = "generic"
	OutcomeAdapterWebSearch           ToolOutcomeAdapter = "web.search"
	OutcomeAdapterWeatherPayload      ToolOutcomeAdapter = "weather.payload"
	OutcomeAdapterWeatherCard         ToolOutcomeAdapter = "weather.card"
	OutcomeAdapterWebPage             ToolOutcomeAdapter = "web.page"
	OutcomeAdapterWorkspaceSearch     ToolOutcomeAdapter = "workspace.search"
	OutcomeAdapterWorkspaceRead       ToolOutcomeAdapter = "workspace.read"
	OutcomeAdapterBrowserTabs         ToolOutcomeAdapter = "browser.tabs"
	OutcomeAdapterBrowserHealth       ToolOutcomeAdapter = "browser.health"
	OutcomeAdapterBrowserFocus        ToolOutcomeAdapter = "browser.focus"
	OutcomeAdapterBrowserOpen         ToolOutcomeAdapter = "browser.open"
	OutcomeAdapterBrowserClose        ToolOutcomeAdapter = "browser.close"
	OutcomeAdapterBrowserNavigate     ToolOutcomeAdapter = "browser.navigate"
	OutcomeAdapterBrowserSnapshot     ToolOutcomeAdapter = "browser.snapshot"
	OutcomeAdapterBrowserPublicTarget ToolOutcomeAdapter = "browser.public_target"
	OutcomeAdapterBrowserVisual       ToolOutcomeAdapter = "browser.visual"
	OutcomeAdapterBrowserWait         ToolOutcomeAdapter = "browser.wait"
	OutcomeAdapterBrowserClick        ToolOutcomeAdapter = "browser.click"
	OutcomeAdapterBrowserForm         ToolOutcomeAdapter = "browser.form"
	OutcomeAdapterBrowserTransition   ToolOutcomeAdapter = "browser.transition"
	OutcomeAdapterBrowserGoal         ToolOutcomeAdapter = "browser.goal"
	OutcomeAdapterDocumentEdit        ToolOutcomeAdapter = "document.edit"
	OutcomeAdapterScheduleList        ToolOutcomeAdapter = "schedule.list"
	OutcomeAdapterScheduleChange      ToolOutcomeAdapter = "schedule.change"
	OutcomeAdapterLocalMindTask       ToolOutcomeAdapter = "localmind.task"

	OutcomeSignalResultsAvailable                OutcomeSignal = "results_available"
	OutcomeSignalWeatherPayloadAvailable         OutcomeSignal = "weather_payload_available"
	OutcomeSignalWeatherCardAvailable            OutcomeSignal = "weather_card_available"
	OutcomeSignalNoResults                       OutcomeSignal = "no_results"
	OutcomeSignalContentAvailable                OutcomeSignal = "content_available"
	OutcomeSignalSourcePageAvailable             OutcomeSignal = "source_page_available"
	OutcomeSignalPublicTargetResolved            OutcomeSignal = "public_target_resolved"
	OutcomeSignalPublicTargetUnavailable         OutcomeSignal = "public_target_unavailable"
	OutcomeSignalStructureRequired               OutcomeSignal = "structure_required"
	OutcomeSignalAuthenticationRequired          OutcomeSignal = "authentication_required"
	OutcomeSignalTabsScanned                     OutcomeSignal = "tabs_scanned"
	OutcomeSignalTargetTabExists                 OutcomeSignal = "target_tab_exists"
	OutcomeSignalTargetTabMissing                OutcomeSignal = "target_tab_missing"
	OutcomeSignalFocusCompleted                  OutcomeSignal = "focus_completed"
	OutcomeSignalOpenCompleted                   OutcomeSignal = "open_completed"
	OutcomeSignalCloseCompleted                  OutcomeSignal = "close_completed"
	OutcomeSignalBrowserHealthy                  OutcomeSignal = "browser_healthy"
	OutcomeSignalBrowserUnavailable              OutcomeSignal = "browser_unavailable"
	OutcomeSignalTargetTabBlank                  OutcomeSignal = "target_tab_blank"
	OutcomeSignalNavigateCompleted               OutcomeSignal = "navigate_completed"
	OutcomeSignalSnapshotAvailable               OutcomeSignal = "snapshot_available"
	OutcomeSignalSnapshotTruncated               OutcomeSignal = "snapshot_truncated"
	OutcomeSignalSnapshotStale                   OutcomeSignal = "snapshot_stale"
	OutcomeSignalClickCompleted                  OutcomeSignal = "click_completed"
	OutcomeSignalWaitCompleted                   OutcomeSignal = "wait_completed"
	OutcomeSignalTargetSettled                   OutcomeSignal = "target_settled"
	OutcomeSignalHiddenTargetSettled             OutcomeSignal = "hidden_target_settled"
	OutcomeSignalActionTargetSettled             OutcomeSignal = "action_target_settled"
	OutcomeSignalVisibleTargetSettled            OutcomeSignal = "visible_target_settled"
	OutcomeSignalHiddenSnapshotDrifted           OutcomeSignal = "hidden_snapshot_drifted"
	OutcomeSignalActionSnapshotDrifted           OutcomeSignal = "action_snapshot_drifted"
	OutcomeSignalVisibleSnapshotDrifted          OutcomeSignal = "visible_snapshot_drifted"
	OutcomeSignalTargetValidated                 OutcomeSignal = "target_validated"
	OutcomeSignalPresentationOpened              OutcomeSignal = "presentation_opened"
	OutcomeSignalPresentationValidated           OutcomeSignal = "presentation_validated"
	OutcomeSignalInteractionProgress             OutcomeSignal = "interaction_progress"
	OutcomeSignalInteractionVerificationRequired OutcomeSignal = "interaction_verification_required"
	OutcomeSignalInteractionGoalSatisfied        OutcomeSignal = "interaction_goal_satisfied"
	OutcomeSignalInteractionLoopDetected         OutcomeSignal = "interaction_loop_detected"
	OutcomeSignalInteractionAttemptLimit         OutcomeSignal = "interaction_attempt_limit"
	OutcomeSignalInteractionVerificationFailed   OutcomeSignal = "interaction_verification_failed"
	OutcomeSignalUnsafeClickTarget               OutcomeSignal = "unsafe_click_target"
	OutcomeSignalDraftActionCompleted            OutcomeSignal = "draft_action_completed"
	OutcomeSignalDraftActionForbidden            OutcomeSignal = "draft_action_forbidden"
	OutcomeSignalVisualEvidenceAvailable         OutcomeSignal = "visual_evidence_available"
	OutcomeSignalVisualEvidenceStale             OutcomeSignal = "visual_evidence_stale"
	OutcomeSignalEditCompleted                   OutcomeSignal = "edit_completed"
	OutcomeSignalArtifactAvailable               OutcomeSignal = "artifact_available"
	OutcomeSignalSchedulesListed                 OutcomeSignal = "schedules_listed"
	OutcomeSignalScheduleTargetResolved          OutcomeSignal = "schedule_target_resolved"
	OutcomeSignalScheduleChanged                 OutcomeSignal = "schedule_changed"
	OutcomeSignalLocalMindTaskPending            OutcomeSignal = "localmind_task_pending"
	OutcomeSignalLocalMindTaskCompleted          OutcomeSignal = "localmind_task_completed"
	OutcomeSignalLocalMindTaskFailed             OutcomeSignal = "localmind_task_failed"
	OutcomeSignalLocalMindTaskCancelled          OutcomeSignal = "localmind_task_cancelled"

	AssessmentComplete          AssessmentStatus = "complete"
	AssessmentNeedsMoreEvidence AssessmentStatus = "needs_more_evidence"
	AssessmentBlocked           AssessmentStatus = "blocked"

	CompletionEvidence      CompletionRule = "evidence"
	CompletionModelAnswer   CompletionRule = "model_answer"
	CompletionMessage       CompletionRule = "message"
	CompletionDeterministic CompletionRule = "deterministic"
	CompletionDecision      CompletionRule = "decision"

	WorkflowStatusRunning   WorkflowStatus = "running"
	WorkflowStatusSucceeded WorkflowStatus = "succeeded"
	WorkflowStatusBlocked   WorkflowStatus = "blocked"

	WorkflowNodePending   WorkflowNodeStatus = "pending"
	WorkflowNodeActive    WorkflowNodeStatus = "active"
	WorkflowNodeSucceeded WorkflowNodeStatus = "succeeded"
	WorkflowNodeBlocked   WorkflowNodeStatus = "blocked"

	WorkflowInvocationDirectOnce WorkflowInvocationMode = "direct_once"

	ArgumentBindingIntentTarget ArgumentBindingSource = "intent_target"
	ArgumentBindingOutcomeRef   ArgumentBindingSource = "outcome_ref"
	ArgumentBindingRouteSlot    ArgumentBindingSource = "route_slot"
	ArgumentBindingRouteFact    ArgumentBindingSource = "route_fact"

	WorkflowResultTextAndOutputs WorkflowResultProjection = "text_and_outputs"
	WorkflowResultOutputsOnly    WorkflowResultProjection = "outputs_only"
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
	Requirements        []CapabilityRequirement `json:"requirements"`
	SupportRequirements []CapabilityRequirement `json:"support_requirements,omitempty"`
	DeniedEffects       []ToolEffect            `json:"denied_effects,omitempty"`
	MaterializeAll      bool                    `json:"materialize_all,omitempty"`
}

type StageCapabilityRule struct {
	Stage        string   `json:"stage"`
	Capabilities []string `json:"capabilities"`
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
	Deterministic  bool                    `json:"deterministic,omitempty"`
	NextStage      string                  `json:"next_stage"`
	Add            []CapabilityRequirement `json:"add"`
	Replace        *CapabilityScope        `json:"replace,omitempty"`
	MaxActivations int                     `json:"max_activations"`
}

type ArgumentBinding struct {
	Capability   string                `json:"capability"`
	Argument     string                `json:"argument"`
	ResourceKind string                `json:"resource_kind"`
	Source       ArgumentBindingSource `json:"source"`
	SourceKey    string                `json:"source_key,omitempty"`
	TargetKinds  []TargetKind          `json:"target_kinds,omitempty"`
}

type WorkflowNode struct {
	ID                WorkflowNodeID         `json:"id"`
	InitialStage      string                 `json:"initial_stage"`
	DependsOn         []WorkflowNodeID       `json:"depends_on,omitempty"`
	Goal              NodeGoal               `json:"goal"`
	InitialScope      CapabilityScope        `json:"initial_scope"`
	Transitions       []ScopeTransition      `json:"transitions,omitempty"`
	ArgumentBindings  []ArgumentBinding      `json:"argument_bindings,omitempty"`
	StageCapabilities []StageCapabilityRule  `json:"stage_capabilities,omitempty"`
	AllowedRisks      []RiskLevel            `json:"allowed_risks"`
	MaxAttempts       int                    `json:"max_attempts"`
	InvocationMode    WorkflowInvocationMode `json:"invocation_mode,omitempty"`
}

type WorkflowPlan struct {
	SchemaVersion    int                      `json:"schema_version"`
	ProfileID        WorkflowID               `json:"profile_id"`
	ProfileRevision  int                      `json:"profile_revision"`
	InitialNodeIDs   []WorkflowNodeID         `json:"initial_node_ids"`
	Nodes            []WorkflowNode           `json:"nodes"`
	Completion       CompletionRule           `json:"completion"`
	ResultProjection WorkflowResultProjection `json:"result_projection,omitempty"`
}

type ResourceRef struct {
	Kind       string            `json:"kind"`
	Ref        string            `json:"ref"`
	Provenance string            `json:"provenance"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

type ToolOutcome struct {
	ID         string          `json:"id"`
	ToolCallID string          `json:"tool_call_id"`
	Tool       string          `json:"tool"`
	NodeID     WorkflowNodeID  `json:"node_id"`
	Status     ToolCallStatus  `json:"status"`
	Signals    []OutcomeSignal `json:"signals,omitempty"`
	Refs       []ResourceRef   `json:"refs,omitempty"`
	Retryable  bool            `json:"retryable,omitempty"`
}

type NodeAssessment struct {
	OutcomeID    string           `json:"outcome_id"`
	NodeID       WorkflowNodeID   `json:"node_id"`
	Status       AssessmentStatus `json:"status"`
	Signals      []OutcomeSignal  `json:"signals,omitempty"`
	SelectedRefs []ResourceRef    `json:"selected_refs,omitempty"`
	ReasonCode   string           `json:"reason_code,omitempty"`
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
	Name          string               `json:"name"`
	Title         string               `json:"title,omitempty"`
	Description   string               `json:"description"`
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
	Stage                 string                 `json:"stage"`
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
	Browser       *BrowserWorkflowState                `json:"browser,omitempty"`
}

type ToolDirectoryMetadata struct {
	Summary      string       `json:"summary"`
	WhenToUse    string       `json:"when_to_use"`
	WhenNotToUse string       `json:"when_not_to_use,omitempty"`
	InputKinds   []TargetKind `json:"input_kinds,omitempty"`
	OutputKinds  []OutputKind `json:"output_kinds,omitempty"`
	Effects      []ToolEffect `json:"effects"`
}
