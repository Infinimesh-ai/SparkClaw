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
  created_at: string;
  resolved_at?: string;
  resolution_note?: string;
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

export type DeliveryCapabilities = {
  kinds: MessagePartKind[];
  dispositions: MessagePartDisposition[];
  file_fallback_kinds?: MessagePartKind[];
  native_voice_types?: string[];
  max_parts: number;
  max_total_bytes: number;
  max_bytes_by_kind?: Partial<Record<MessagePartKind, number>>;
  supports_caption: boolean;
  supports_native_voice: boolean;
  supports_file_fallback: boolean;
};

export type DeliveryEndpoint = {
  id: string;
  channel: string;
  software_display_name: string;
  account_display_name: string;
  conversation_label?: string;
  recipient: { id: string; display_name: string };
  capabilities: DeliveryCapabilities;
};

export type DeliveryPart = {
  id: string;
  kind: MessagePartKind;
  disposition: MessagePartDisposition;
  text?: string;
  artifact_id?: string;
  name?: string;
  content_type?: string;
  bytes?: number;
  caption?: string;
};

export type PartDeliveryReceipt = {
  part_id: string;
  status: "sent" | "failed" | "not_attempted";
  representation: "native" | "file_fallback";
  provider_ref?: string;
  error_code?: string;
};

export type DeliveryReceipt = {
  delivery_id: string;
  endpoint_id: string;
  status: DeliveryStatus;
  provider_ref?: string;
  error?: string;
  error_code?: string;
  retry_state?: string;
  attempt: number;
  part_receipts?: PartDeliveryReceipt[];
  attempted_at: string;
  delivered_at?: string;
};

export type DeliveryStatus = "draft" | "target_resolved" | "awaiting_send_approval" | "approved" | "sending" | "pending" | "sent" | "partially_sent" | "failed" | "outcome_unknown";

export type MessageDelivery = {
  id: string;
  direction: "send";
  origin: "web_direct" | "agent_workflow" | "source_reply" | "schedule";
  status: DeliveryStatus;
  target: string;
  software_display_name: string;
  recipient_display_name: string;
  account_display_name: string;
  content: { parts: DeliveryPart[] };
  receipt?: DeliveryReceipt;
  attempts: number;
  created_at: string;
  updated_at: string;
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

export type SessionEvent = {
  id: string;
  time: string;
  type: string;
  session_id?: string;
  run_id?: string;
  payload?: unknown;
};

export type ApprovalResolution = {
  approval: Approval;
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
  version: number;
  updated_at?: string;
};

export type PublicModelProfile = {
  name: string;
  base_url: string;
  model: string;
  context_tokens: number;
  mtp: boolean;
  max_tokens?: number;
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
