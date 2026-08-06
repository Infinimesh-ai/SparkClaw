import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Approval } from "../api/types";
import { dictionaries } from "../i18n";
import { ApprovalPanel } from "./panels";

function happyApproval(planAvailability: string, plan = "# Plan\n\nInspect, implement, verify."): Approval {
  return {
    id: "ap-happy",
    source: "happy_team_plan",
    external_id: "task-1",
    external_context: {
      provider: "happy-team",
      title: "Implement feature",
      goal_prompt: "Implement the requested feature safely.",
      plan,
      plan_availability: planAvailability
    },
    session_id: "",
    run_id: "",
    tool_call_id: "",
    tool: "happy-team.review_plan",
    risk: "dangerous",
    status: "pending",
    summary: "Review Happy Team plan",
    reason: "Human decision required",
    resources: ["happy-task:task-1"],
    arguments: { taskId: "task-1" },
    created_at: "2026-08-06T00:00:00Z"
  };
}

describe("ApprovalPanel Happy plans", () => {
  it("renders task goal and plan without a raw argument editor", () => {
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[happyApproval("available")]} text={dictionaries.en} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup).toContain("Implement feature");
    expect(markup).toContain("Implement the requested feature safely.");
    expect(markup).toContain("Inspect, implement, verify.");
    expect(markup).not.toContain("taskId");
    expect(markup).not.toContain("disabled");
  });

  it("disables approval and plan editing while the member machine is offline", () => {
    const markup = renderToStaticMarkup(
      <ApprovalPanel approvals={[happyApproval("temporarily_unavailable", "")]} text={dictionaries.en} onResolve={() => {}} onModify={() => {}} onModifyPlan={() => {}} />
    );
    expect(markup).toContain(dictionaries.en.approval.planUnavailable);
    expect(markup.match(/disabled/g)?.length).toBe(2);
  });
});
