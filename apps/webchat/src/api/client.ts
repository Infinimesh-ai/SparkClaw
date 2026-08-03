import type {
  AgentResult,
  ArtifactObject,
  AuditEvent,
  Approval,
  ApprovalResolution,
  Client,
  ConnectorStatus,
  DocumentUploadResult,
  DeliveryEndpoint,
  DeliveryPart,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  MemoryExportArchive,
  Message,
  MessageDelivery,
  MessageAttachment,
  ModelStreamEvent,
  ModelCall,
  NotificationBinding,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunFeedback,
  RunTrace,
  Schedule,
  ScheduleAction,
  Session,
  SpeechStatus,
  SpeechTranscriptionResult,
  TraceMetadata,
  ToolCall
} from "./types";

const API_BASE = import.meta.env.VITE_SPARKCLAW_API_BASE ?? "";
const TOKEN_STORAGE_KEY = "sparkclaw.api_token";

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly retryable: boolean;
  readonly details: unknown;

  constructor(status: number, message: string, code = "", retryable = false, details?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryable = retryable;
    this.details = details;
  }
}

export function apiToken() {
  return import.meta.env.VITE_SPARKCLAW_API_TOKEN ?? window.localStorage.getItem(TOKEN_STORAGE_KEY) ?? "";
}

export function saveAPIToken(token: string) {
  window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
}

export function clearAPIToken() {
  window.localStorage.removeItem(TOKEN_STORAGE_KEY);
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const headers: Record<string, string> = {
    ...(apiToken() ? { Authorization: `Bearer ${apiToken()}` } : {}),
    ...((init?.headers as Record<string, string> | undefined) ?? {})
  };
  if (!(init?.body instanceof FormData)) {
    headers["Content-Type"] = "application/json";
  }
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({})) as { error?: unknown; code?: unknown; retryable?: unknown };
    throw new APIError(
      response.status,
      typeof body.error === "string" ? body.error : `HTTP ${response.status}`,
      typeof body.code === "string" ? body.code : "",
      body.retryable === true,
      body
    );
  }
  return response.json() as Promise<T>;
}

type SendMessageStreamHandlers = {
  signal?: AbortSignal;
  onEvent?: (event: string, data: unknown) => void;
  onTextDelta?: (text: string, event: ModelStreamEvent) => void;
  onFinal?: (result: AgentResult) => void;
  onError?: (error: Error) => void;
};

async function requestEventStream(path: string, init: RequestInit, onBlock: (event: string, data: string) => void) {
  const headers: Record<string, string> = {
    ...(apiToken() ? { Authorization: `Bearer ${apiToken()}` } : {}),
    Accept: "text/event-stream",
    ...((init.headers as Record<string, string> | undefined) ?? {})
  };
  if (!(init.body instanceof FormData)) {
    headers["Content-Type"] = "application/json";
  }
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error ?? `HTTP ${response.status}`);
  }
  if (!response.body) {
    throw new Error("Streaming response body is unavailable");
  }
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  const flushBlock = (block: string) => {
    const lines = block.split(/\r?\n/);
    let event = "message";
    const dataLines: string[] = [];
    for (const line of lines) {
      if (line.startsWith("event:")) {
        event = line.slice("event:".length).trim();
      } else if (line.startsWith("data:")) {
        dataLines.push(line.slice("data:".length).trimStart());
      }
    }
    if (dataLines.length > 0) {
      onBlock(event, dataLines.join("\n"));
    }
  };
  while (true) {
    const { value, done } = await reader.read();
    buffer += decoder.decode(value ?? new Uint8Array(), { stream: !done });
    let boundary = buffer.search(/\r?\n\r?\n/);
    while (boundary >= 0) {
      const block = buffer.slice(0, boundary);
      const match = buffer.match(/\r?\n\r?\n/);
      buffer = buffer.slice(boundary + (match?.[0].length ?? 2));
      if (block.trim()) flushBlock(block);
      boundary = buffer.search(/\r?\n\r?\n/);
    }
    if (done) break;
  }
  if (buffer.trim()) {
    flushBlock(buffer);
  }
}

