export type RiskLevel = "read" | "draft" | "reversible" | "dangerous";

export type Session = {
  id: string;
  title: string;
  source?: string;
  hidden?: boolean;
  created_at: string;
  updated_at: string;
};

export type Schedule = {
  id: string;
  session_id?: string;
  title: string;
  text: string;
  due_time: string;
  timezone: string;
  recurrence?: string;
  status: "pending" | "sending";
  updated_at: string;
  editable: boolean;
  cancelable: boolean;
  endpoint: {
    kind?: "web" | "third_party_device";
    channel?: string;
    software_display_name?: string;
    account_display_name?: string;
    recipient_display_name?: string;
    conversation_label?: string;
    status: "active" | "unavailable" | "not_applicable";
  };
};

export type ScheduleAction = {
  operation: "edit" | "delete";
  schedule_id: string;
  expected_updated_at: string;
  text?: string;
  due_time?: string;
  timezone?: string;
  recurrence?: string;
};

export type Message = {
  id: string;
  session_id: string;
  role: "user" | "assistant";
  content: string;
  created_at: string;
  run_id?: string;
  attachments?: MessageAttachment[];
  requested_media?: MessageMediaLocator[];
};

export type MessageMediaLocator = {
  path?: string;
  name?: string;
  query?: string;
  caption?: string;
};

export type RunFeedback = {
  id: string;
  session_id: string;
  run_id: string;
  message_id?: string;
  rating: "up" | "down" | "corrected";
  note?: string;
  correction?: string;
  created_at: string;
  updated_at: string;
};

export type ToolCall = {
  id: string;
  session_id: string;
  run_id: string;
  tool: string;
  risk: RiskLevel;
  status: string;
  arguments: Record<string, unknown>;
  result?: unknown;
  error?: string;
  approval_id?: string;
  observation_ref?: string;
  observation_summary?: string;
  started_at: string;
  completed_at?: string;
};

export type Approval = {
  id: string;
  source: "tool" | "happy_team_plan" | string;
  external_id?: string;
  external_context?: {
    provider: string;
    title: string;
    goal_prompt: string;
    plan?: string;
    plan_availability: "available" | "temporarily_unavailable" | string;
    plan_edited?: boolean;
  };
  session_id: string;
  run_id: string;
  tool_call_id: string;
  tool: string;
  risk: RiskLevel;
  status: string;
  summary: string;
  reason: string;
  resources: string[];
  arguments: Record<string, unknown>;
  policy_context?: Record<string, unknown>;
  presentation?: ApprovalPresentation;
  created_at: string;
  resolved_at?: string;
  resolution_note?: string;
};

export type ApprovalPresentation = {
  kind: "external_mcp_workspace_data_access" | string;
  session_id: string;
  requester: string;
  locators?: MessageMediaLocator[];
  locator_status?: "unverified" | string;
  access_class?: string;
  output_class?: string;
  return_route: {
    mode?: string;
    source_endpoint_id?: string;
    endpoint_id?: string;
  };
  scope: "single_operation" | string;
};

export type MemoryCandidate = {
  id: string;
  session_id: string;
  run_id: string;
  kind: string;
  content: string;
  sensitivity: string;
  status: string;
  reason: string;
  created_at: string;
  resolved_at?: string;
};

export type Memory = {
  id: string;
  kind: string;
  content: string;
  source_run_id: string;
  created_at: string;
};

export type ResourceRef = {
  kind: string;
  ref: string;
  provenance: string;
  attributes?: Record<string, string>;
};

export type WorkflowMessagePart = {
  id: string;
  kind: MessagePartKind;
  disposition: MessagePartDisposition;
  text?: string;
  artifact_id?: string;
  resource?: ResourceRef;
  name?: string;
  content_type?: string;
  bytes?: number;
  width?: number;
  height?: number;
  sha256?: string;
  caption?: string;
  derived_from_part_id?: string;
};

