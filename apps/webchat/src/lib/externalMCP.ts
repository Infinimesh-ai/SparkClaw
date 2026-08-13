import type { MCPGrantOption, MCPRequestedGrant } from "../api/types";

export type MCPGrantSelection = Record<string, { operations: string[]; allowApproval: boolean }>;

export function buildMCPRequestedGrants(options: MCPGrantOption[], selection: MCPGrantSelection): MCPRequestedGrant[] {
  return options.flatMap((option) => {
    const selected = selection[option.capability_id];
    if (!selected) return [];
    const allowed = new Set(option.operations.map((item) => item.operation));
    const operations = [...new Set(selected.operations.filter((operation) => allowed.has(operation)))].sort();
    if (operations.length === 0) return [];
    const hasApprovalEffect = option.operations.some((item) => operations.includes(item.operation) && !isReadOnlyEffect(item.effect));
    return [{
      capability_id: option.capability_id,
      operations,
      allow_approval: hasApprovalEffect && selected.allowApproval
    }];
  });
}

export function isReadOnlyEffect(effect: string) {
  return effect === "external.read" || effect === "local.read" || effect === "local.compute" || effect === "workspace.read";
}
