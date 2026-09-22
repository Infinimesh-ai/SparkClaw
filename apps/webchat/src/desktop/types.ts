export type DesktopPageRole = "personal" | "task";

export type DesktopPage = {
  page_ref: string;
  role: DesktopPageRole;
  task_id: string;
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
  presentation: {
    panel_bounds: { x: number; y: number; width: number; height: number };
    insufficient_space: boolean;
    presented_page_ref: string;
  };
};

export type SparkClawDesktop = {
  runtimeKind: "electron";
  capabilityVersion: 1;
  gatewayBase: string;
  speechBase: string;
  localConnection(): Promise<{ schema_version: 1; state: "connected" | "reconnecting" | "incomplete_setup" | "service_unavailable" | "invalid_authentication" | "identity_conflict" }>;
  retryLocalConnection(): Promise<{ schema_version: 1; state: "connected" | "reconnecting" | "incomplete_setup" | "service_unavailable" | "invalid_authentication" | "identity_conflict" }>;
  onLocalConnection(listener: (status: { schema_version: 1; state: "connected" | "reconnecting" | "incomplete_setup" | "service_unavailable" | "invalid_authentication" | "identity_conflict" }) => void): () => void;
  state(): Promise<DesktopState>;
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