export type WorkflowResult = {
  schema_version: number;
  id: string;
  run_id: string;
  owner_id: string;
  authorization: { principal_id: string; scope?: string[] };
  status: "succeeded" | "waiting" | "blocked" | "failed";
  capability_path: string[];
  workflow: { id: string; revision: number };
  data?: Record<string, unknown>;
  content: { parts: WorkflowMessagePart[] };
  references?: ResourceRef[];
  return_route: { mode: "source" | "endpoint" | "none"; source_endpoint_id?: string; endpoint_id?: string };
  resume?: { kind: string; token?: string; data?: Record<string, unknown> };
  error?: { code: string; message: string; retryable?: boolean };
};

export type AgentResult = {
  run: {
    id: string;
    session_id: string;
    state: string;
    model_lane: string;
    risk: RiskLevel;
    started_at: string;
    completed_at?: string;
    summary?: string;
  };
  message: Message;
  tool_calls: ToolCall[];
  approvals: Approval[];
  workflow_result?: WorkflowResult | null;
};

export type MessageAttachment = {
  artifact_id?: string;
  name: string;
  rel_path: string;
  uri?: string;
  content_type?: string;
  bytes?: number;
  width?: number;
  height?: number;
  sha256?: string;
  source?: string;
  caption?: string;
  understanding_summary?: string;
};

export type MessagePartKind = "text" | "image" | "audio" | "file";
export type MessagePartDisposition = "inline" | "attachment" | "voice_note";

export type DeliveryEndpoint = {
  id: string;
  channel: string;
  software_display_name: string;
  account_display_name: string;
  conversation_label?: string;
  recipient: { id: string; display_name: string };
};

export type ModelStreamEvent = {
  type: string;
  session_id?: string;
  run_id?: string;
  message_id?: string;
  span_id?: string;
  text?: string;
  tool_call_id?: string;
  tool_name?: string;
  arguments_delta?: string;
  arguments?: unknown;
  error?: string;
};

export type ApprovalResolution = {
  approval: Approval;
  approval_status: string;
  execution_status: string;
  execution_error?: string;
  tool_call?: ToolCall | null;
  workflow_result?: WorkflowResult | null;
};

export type ReadyStatus = {
  ok: boolean;
  workspace_root: string;
  trace_dir: string;
  state_backend: string;
  state_path: string;
  state_dsn?: string;
  auth_required?: boolean;
  rate_limit?: {
    enabled: boolean;
    requests_per_minute: number;
    burst: number;
  };
  model_mode: string;
  gateway_binding: string;
  speech: SpeechStatus;
  resident_services: ResidentServiceStatus[];
  store?: {
    backend: "memory" | "file" | "postgres" | string;
    state: "starting" | "ready" | "unready" | "closing" | "closed" | string;
    ready: boolean;
    durable: boolean;
    reason_code?: string;
    active_operations: number;
    started_at: string;
    last_probe_at?: string;
    degraded_at?: string;
    last_recovered_at?: string;
  };
};

export type ResidentServiceStatus = {
  lane: "fast" | "embedding" | "guard" | "asr" | "ocr" | string;
  backend: string;
  model: string;
  readiness: string;
  last_call_status?: string;
};

export type SpeechStatus = {
  enabled: boolean;
  ready: boolean;
  state: "disabled" | "unavailable" | "warming" | "ready" | "busy" | string;
  backend: string;
  model: string;
  supports_streaming: boolean;
  accepted_content_types: string[];
  max_audio_seconds: number;
  max_upload_bytes: number;
  reason?: string;
  realtime?: SpeechRealtimeCapabilities;
};

export type SpeechRealtimeCapabilities = {
  protocol: string;
  sample_rate: number;
  channels: number;
  bits_per_sample: number;
  frame_ms: number;
};

export type SpeechRealtimeFormat = {
  sample_rate: number;
  channels: number;
  bits_per_sample: number;
  frame_ms: number;
};

export type SpeechRealtimeLimits = {
  max_audio_seconds: number;
  max_frame_samples: number;
};

export type SpeechRealtimeTicket = {
  id: string;
  url: string;
  expires_at: string;
  protocol: string;
  format: SpeechRealtimeFormat;
  limits: SpeechRealtimeLimits;
};

