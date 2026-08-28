// Pure formatting/parsing helpers shared by App and the panel components.
import type { ArtifactObject, ModelCall, NotificationBinding, PublicConfig } from "../api/types";
import type { Copy, Language } from "../i18n";
import { clientTimezone } from "./timezone";

export type DocumentUsage = {
  count: number;
  last_used_at: string;
};

const DOCUMENT_USAGE_STORAGE_KEY = "sparkclaw.document_usage";

export function stripSystemArgs(args: Record<string, unknown>) {
  return Object.fromEntries(Object.entries(args).filter(([key]) => !key.startsWith("_")));
}

export function profileLabel(profile: PublicConfig["model"]["fast"], text: Copy) {
  const model = profile.model || profile.name;
  const inputTokens = profile.max_input_tokens ? ` · ${profile.max_input_tokens.toLocaleString()} ${text.units.input}` : "";
  const outputTokens = profile.max_tokens ? ` · ${profile.max_tokens.toLocaleString()} ${text.units.output}` : "";
  return `${profile.name} · ${model} · ${profile.context_tokens.toLocaleString()} ${text.units.ctx}${inputTokens}${outputTokens}${profile.mtp ? " · MTP" : ""}`;
}

export function rateLimitLabel(limit: { enabled: boolean; requests_per_minute: number; burst: number } | undefined, text: Copy) {
  if (!limit?.enabled) return text.common.disabled;
  return `${limit.requests_per_minute}/min · burst ${limit.burst}`;
}

export function retentionLabel(days: number, text: Copy) {
  if (!days || days <= 0) return text.settings.noAutoPrune;
  return `${days}${text.units.retentionDays}`;
}

export function parseToolList(value: string) {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const raw of value.split(/[\n,]/)) {
    const tool = raw.trim();
    if (!tool || seen.has(tool)) continue;
    seen.add(tool);
    out.push(tool);
  }
  return out;
}

export function formatPreferences(preferences: Record<string, string>) {
  return Object.entries(preferences)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([key, value]) => `${key}=${value}`)
    .join("\n");
}

export function parsePreferences(value: string, text: Copy) {
  const preferences: Record<string, string> = {};
  for (const line of value.split(/\n/)) {
    const trimmed = line.trim();
    if (!trimmed) continue;
    const separator = trimmed.indexOf("=");
    if (separator === -1) {
      throw new Error(text.settings.preferenceFormat);
    }
    const key = trimmed.slice(0, separator).trim();
    const itemValue = trimmed.slice(separator + 1).trim();
    if (!key) {
      throw new Error(text.settings.preferenceKey);
    }
    preferences[key] = itemValue;
  }
  return preferences;
}

export function formatRisk(risk: string, text: Copy) {
  return text.risk[risk as keyof Copy["risk"]] ?? risk;
}

export function formatState(state: string, text: Copy) {
  return text.state[state as keyof Copy["state"]] ?? state;
}

export function bindingStatusLabel(status: string, text: Copy) {
  switch (status) {
    case "waiting_scan":
      return text.settings.waitingScan;
    case "waiting_confirm":
      return text.settings.waitingConfirm;
    case "active":
      return text.settings.bound;
    case "expired":
      return text.settings.expired;
    default:
      return formatState(status, text);
  }
}

export function isBindingPending(status: string) {
  return status === "waiting_scan" || status === "waiting_confirm";
}

export function isVisibleNotificationBinding(status: string) {
  return isBindingPending(status) || status === "active";
}

export function sortNotificationBindings(bindings: NotificationBinding[]) {
  const rank = (binding: NotificationBinding) => {
    if (isBindingPending(binding.status)) return 0;
    if (binding.status === "active" && binding.default_for_channel) return 1;
    if (binding.status === "active") return 2;
    return 3;
  };
  return [...bindings].sort((left, right) => {
    const rankDelta = rank(left) - rank(right);
    if (rankDelta !== 0) return rankDelta;
    return new Date(right.updated_at).getTime() - new Date(left.updated_at).getTime();
  });
}

