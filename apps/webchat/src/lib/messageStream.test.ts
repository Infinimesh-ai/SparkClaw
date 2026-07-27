import { describe, expect, it } from "vitest";
import { messageStreamFailureDisposition } from "./messageStream";

describe("message stream failure recovery", () => {
  it("refreshes an accepted request instead of replaying it", () => {
    expect(messageStreamFailureDisposition(true)).toBe("refresh_session");
  });

  it("restores an unaccepted request for explicit owner retry", () => {
    expect(messageStreamFailureDisposition(false)).toBe("restore_draft");
  });
});