export type SpeechRealtimeEvent = {
  event: "ready" | "ack" | "partial" | "final" | "fallback" | "error";
  protocol?: string;
  format?: SpeechRealtimeFormat;
  limits?: SpeechRealtimeLimits;
  accepted_sequence?: number;
  received_audio_ms?: number;
  revision?: number;
  text?: string;
  language?: string;
  audio_end_ms?: number;
  duration_ms?: number;
  inference_ms?: number;
  stop_reason?: string;
  model?: string;
  code?: string;
  retryable?: boolean;
};

export type SpeechTranscriptionResult = {
  id: string;
  request_id: string;
  session_id: string;
  text: string;
  language?: string;
  duration_ms: number;
  inference_ms: number;
  model?: string;
  audio_retained: false;
};

export type OwnerProfile = {
  id: string;
  display_name: string;
  email?: string;
  preferences?: Record<string, string>;
  created_at: string;
  updated_at: string;
};

export type Client = {
  id: string;
  name: string;
  created_at: string;
  last_seen_at?: string;
  revoked_at?: string;
};

export type NotificationBinding = {
  id: string;
  owner_id: string;
  channel: string;
  provider: string;
  status: "waiting_scan" | "waiting_confirm" | "active" | "expired" | "revoked" | "failed" | string;
  display_name?: string;
  external_user_id?: string;
  account_id?: string;
  credential_ref?: string;
  context_token?: string;
  base_url?: string;
  qr_code_url?: string;
  qr_code_image?: string;
  default_for_channel: boolean;
  scopes: string[];
  created_at: string;
  updated_at: string;
  expires_at?: string | null;
  revoked_at?: string | null;
  last_error?: string;
};

export type PassiveNotification = {
  id: string;
  notification_id: string;
  source: "localmind" | string;
  kind: "document_mention" | "comment_mention" | string;
  deep_link: string;
  occurred_at: string;
  read_at?: string;
  created_at: string;
  updated_at: string;
};

export type ConnectorStatus = {
  channel: string;
  provider: string;
  setup_kind: "qr" | "secret" | string;
  available: boolean;
  enabled: boolean;
  running: boolean;
  state: "disabled" | "unavailable" | "starting" | "setup_required" | "setup_pending" | "active" | "error" | string;
  binding_status: string;
  binding_startable: boolean;
  supports_multiple_bindings: boolean;
  disabled_reason?: string;
  last_error?: string;
  iscp_enabled?: boolean;
  lan_access_enabled?: boolean;
  version: number;
  updated_at?: string;
};

export type ISCPPairingStatus = {
  enabled: boolean;
  ready: boolean;
  state: "disabled" | "unavailable" | "ready" | string;
  domain_id?: string;
  authority_host?: string;
  expected_ticket_type: string;
  disabled_reason?: string;
};

export type ISCPOnboarding = {
  schema_version: number;
  id: string;
  owner_id: string;
  actor_id: string;
  display_name: string;
  domain_id: string;
  authority_ref: string;
  ticket_id: string;
  ticket_type: string;
  relay_id: string;
  trust_root_id: string;
  max_uses: number;
  status: string;
  ticket_issued_at: string;
  ticket_expires_at: string;
  created_at: string;
  updated_at: string;
};

export type ISCPPairingTicket = {
  type: string;
  ticket_id: string;
  domain_id: string;
  relay_id: string;
  trust_root_id: string;
  max_uses: number;
  issued_at: string;
  expires_at: string;
  signature: { alg: string; kid: string; value: string };
};

export type IssuedISCPPairing = {
  onboarding: ISCPOnboarding;
  ticket: ISCPPairingTicket;
};

export type MCPAccessCatalog = {
  scope: "conversation";
  business_tool: "sparkclaw.conversation.send";
  iscp_enabled: boolean;
  lan_access_enabled: boolean;
  transport_version: number;
  domain_id?: string;
  endpoint_path: string;
};

export type MCPAccessTicket = {
  schema_version: number;
  id: string;
  owner_id: string;
  actor_id: string;
  domain_id: string;
  authorization_revision: number;
  scope: "conversation";
  status: string;
  max_uses: number;
  use_count: number;
  issued_at: string;
  expires_at: string;
  consumed_at?: string;
  revoked_at?: string;
};

export type IssuedMCPAccessTicket = {
  ticket: MCPAccessTicket;
  secret: string;
};

