import { describe, expect, it } from "vitest";
import { hasPersistedResultMessage, MessageStreamDeliveryError, messageStreamFailureDisposition } from "./messageStream";

describe("message stream failure recovery", () => {
  it("refreshes an accepted request instead of replaying it", () => {
    expect(messageStreamFailureDisposition(true)).toBe("refresh_session");
  });

  it("restores an unaccepted request for explicit owner retry", () => {
    expect(messageStreamFailureDisposition(false)).toBe("restore_draft");
  });

  it("keeps ordinary run errors on the accepted-stream path", () => {
    expect(messageStreamFailureDisposition(true, new Error("model failed"))).toBe("refresh_session");
  });

  it("surfaces a post-run delivery failure even though the stream was accepted", () => {
    expect(messageStreamFailureDisposition(true, new MessageStreamDeliveryError("provider unavailable"))).toBe("delivery_failed");
    expect(messageStreamFailureDisposition(false, new MessageStreamDeliveryError("provider unavailable"))).toBe("delivery_failed");
  });
});

describe("message stream final result", () => {
  it("distinguishes a suppressed WebChat result from a persisted assistant message", () => {
    expect(hasPersistedResultMessage({ id: "message-1" })).toBe(true);
    expect(hasPersistedResultMessage({ id: "" })).toBe(false);
    expect(hasPersistedResultMessage(undefined)).toBe(false);
  });
});