export function sessionEventsURL(sessionId: string) {
  const path = `/api/sessions/${sessionId}/events/stream`;
  if (!API_BASE) return path;
  const url = new URL(path, window.location.origin);
  const base = new URL(API_BASE, window.location.origin);
  url.protocol = base.protocol;
  url.host = base.host;
  return url.toString();
}

export function workspaceScreenshotURL(path: string) {
  const name = path.split(/[\\/]/).pop() ?? "";
  const route = `/api/workspace/screenshots/${encodeURIComponent(name)}`;
  if (!API_BASE) return route;
  return new URL(route, new URL(API_BASE, window.location.origin)).toString();
}

export function documentFileURL(path: string, sessionId = "") {
  const params = new URLSearchParams({ path });
  if (sessionId) params.set("session_id", sessionId);
  const route = `/api/documents/file?${params.toString()}`;
  if (!API_BASE) return route;
  return new URL(route, new URL(API_BASE, window.location.origin)).toString();
}

// Shared fetch for binary endpoints (documents, screenshots) that need the
// same bearer-token handling as JSON requests.
export async function fetchAuthedBlob(url: string, signal?: AbortSignal) {
  const token = apiToken();
  const response = await fetch(url, {
    signal,
    headers: token ? { Authorization: `Bearer ${token}` } : undefined
  });
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  return response.blob();
}

export function fetchDocumentFile(path: string, sessionId = "", signal?: AbortSignal) {
  return fetchAuthedBlob(documentFileURL(path, sessionId), signal);
}

export async function openDocumentFile(path: string, sessionId = "") {
  const target = window.open("", "_blank");
  try {
    const blob = await fetchDocumentFile(path, sessionId);
    const objectURL = URL.createObjectURL(blob);
    if (target) {
      target.opener = null;
      target.location.href = objectURL;
    } else {
      const link = document.createElement("a");
      link.href = objectURL;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.click();
    }
    window.setTimeout(() => URL.revokeObjectURL(objectURL), 60_000);
  } catch (error) {
    target?.close();
    throw error;
  }
}

