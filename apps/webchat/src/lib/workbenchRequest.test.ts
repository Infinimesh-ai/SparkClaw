import { describe, expect, it } from "vitest";
import { newWorkbenchRequestID, validateWorkbenchRequestStatus } from "./workbenchRequest";

describe("workbench request identity", () => {
  it("creates distinct explicit UUIDs without depending on a secure-context randomUUID API", () => {
    const ids = Array.from({ length: 10 }, newWorkbenchRequestID);
    expect(new Set(ids).size).toBe(10);
    expect(ids.every((id) => /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/u.test(id))).toBe(true);
  });
  it("cannot reconcile a different request or unknown status shape", () => {
    const requestID = newWorkbenchRequestID();
    const status = { schema_version: 1 as const, request_id: requestID, input_digest: "a".repeat(64), state: "unknown" as const };
    expect(validateWorkbenchRequestStatus(status, requestID)).toEqual(status);
    expect(() => validateWorkbenchRequestStatus(status, newWorkbenchRequestID())).toThrow(/identity/u);
    expect(() => validateWorkbenchRequestStatus({ ...status, input_digest: "bad" }, requestID)).toThrow(/identity/u);
  });
});
