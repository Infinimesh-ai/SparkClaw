export type DesktopPageRole = "personal" | "task";

export type DesktopPage = {
  page_ref: string;
  role: DesktopPageRole;
  task_id: string;
  local_conversation_id?: string;
  title: string;
  url: string;
  presented: boolean;
  loading: boolean;
  crashed: boolean;
  can_go_back: boolean;
  can_go_forward: boolean;
};

export type DesktopState = {
  schema_version: 1;
  capability_version: 1;
  runtime_kind: "electron";
  runtime_generation: string;
  revision: number;
  pages: DesktopPage[];
  downloads: Array<{
    download_ref: string;
    source: "personal" | "workbench";
    page_ref: string;
    filename: string;
    state: "in_progress" | "complete" | "interrupted";
    received_bytes: number;
    total_bytes: number;
  }>;
  permissions: Array<{
    permission_ref: string;
    page_ref: string;
    origin: string;
    permission: string;
    media_types: string[];
  }>;
  browser_host?: {
    state: string;
    role?: "client_embedded";
    unknown_writes?: Array<{ command_id: string; digest: string; local_conversation_id: string; local_task_id: string }>;
  };
  presentation: {
    panel_bounds: { x: number; y: number; width: number; height: number };
    insufficient_space: boolean;
    presented_page_ref: string;
  };
};

export type DesktopBackendDescriptor = {
  schema_version: 1 | 2 | 3;
  transport?: "iscp";
  client_id?: string;
  domain_id?: string;
  initiator_device_id?: string;
  responder_device_id?: string;
  responder_key_thumbprint?: string;
  relay_url?: string;
  relay_profile?: "production" | "local-lab";
  test_mode?: true;
  origin: string;
  deployment_id: string;
  owner_id?: string;
  tls_certificate_sha256?: string;
  tls_ca_pem?: string;
};

export type DesktopConnectionStatus = {
  schema_version: 1;
  state: "connected" | "reconnecting" | "incomplete_setup" | "service_unavailable" | "invalid_authentication" | "identity_conflict" | "locked" | "secure_storage_unavailable";
  backend?: DesktopBackendDescriptor;
  test_mode?: true;
  transport_stage?: string;
  authorization_deletion?: { state: "pending" | "revoked"; operation_id: string };
  capabilities?: { operations: readonly string[]; files: boolean; mail: boolean; browser: boolean; speech: boolean; approvals: boolean; settings: boolean; notifications?: boolean; events?: boolean; surfaces?: Record<string,{ enabled: boolean; reason: string }> };
  client_id?: string;
  owner_id?: string;
};

export type SparkClawDesktop = {
  runtimeKind: "electron";
  capabilityVersion: 1;
  gatewayBase: string;
  speechBase: string;
  localConnection(): Promise<DesktopConnectionStatus>;
  retryLocalConnection(): Promise<DesktopConnectionStatus>;
  configureBackend?(descriptor: DesktopBackendDescriptor): Promise<DesktopConnectionStatus>;
  login?(token: string): Promise<DesktopConnectionStatus>;
  enroll?(credential: string): Promise<DesktopConnectionStatus>;
  connectionCredential?(token: string): Promise<string>;
  checkNewAuthorization?(): Promise<DesktopConnectionStatus>;
  deleteAuthorization?(): Promise<DesktopConnectionStatus>;
  logout?(): Promise<DesktopConnectionStatus>;
  speechStream?(request: Record<string, unknown>): Promise<unknown>;
  transcribeRecording?(request: { session_id: string; request_id: string; language: string; bytes: Uint8Array }): Promise<import("../api/types").SpeechTranscriptionResult>;
  loginStartup(enabled?: boolean): Promise<{ supported: boolean; enabled: boolean }>;
  onBackendEvents?(listener: (event: { categories: string[] }) => void): () => void;
  onLocalConnection(listener: (status: DesktopConnectionStatus) => void): () => void;
  state(): Promise<DesktopState>;
  selectConversation?(localConversationID: string): Promise<{ page_ref: string }>;
  grantBrowserHost?(): Promise<{ granted: boolean }>;
  reconcileBrowserHost?(commandID: string, digest: string, outcome: "observed_completed" | "observed_not_applied"): Promise<{ completed: true }>;
  createPersonal(url?: string): Promise<{ page_ref: string }>;
  navigatePersonal(pageRef: string, url: string): Promise<{ completed: true }>;
  personalNavigation(pageRef: string, action: "back" | "forward" | "reload"): Promise<{ completed: true }>;
  closePersonal(pageRef: string): Promise<{ completed: true }>;
  presentPersonal(pageRef: string): Promise<{ completed: true }>;
  observeTask(pageRef: string): Promise<{ completed: true }>;
  hideBrowser(): Promise<{ completed: true }>;
  setBounds(bounds: { x: number; y: number; width: number; height: number }, revision: number): Promise<{ completed: true }>;
  respondPermission(permissionRef: string, allow: boolean): Promise<{ completed: true }>;
  cancelDownload(downloadRef: string): Promise<{ completed: true }>;
  showDownload(downloadRef: string): Promise<{ completed: true }>;
  onState(listener: (state: DesktopState) => void): () => void;
};

declare global {
  interface Window {
    sparkclawDesktop?: SparkClawDesktop;
  }
}
