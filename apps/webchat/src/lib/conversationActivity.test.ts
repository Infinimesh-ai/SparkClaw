import { describe, expect, it } from "vitest";
import { conversationActivity, localExecutionStates } from "./conversationActivity";

describe("conversation task activity", () => {
  it.each(["submission_pending", "accepted", "running", "approval_pending", "browser_login_blocked", "cancel_pending"])("keeps %s visibly in progress", state => {
    expect(conversationActivity(["completed", state])).toBe("running");
  });
  it.each(["completed", "delivered", "saved"])("shows the completion dot for %s", state => {
    expect(conversationActivity([state, "failed"])).toBe("completed");
  });
  it.each(["failed", "canceled", "unknown", "blocked", "delivery_failed", "delivery_expired", "delivery_too_large", "awaiting_runtime"])("does not treat %s as active or successful", state => {
    expect(conversationActivity([state, "delivered"])).toBeUndefined();
  });
  it("ignores future and missed schedules when projecting local task history", () => {
    const tasks = ["delivered", "scheduled_local", "schedule_missed", "schedule_canceled"].map(status => ({ status }));
    expect(conversationActivity(localExecutionStates(tasks))).toBe("completed");
    expect(conversationActivity(localExecutionStates([...tasks, { status: "failed" }]))).toBeUndefined();
    expect(conversationActivity([])).toBeUndefined();
  });
});