export type MCPBinding = {
  schema_version: number;
  id: string;
  owner_id: string;
  actor_id: string;
  domain_id: string;
  requester_device_id: string;
  requester_key_thumbprint: string;
  authorization_revision: number;
  scope: "conversation";
  status: string;
  linked_session_id: string;
  latest_iscp_session_id?: string;
  created_at: string;
  updated_at: string;
  last_used_at?: string;
  revoked_at?: string;
};

export type MCPAccessRecordDeletion = {
  deleted_tickets: number;
  deleted_bindings: number;
};

export type IntegrationID = "infinimesh-info" | "localmind";

export type EmailProviderStatus = {
  provider: "qq_mail" | "outlook" | "gmail";
  display_name: string;
  enabled: boolean;
  default: boolean;
  account: "default";
  account_hint?: string;
  state: string;
  last_checked_at?: string;
  error_code?: string;
  version: number;
  updated_at?: string;
};

export type BrowserExtensionStatus = {
  configured: boolean;
  state: "not_configured" | "checking" | "ready" | "needs_attention" | "temporarily_unavailable" | "vault_unavailable" | string;
  profile_id: "default" | string;
  credential_generation: number;
  controller_generation?: number;
  session_generation?: number;
  page_generation?: number;
  last_validated_at?: string;
  error_code?: string;
  versions: {
    client?: string;
    client_version?: string;
    playwright_version?: string;
    browser_channel?: string;
  };
};

export type IntegrationCredential = {
  id: string;
  label: string;
  validated_at: string;
  last_checked_at?: string;
  state: string;
  error_code?: string;
  active: boolean;
};

export type IntegrationStatus = {
  id: IntegrationID;
  category: "data_provider" | "outbound_mcp";
  configured: boolean;
  source: "household" | "operator" | "none";
  state: string;
  editable: boolean;
  checkable: boolean;
  operator_available: boolean;
  active_credential_id?: string;
  credentials: IntegrationCredential[];
  last_checked_at?: string;
  error_code?: string;
};

export type PublicModelProfile = {
  name: string;
  base_url: string;
  model: string;
  capacity_physical_model: string;
  context_tokens: number;
  output_budgets: Record<string, number>;
  mtp: boolean;
};

export type PublicConfig = {
  gateway: {
    bind: string;
    port: number;
    pairing_required: boolean;
    remote_access: string;
    api_token?: string;
    rate_limit: {
      enabled: boolean;
      requests_per_minute: number;
      burst: number;
    };
  };
  model: {
    capacity_profile: string;
    mock: boolean;
    http_timeout_seconds?: number;
    disable_thinking?: boolean;
    fast: PublicModelProfile;
    deep: PublicModelProfile;
    embedding: PublicModelProfile;
    guard: PublicModelProfile;
  };
  speech: {
    enabled: boolean;
    backend: string;
    model: string;
    default_language: string;
    max_audio_seconds: number;
    max_upload_bytes: number;
    retain_audio: false;
  };
  iscp_pairing: ISCPPairingStatus;
  workspaces: {
    default_root: string;
    allowlist: string[];
  };
  security: {
    external_content_untrusted: boolean;
    approval_required_for_dangerous_tools: boolean;
    sandbox_required_for_mutating_tools: boolean;
    dangerous_tools_require_deep_verification: boolean;
    denied_tools: string[];
    approval_required_tools: string[];
    tool_policy_path: string;
    browser_read_allow_hosts: string[];
  };
  sandbox: {
    enabled: boolean;
    backend: string;
    runner_url: string;
    image: string;
    network: string;
    workspace_access: string;
    host_access: string;
  };
  storage: {
    trace_dir: string;
    log_dir: string;
    artifact_backend: string;
    artifact_dir: string;
    artifact_bucket: string;
    s3_endpoint: string;
    s3_region: string;
    s3_access_key: string;
    s3_secret_key: string;
  };
  state: {
    backend: string;
    path: string;
    dsn: string;
    encrypt_at_rest: boolean;
    encryption_key: string;
    encryption_key_file: string;
  };
  memory: {
    enabled: boolean;
    write_policy: string;
    allow_sensitive_memory: boolean;
    retention_days: number;
    redact_patterns: string[];
  };
  runtime: {
    observation_summary_max_bytes: number;
  };
  tools: {
    notifications: {
      channels: Record<string, {
        enabled: boolean;
        provider: string;
        base_url: string;
        token_configured: boolean;
        recipient_set: boolean;
        available?: boolean;
        operator_enabled?: boolean;
        binding_status?: string;
        startable?: boolean;
        disabled_reason?: string;
      }>;
    };
    reminders: {
      enabled: boolean;
      default_channel: string;
    };
  };
  tool_policy: {
    policy_path: string;
    external_content_untrusted: boolean;
    approval_required_for_dangerous_tools: boolean;
    sandbox_required_for_mutating_tools: boolean;
    dangerous_tools_deep_verification: boolean;
    definition_count: number;
    risk_counts: Record<string, number>;
    definition_approval_required_tools: string[];
    configured_approval_required_tools: string[];
    denied_tools: string[];
    browser_read_allow_hosts: string[];
  };
};

