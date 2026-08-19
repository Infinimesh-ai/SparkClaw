import { describe, expect, it } from "vitest";
import type { Schedule } from "../api/types";
import { formatScheduleTime, schedulePattern } from "./schedules";

const schedule: Schedule = {
  id: "schedule-1",
  title: "Daily check",
  text: "Daily check",
  due_time: "2026-07-21T01:30:00Z",
  timezone: "Asia/Shanghai",
  recurrence: "daily",
  status: "pending",
  updated_at: "2026-07-20T01:30:00Z",
  editable: true,
  cancelable: true,
  endpoint: { kind: "web", channel: "web", software_display_name: "WebChat", status: "active" }
};

describe("schedule formatting", () => {
  it("formats the next run in the client timezone instead of the schedule timezone", () => {
    expect(formatScheduleTime(schedule, "zh", "UTC")).toContain("01:30");
  });

  it("maps known recurrence values and preserves custom expressions", () => {
    const labels = { oneTime: "once", daily: "daily", weekly: "weekly", monthly: "monthly" };
    expect(schedulePattern(schedule, labels)).toBe("daily");
    expect(schedulePattern({ ...schedule, recurrence: "0 9 * * 1-5" }, labels)).toBe("0 9 * * 1-5");
    expect(schedulePattern({ ...schedule, recurrence: "" }, labels)).toBe("once");
  });
});