export function isImageLikeQR(value = "") {
  return value.startsWith("data:image/") || /^https?:\/\/.+\.(png|jpg|jpeg|webp|gif)(\?.*)?$/i.test(value) || isLikelyBase64Image(value);
}

export function qrImageSource(value = "") {
  if (!value || value.startsWith("data:image/") || value.startsWith("http://") || value.startsWith("https://")) return value;
  return `data:image/png;base64,${value}`;
}

export function isLikelyBase64Image(value = "") {
  const trimmed = value.trim();
  return trimmed.length > 100 && /^[A-Za-z0-9+/=\s]+$/.test(trimmed);
}

export function shortId(id: string) {
  return id.slice(0, 10);
}

export function fileNameFromPath(path: string) {
  return path.split(/[\\/]/).pop() || path;
}

export function loadDocumentUsage(): Record<string, DocumentUsage> {
  try {
    const raw = window.localStorage.getItem(DOCUMENT_USAGE_STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as Record<string, DocumentUsage>;
    if (!parsed || typeof parsed !== "object") return {};
    return parsed;
  } catch {
    return {};
  }
}

export function saveDocumentUsage(usage: Record<string, DocumentUsage>) {
  window.localStorage.setItem(DOCUMENT_USAGE_STORAGE_KEY, JSON.stringify(usage));
}

export function sortDocumentsByUsage(documents: ArtifactObject[], usage: Record<string, DocumentUsage>) {
  return [...documents].sort((a, b) => {
    const left = usage[a.key];
    const right = usage[b.key];
    const countDiff = (right?.count ?? 0) - (left?.count ?? 0);
    if (countDiff !== 0) return countDiff;
    const recentDiff = parseTime(right?.last_used_at) - parseTime(left?.last_used_at);
    if (recentDiff !== 0) return recentDiff;
    const createdDiff = parseTime(b.created_at) - parseTime(a.created_at);
    if (createdDiff !== 0) return createdDiff;
    return a.key.localeCompare(b.key, undefined, { numeric: true });
  });
}

export function parseTime(value = "") {
  const time = Date.parse(value);
  return Number.isNaN(time) ? 0 : time;
}

export function fileKindLabel(document: ArtifactObject) {
  const ext = fileNameFromPath(document.key).split(".").pop()?.toLowerCase() ?? "";
  switch (ext) {
    case "docx":
      return "Microsoft Word";
    case "xlsx":
      return "Microsoft Excel";
    case "pptx":
      return "Microsoft PowerPoint";
    case "pdf":
      return "PDF";
    case "csv":
      return "CSV";
    case "md":
      return "Markdown";
    case "txt":
      return "Text";
    default:
      return document.content_type || "Document";
  }
}

export function formatBytes(bytes = 0) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }
  return `${value >= 10 || unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

export function cssToken(value: string) {
  return value.toLowerCase().replace(/[^a-z0-9_-]+/g, "-");
}

export function formatLatency(calls: ModelCall[] | undefined, text: Copy) {
  if (!calls || calls.length === 0) return "0 ms";
  const total = calls.reduce((sum, call) => sum + call.latency_ms, 0);
  return `${Math.round(total / calls.length)} ms ${text.units.avg}`;
}

export function formatTime(value: string, language: Language, timezone = clientTimezone()) {
  const options: Intl.DateTimeFormatOptions = {
    hour: "2-digit",
    minute: "2-digit",
    ...(timezone ? { timeZone: timezone } : {})
  };
  try {
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", options).format(new Date(value));
  } catch {
    delete options.timeZone;
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", options).format(new Date(value));
  }
}

export function formatDateTime(value: string, language: Language, timezone = clientTimezone()) {
  const options: Intl.DateTimeFormatOptions = {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    ...(timezone ? { timeZone: timezone } : {})
  };
  try {
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", options).format(new Date(value));
  } catch {
    delete options.timeZone;
    return new Intl.DateTimeFormat(language === "zh" ? "zh-CN" : "en-US", options).format(new Date(value));
  }
}