export type EvalCase = {
  name: string;
  status: string;
  message: string;
  duration_ms: number;
};

export type EvalRun = {
  id: string;
  profile: string;
  status: string;
  summary: string;
  cases: EvalCase[];
  failure_archives?: Array<{
    case_name: string;
    uri: string;
    path?: string;
    key?: string;
    backend: string;
    content_type: string;
    bytes: number;
  }>;
  started_at: string;
  completed_at?: string;
};

export type ArtifactObject = {
  id: string;
  kind: string;
  run_id?: string;
  eval_id?: string;
  session_id?: string;
  backend: string;
  bucket?: string;
  key: string;
  uri: string;
  path?: string;
  content_type: string;
  bytes: number;
  created_at: string;
};

export type DocumentUploadResult = {
  artifact: ArtifactObject;
  path: string;
  rel_path: string;
  bytes: number;
  media?: {
    rel_path?: string;
    content_type?: string;
    sha256?: string;
    width?: number;
    height?: number;
  };
};

export type EpisodeSummary = {
  id: string;
  session_id: string;
  run_id: string;
  goal: string;
  outcome: string;
  risk: RiskLevel;
  model_lane: string;
  tools: string[];
  approvals: string[];
  failures?: string[];
  repair_performed: boolean;
  summary: string;
  created_at: string;
};

export type MemoryExport = {
  generated_at: string;
  owner_profile: OwnerProfile;
  memories: Memory[];
  memory_candidates: MemoryCandidate[];
  episodes: EpisodeSummary[];
  counts: {
    memories: number;
    memory_candidates: number;
    pending_candidates: number;
    episodes: number;
  };
};

export type MemoryExportArchive = {
  export: MemoryExport;
  artifact: ArtifactObject;
};

export type ModelCall = {
  id: string;
  session_id?: string;
  run_id?: string;
  lane: string;
  profile: string;
  model: string;
  operation: string;
  mock: boolean;
  fallback?: boolean;
  status: string;
  prompt_tokens: number;
  response_tokens: number;
  total_tokens: number;
  latency_ms: number;
  error?: string;
  started_at: string;
  completed_at?: string;
};

export type AuditEvent = {
  id: string;
  time: string;
  type: string;
  session_id?: string;
  run_id?: string;
  actor: string;
  summary: string;
  fields?: Record<string, unknown>;
};

export type RunTrace = {
  run: AgentResult["run"];
  model: {
    lane: string;
    profile: string;
    model: string;
    content: string;
    mock: boolean;
    fallback?: boolean;
    error_note?: string;
  };
  model_calls?: ModelCall[];
  messages: Message[];
  tool_calls: ToolCall[];
  approvals: Approval[];
  feedback?: RunFeedback[];
  audit: AuditEvent[];
  episode?: EpisodeSummary;
};

export type TraceMetadata = {
  run_id: string;
  session_id: string;
  state: string;
  risk: RiskLevel;
  model_lane: string;
  summary?: string;
  started_at: string;
  completed_at?: string;
  message_count: number;
  tool_call_count: number;
  approval_count: number;
  model_call_count: number;
  artifact_uri?: string;
  artifact_path?: string;
};
