// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MessageStreamDeliveryError } from "../lib/messageStream";
import { api, documentFileURL, messageStreamRequestBody } from "./client";

describe("documentFileURL", () => {
  it("keeps the workspace path scoped to its session", () => {
    const url = documentFileURL("media/20260720/weather card.png", "session-weather");
    expect(url).toContain("/api/documents/file?");
    expect(url).toContain("path=media%2F20260720%2Fweather+card.png");
    expect(url).toContain("session_id=session-weather");
  });
});

describe("messageStreamRequestBody", () => {
  it("changes only the final target when an endpoint is selected", () => {
    const attachments = [{ artifact_id: "artifact-file", name: "report.txt", rel_path: "uploads/report.txt" }];
    expect(messageStreamRequestBody("Summarize this report", attachments, "endpoint-selected")).toEqual({
      content: "Summarize this report",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });
    expect(messageStreamRequestBody("Summarize this report", attachments)).toEqual({
      content: "Summarize this report",
      attachments
    });

    expect(messageStreamRequestBody("", attachments, "endpoint-selected")).toEqual({
      content: "",
      attachments,
      target_endpoint_id: "endpoint-selected"
    });
  });
});

function sseResponse(payload: string) {
  const encoder = new TextEncoder();
  let sent = false;
  return {
    ok: true,
    body: {
      getReader: () => ({
        read: async () => {
          if (sent) return { value: undefined, done: true };
          sent = true;
          return { value: encoder.encode(payload), done: false };
        }
      })
    }
  };
}

describe("sendMessageStream failure events", () => {
  beforeEach(() => {
    // jsdom serves the suite from an opaque origin without localStorage;
    // apiToken() only needs a null read.
    vi.stubGlobal("localStorage", { getItem: () => null });
  });
  afterEach(() => vi.unstubAllGlobals());

  it("raises a typed delivery error for the gateway's delivery_failed event", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse(
      'event: message.stream.started\ndata: {"session_id":"s1"}\n\n' +
      'event: message.stream.delivery_failed\ndata: {"error":"provider temporarily unavailable","session_id":"s1"}\n\n'
    )));
    const errors: Error[] = [];
    await api.sendMessageStream("s1", "hello", [], { onError: (error) => errors.push(error) });
    expect(errors).toHaveLength(1);
    expect(errors[0]).toBeInstanceOf(MessageStreamDeliveryError);
    expect(errors[0].message).toBe("provider temporarily unavailable");
  });

  it("keeps run failures as plain stream errors", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => sseResponse(
      'event: error\ndata: {"error":"model failed","session_id":"s1"}\n\n'
    )));
    const errors: Error[] = [];
    await api.sendMessageStream("s1", "hello", [], { onError: (error) => errors.push(error) });
    expect(errors).toHaveLength(1);
    expect(errors[0]).not.toBeInstanceOf(MessageStreamDeliveryError);
    expect(errors[0].message).toBe("model failed");
  });
});
