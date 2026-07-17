import { describe, expect, it } from "vitest";
import type { DeliveryEndpoint } from "../api/types";
import {
  deliveryDraftParts,
  deliveryPartFromAttachment,
  emptyExternalDeliveryDraft,
  moveDeliveryPart,
  selectDeliverySoftware,
  validateDeliveryDraft
} from "./deliveryDraft";

const endpoint = (id: string, channel: string, recipient: string): DeliveryEndpoint => ({
  id,
  channel,
  software_display_name: channel,
  account_display_name: `${channel} account`,
  recipient: { id: `recipient-${id}`, display_name: recipient },
  capabilities: {
    kinds: ["text", "image", "audio", "file"],
    dispositions: ["inline", "attachment", "voice_note"],
    file_fallback_kinds: ["audio"],
    native_voice_types: ["audio/ogg"],
    max_parts: 3,
    max_total_bytes: 100,
    supports_caption: true,
    supports_native_voice: true,
    supports_file_fallback: true
  }
});

describe("external delivery draft", () => {
  it("clears a recipient when software changes and only preselects a sole exact endpoint", () => {
    const endpoints = [endpoint("tg-a", "telegram", "Alex"), endpoint("tg-b", "telegram", "Alex"), endpoint("wx-a", "weixin", "Chen")];
    const selected = { ...emptyExternalDeliveryDraft(), software: "telegram", endpointId: "tg-a", text: "hello" };
    expect(selectDeliverySoftware(selected, "telegram", endpoints).endpointId).toBe("");
    expect(selectDeliverySoftware(selected, "weixin", endpoints).endpointId).toBe("wx-a");
    expect(selectDeliverySoftware(selected, "signal", endpoints).endpointId).toBe("");
  });

  it("keeps send disabled for ambiguity and enforces part and byte limits", () => {
    const target = endpoint("tg-a", "telegram", "Alex");
    const ambiguous = { ...emptyExternalDeliveryDraft(), software: "telegram", text: "hello" };
    expect(validateDeliveryDraft(ambiguous, target).error).toBe("recipient_required");
    const oversized = {
      ...ambiguous,
      endpointId: target.id,
      parts: [{ id: "audio", kind: "audio" as const, disposition: "voice_note" as const, artifact_id: "obj", content_type: "audio/wav", bytes: 101 }]
    };
    const result = validateDeliveryDraft(oversized, target);
    expect(result.error).toBe("payload_too_large");
    expect(result.fallbackPartIds).toEqual(["audio"]);
  });

  it("preserves canonical text, image, audio, and file parts in user order", () => {
    const image = deliveryPartFromAttachment("image", {
      artifact_id: "img-object",
      name: "photo.png",
      rel_path: "uploads/photo.png",
      content_type: "image/png",
      bytes: 20
    });
    const audio = deliveryPartFromAttachment("audio", {
      artifact_id: "audio-object",
      name: "note.wav",
      rel_path: "uploads/note.wav",
      content_type: "audio/wav",
      bytes: 30
    });
    const file = deliveryPartFromAttachment("file", {
      artifact_id: "file-object",
      name: "report.pdf",
      rel_path: "uploads/report.pdf",
      content_type: "application/pdf",
      bytes: 40
    });
    const reordered = moveDeliveryPart([image, audio, file], 2, -1);
    expect(reordered.map((part) => part.kind)).toEqual(["image", "file", "audio"]);
    expect(deliveryDraftParts({ software: "telegram", endpointId: "tg-a", text: " hello ", parts: reordered }).map((part) => part.kind))
      .toEqual(["text", "image", "file", "audio"]);
  });
});
