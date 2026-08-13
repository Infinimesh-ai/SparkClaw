import { describe, expect, it } from "vitest";
import type { MCPGrantOption } from "../api/types";
import { buildMCPRequestedGrants } from "./externalMCP";

const options: MCPGrantOption[] = [{
  capability_id: "document.read",
  description: "Read documents",
  operations: [{ operation: "read", effect: "workspace.read" }, { operation: "edit", effect: "workspace.write" }],
  workflow: { id: "document.read", revision: 1 },
  projection_revision: 1
}];

describe("buildMCPRequestedGrants", () => {
  it("keeps only server-advertised operations and gates approval on effects", () => {
    expect(buildMCPRequestedGrants(options, {
      "document.read": { operations: ["unknown", "edit", "read", "edit"], allowApproval: true }
    })).toEqual([{ capability_id: "document.read", operations: ["edit", "read"], allow_approval: true }]);
    expect(buildMCPRequestedGrants(options, {
      "document.read": { operations: ["read"], allowApproval: true }
    })[0].allow_approval).toBe(false);
  });
});