export const api = {
  ready: () => request<ReadyStatus>("/readyz"),
  speechStatus: () => request<SpeechStatus>("/api/speech/status"),
  transcribeSpeech: (sessionId: string, requestId: string, language: string, file: Blob, signal?: AbortSignal) => {
    const form = new FormData();
    form.append("file", file, "recording.wav");
    form.append("session_id", sessionId);
    form.append("request_id", requestId);
    form.append("language", language || "auto");
    return request<SpeechTranscriptionResult>("/api/speech/transcriptions", {
      method: "POST",
      body: form,
      signal
    });
  },
  config: () => request<PublicConfig>("/api/config"),
  owner: () => request<OwnerProfile>("/api/owner"),
  updateOwner: (displayName: string, email: string, preferences: Record<string, string>) =>
    request<OwnerProfile>("/api/owner", {
      method: "POST",
      body: JSON.stringify({ display_name: displayName, email, preferences })
    }),
  clients: () => request<{ clients: Client[] }>("/api/clients"),
  revokeClient: (id: string) => request<Client>(`/api/clients/${id}/revoke`, { method: "POST", body: "{}" }),
  notificationBindings: (channel = "", status = "") => {
    const params = new URLSearchParams();
    if (channel) params.set("channel", channel);
    if (status) params.set("status", status);
    const query = params.toString();
    return request<{ bindings: NotificationBinding[] }>(`/api/notification-bindings${query ? `?${query}` : ""}`);
  },
  connectors: () => request<{ connectors: ConnectorStatus[] }>("/api/connectors"),
  updateConnector: (channel: string, enabled: boolean, expectedVersion: number) =>
    request<ConnectorStatus>(`/api/connectors/${encodeURIComponent(channel)}`, {
      method: "PATCH",
      body: JSON.stringify({ enabled, expected_version: expectedVersion })
    }),
  startNotificationBinding: (channel = "weixin", botToken = "") =>
    request<NotificationBinding>(`/api/notification-bindings/${channel}/start`, {
      method: "POST",
      body: JSON.stringify({ default_for_channel: false, credential_secret: botToken })
    }),
  notificationBinding: (id: string) => request<NotificationBinding>(`/api/notification-bindings/${id}`),
  revokeNotificationBinding: (id: string) => request<NotificationBinding>(`/api/notification-bindings/${id}`, { method: "DELETE" }),
  deliveryEndpoints: () => request<{ endpoints: DeliveryEndpoint[] }>("/api/delivery-endpoints"),
  deliveries: () => request<{ deliveries: MessageDelivery[] }>("/api/deliveries"),
  createDelivery: (target: string, idempotencyKey: string, parts: DeliveryPart[]) =>
    request<MessageDelivery>("/api/deliveries", {
      method: "POST",
      body: JSON.stringify({ target, idempotency_key: idempotencyKey, confirmed: true, content: { parts } })
    }),
  retryDelivery: (id: string) => request<MessageDelivery>(`/api/deliveries/${id}/retry`, { method: "POST", body: JSON.stringify({ confirmed: true }) }),
  schedules: () => request<{ schedules: Schedule[] }>("/api/schedules"),
  scheduleAction: (sessionId: string, content: string, action: ScheduleAction) =>
    request<AgentResult>(`/api/sessions/${sessionId}/messages`, {
      method: "POST",
      body: JSON.stringify({ content, schedule_action: action })
    }),
  updateToolPolicy: (deny: string[], approvalRequired: string[]) =>
    request<PublicConfig["tool_policy"]>("/api/tool-policy", {
      method: "POST",
      body: JSON.stringify({ deny, approval_required: approvalRequired })
    }),
  startPairing: () => request<{ pairing_id: string; code: string; expires_at: string }>("/api/pairing/start", { method: "POST", body: "{}" }),
  claimPairing: (pairingId: string, code: string, clientName = "WebChat") =>
    request<{ client: { id: string; name: string; created_at: string }; token: string }>("/api/pairing/claim", {
      method: "POST",
      body: JSON.stringify({ pairing_id: pairingId, code, client_name: clientName })
    }),
  sessions: () => request<{ sessions: Session[] }>("/api/sessions"),
  createSession: (title = "") =>
    request<Session>("/api/sessions", { method: "POST", body: JSON.stringify({ title }) }),
  updateSession: (sessionId: string, title: string) =>
    request<Session>(`/api/sessions/${sessionId}`, { method: "PATCH", body: JSON.stringify({ title }) }),
  deleteSession: (sessionId: string) =>
    request<Session>(`/api/sessions/${sessionId}`, { method: "DELETE" }),
  messages: (sessionId: string) => request<{ messages: Message[] }>(`/api/sessions/${sessionId}/messages`),
  sendMessage: (sessionId: string, content: string, attachments: MessageAttachment[] = []) =>
    request<AgentResult>(`/api/sessions/${sessionId}/messages`, { method: "POST", body: JSON.stringify({ content, attachments }) }),
  sendMessageStream: async (sessionId: string, content: string, attachments: MessageAttachment[] = [], handlers: SendMessageStreamHandlers = {}) => {
    await requestEventStream(
      `/api/sessions/${sessionId}/messages/stream`,
      { method: "POST", body: JSON.stringify({ content, attachments }), signal: handlers.signal },
      (event, rawData) => {
        let data: unknown = rawData;
        try {
          data = JSON.parse(rawData);
        } catch {
          // Keep non-JSON stream data as text for diagnostics.
        }
        handlers.onEvent?.(event, data);
        if (event === "text_delta" && data && typeof data === "object") {
          const streamEvent = data as ModelStreamEvent;
          if (streamEvent.text) {
            handlers.onTextDelta?.(streamEvent.text, streamEvent);
          }
        } else if (event === "message.stream.final" && data && typeof data === "object") {
          handlers.onFinal?.(data as AgentResult);
        } else if (event === "error") {
          const message = data && typeof data === "object" && "error" in data ? String((data as { error?: unknown }).error ?? "Stream failed") : "Stream failed";
          handlers.onError?.(new Error(message));
        }
      }
    );
  },
  uploadDocument: (sessionId: string, file: File) => {
    const form = new FormData();
    form.append("file", file);
    if (sessionId) form.append("session_id", sessionId);
    return request<DocumentUploadResult>("/api/documents/upload", { method: "POST", body: form });
  },
  availableDocuments: (sessionId = "") => {
    const query = sessionId ? `?session_id=${encodeURIComponent(sessionId)}` : "";
    return request<{ documents: ArtifactObject[] }>(`/api/documents/available${query}`);
  },
  saveRunFeedback: (runId: string, messageId: string, rating: "up" | "down" | "corrected", note = "", correction = "") =>
    request<RunFeedback>(`/api/runs/${runId}/feedback`, {
      method: "POST",
      body: JSON.stringify({ message_id: messageId, rating, note, correction })
    }),
  toolCalls: (sessionId: string) => request<{ tool_calls: ToolCall[] }>(`/api/sessions/${sessionId}/tool-calls`),
  modelCalls: (sessionId: string) => request<{ model_calls: ModelCall[] }>(`/api/sessions/${sessionId}/model-calls`),
  audit: (sessionId: string) => request<{ audit_events: AuditEvent[] }>(`/api/sessions/${sessionId}/audit`),
  episodes: (sessionId: string) => request<{ episodes: EpisodeSummary[] }>(`/api/sessions/${sessionId}/episodes`),
  approvals: (status = "") => request<{ approvals: Approval[] }>(`/api/approvals${status ? `?status=${status}` : ""}`),
  approve: (id: string) =>
    request<ApprovalResolution>(`/api/approvals/${id}/approve`, { method: "POST", body: JSON.stringify({ note: "Approved from WebChat" }) }),
  modifyApproval: (id: string, args: Record<string, unknown>, note = "Modified from WebChat") =>
    request<ApprovalResolution>(`/api/approvals/${id}/modify`, { method: "POST", body: JSON.stringify({ note, args }) }),
  reject: (id: string) =>
    request<ApprovalResolution>(`/api/approvals/${id}/reject`, { method: "POST", body: JSON.stringify({ note: "Rejected from WebChat" }) }),
  memoryCandidates: (status = "") => request<{ memory_candidates: MemoryCandidate[] }>(`/api/memory-candidates${status ? `?status=${status}` : ""}`),
  acceptMemory: (id: string) => request<{ candidate: MemoryCandidate; memory: Memory }>(`/api/memory-candidates/${id}/accept`, { method: "POST" }),
  rejectMemory: (id: string) => request<MemoryCandidate>(`/api/memory-candidates/${id}/reject`, { method: "POST" }),
  memories: () => request<{ memories: Memory[] }>("/api/memories"),
  updateMemory: (id: string, kind: string, content: string) =>
    request<Memory>(`/api/memories/${id}/update`, { method: "POST", body: JSON.stringify({ kind, content }) }),
  deleteMemory: (id: string) => request<Memory>(`/api/memories/${id}/delete`, { method: "POST", body: "{}" }),
  archiveMemoryExport: () => request<MemoryExportArchive>("/api/memories/export", { method: "POST", body: "{}" }),
  runEval: (profile = "smoke") =>
    request<EvalRun>("/api/evals/run", { method: "POST", body: JSON.stringify({ profile }) }),
  evalRuns: () => request<{ eval_runs: EvalRun[] }>("/api/evals"),
  evalRun: (id: string) => request<EvalRun>(`/api/evals/${id}`),
  artifacts: () => request<{ artifacts: ArtifactObject[] }>("/api/artifacts"),
  traces: () => request<{ traces: TraceMetadata[] }>("/api/traces"),
  trace: (runId: string) => request<RunTrace>(`/api/traces/${runId}`)
};
