import { describe, expect, it } from "vitest";
import { hasPersistedResultMessage, messageStreamFailureDisposition } from "./messageStream";

describe("message stream failure recovery", () => {
  it("refreshes an accepted request instead of replaying it", () => {
    expect(messageStreamFailureDisposition(true)).toBe("refresh_session");
  });

  it("restores an unaccepted request for explicit owner retry", () => {
    expect(messageStreamFailureDisposition(false)).toBe("restore_draft");
  });
});

describe("message stream final result", () => {
  it("distinguishes a suppressed WebChat result from a persisted assistant message", () => {
    expect(hasPersistedResultMessage({ id: "message-1" })).toBe(true);
    expect(hasPersistedResultMessage({ id: "" })).toBe(false);
    expect(hasPersistedResultMessage(undefined)).toBe(false);
  });
});
