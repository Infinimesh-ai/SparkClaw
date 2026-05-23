import type {
  AgentResult,
  ArtifactObject,
  AuditEvent,
  Approval,
  ApprovalResolution,
  Client,
  EpisodeSummary,
  EvalRun,
  Memory,
  MemoryCandidate,
  MemoryExportArchive,
  Message,
  ModelCall,
  OwnerProfile,
  PublicConfig,
  ReadyStatus,
  RunFeedback,
  RunTrace,
  Session,
  Skill,
  TraceMetadata,
  ToolCall
} from "./types";

const API_BASE = import.meta.env.VITE_SPARKCLAW_API_BASE ?? "";
const TOKEN_STORAGE_KEY = "sparkclaw.api_token";

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
  const response = await fetch(`${API_BASE}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(apiToken() ? { Authorization: `Bearer ${apiToken()}` } : {}),
      ...(init?.headers ?? {})
    }
  });
  if (!response.ok) {
    const body = await response.json().catch(() => ({}));
    throw new Error(body.error ?? `HTTP ${response.status}`);
  }
  return response.json() as Promise<T>;
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

export const api = {
  ready: () => request<ReadyStatus>("/readyz"),
  config: () => request<PublicConfig>("/api/config"),
  owner: () => request<OwnerProfile>("/api/owner"),
  updateOwner: (displayName: string, email: string, preferences: Record<string, string>) =>
    request<OwnerProfile>("/api/owner", {
      method: "POST",
      body: JSON.stringify({ display_name: displayName, email, preferences })
    }),
  clients: () => request<{ clients: Client[] }>("/api/clients"),
  revokeClient: (id: string) => request<Client>(`/api/clients/${id}/revoke`, { method: "POST", body: "{}" }),
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
  createSession: (title = "SparkClaw Session") =>
    request<Session>("/api/sessions", { method: "POST", body: JSON.stringify({ title }) }),
  messages: (sessionId: string) => request<{ messages: Message[] }>(`/api/sessions/${sessionId}/messages`),
  sendMessage: (sessionId: string, content: string) =>
    request<AgentResult>(`/api/sessions/${sessionId}/messages`, { method: "POST", body: JSON.stringify({ content }) }),
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
  trace: (runId: string) => request<RunTrace>(`/api/traces/${runId}`),
  skills: () => request<{ skills: Skill[] }>("/api/skills")
};
