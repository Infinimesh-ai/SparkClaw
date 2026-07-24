import type { DeliveryEndpoint, DeliveryPart, MessageAttachment, MessagePartKind } from "../api/types";

export type ExternalDeliveryDraft = {
  software: string;
  endpointId: string;
  text: string;
  parts: DeliveryPart[];
};

export type DeliveryDraftValidation = {
  valid: boolean;
  error: "" | "recipient_required" | "content_required" | "part_unsupported" | "too_many_parts" | "payload_too_large";
  totalBytes: number;
  fallbackPartIds: string[];
};

export function emptyExternalDeliveryDraft(): ExternalDeliveryDraft {
  return { software: "", endpointId: "", text: "", parts: [] };
}

export function endpointsForSoftware(endpoints: DeliveryEndpoint[], software: string) {
  return software ? endpoints.filter((endpoint) => endpoint.channel === software) : [];
}

export function selectDeliverySoftware(draft: ExternalDeliveryDraft, software: string): ExternalDeliveryDraft {
  return { ...draft, software, endpointId: "" };
}

export function deliveryDraftParts(draft: ExternalDeliveryDraft): DeliveryPart[] {
  const text = draft.text.trim();
  return [
    ...(text ? [{ id: "part-text", kind: "text" as const, disposition: "inline" as const, text }] : []),
    ...draft.parts
  ];
}

export function validateDeliveryDraft(draft: ExternalDeliveryDraft, endpoint?: DeliveryEndpoint): DeliveryDraftValidation {
  const parts = deliveryDraftParts(draft);
  const totalBytes = draft.parts.reduce((total, part) => total + (part.bytes ?? 0), 0);
  const fallbackPartIds = endpoint ? draft.parts.filter((part) => usesFileFallback(part, endpoint)).map((part) => part.id) : [];
  if (!endpoint || endpoint.id !== draft.endpointId) return { valid: false, error: "recipient_required", totalBytes, fallbackPartIds };
  if (parts.length === 0) return { valid: false, error: "content_required", totalBytes, fallbackPartIds };
  if (endpoint.capabilities.max_parts > 0 && parts.length > endpoint.capabilities.max_parts) {
    return { valid: false, error: "too_many_parts", totalBytes, fallbackPartIds };
  }
  for (const part of parts) {
    if (!endpoint.capabilities.kinds.includes(part.kind) || !endpoint.capabilities.dispositions.includes(part.disposition)) {
      return { valid: false, error: "part_unsupported", totalBytes, fallbackPartIds };
    }
    const partLimit = endpoint.capabilities.max_bytes_by_kind?.[part.kind] ?? 0;
    if (partLimit > 0 && (part.bytes ?? 0) > partLimit) {
      return { valid: false, error: "payload_too_large", totalBytes, fallbackPartIds };
    }
  }
  if (endpoint.capabilities.max_total_bytes > 0 && totalBytes > endpoint.capabilities.max_total_bytes) {
    return { valid: false, error: "payload_too_large", totalBytes, fallbackPartIds };
  }
  return { valid: true, error: "", totalBytes, fallbackPartIds };
}

export function deliveryPartFromAttachment(id: string, attachment: MessageAttachment): DeliveryPart {
  const kind = attachmentKind(attachment);
  return {
    id,
    kind,
    disposition: "attachment",
    artifact_id: attachment.artifact_id,
    name: attachment.name,
    content_type: attachment.content_type,
    bytes: attachment.bytes,
    caption: attachment.caption
  };
}

export function deliveryPartIDFromAttachment(attachment: MessageAttachment) {
  return `attachment:${attachment.artifact_id || attachment.rel_path}`;
}

export function moveDeliveryPart(parts: DeliveryPart[], index: number, offset: -1 | 1) {
  const target = index + offset;
  if (index < 0 || index >= parts.length || target < 0 || target >= parts.length) return parts;
  const next = [...parts];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

function usesFileFallback(part: DeliveryPart, endpoint: DeliveryEndpoint) {
  if (!endpoint.capabilities.file_fallback_kinds?.includes(part.kind)) return false;
  if (part.kind !== "audio") return true;
  if (part.disposition !== "voice_note") return true;
  return !endpoint.capabilities.native_voice_types?.includes((part.content_type ?? "").toLowerCase());
}

function attachmentKind(attachment: MessageAttachment): MessagePartKind {
  const contentType = (attachment.content_type ?? "").toLowerCase();
  if (contentType.startsWith("image/")) return "image";
  if (contentType.startsWith("audio/")) return "audio";
  const name = attachment.name.toLowerCase();
  if (/\.(png|jpe?g|gif|webp|avif|heic)$/.test(name)) return "image";
  if (/\.(aac|flac|m4a|mp3|ogg|oga|opus|wav)$/.test(name)) return "audio";
  return "file";
}
